package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/DrSmithFr/web-ide/lib/i18n"
	"github.com/DrSmithFr/web-ide/lib/tunnel"
	"github.com/DrSmithFr/web-ide/pod/plugins"
	"github.com/DrSmithFr/web-ide/pod/store"
)

// LinksFile is the links of this pod to the services of the other pods (relay.md, *Services*).
const LinksFile = "service-links.json"

// The scopes of a link: who reaches its port.
const (
	ScopeLocal = "local" // this machine (127.0.0.1)
	ScopeLAN   = "lan"   // the local network (0.0.0.0): anyone there, with no authentication
)

// The states of a link that is on (StateOff when it is not).
const (
	StateOff       = "off"
	StateOn        = "on"
	StateOffline   = "offline"    // the pod does not answer: the listener stays, 502, retried
	StateCut       = "cut"        // the pod cut it: the link turned off
	StateGone      = "gone"       // the pod no longer shares the service (or it is off there)
	StatePortTaken = "port-taken" // the port typed is taken on this machine
)

// Link is a service of another pod linked to a port of this machine.
type Link struct {
	ID      string `json:"id"`
	Pod     string `json:"pod"` // its device key
	PodName string `json:"podName"`
	Service string `json:"service"`
	Name    string `json:"name"`
	// Port is the one typed (0: the port of the service, else the next free one).
	Port  int    `json:"port"`
	Scope string `json:"scope"`
	On    bool   `json:"on"`
	// ServicePort is the port of the service on its pod, learned at the attach: the default port,
	// known again after a restart while the pod is offline.
	ServicePort int `json:"servicePort,omitempty"`
}

// LinkView is a link as the page shows it.
type LinkView struct {
	Link
	State    string       `json:"state"`
	Listen   int          `json:"listen,omitempty"` // the port it listens on now
	Route    tunnel.Route `json:"route"`
	Requests int64        `json:"requests"`
	Bytes    int64        `json:"bytes"`
}

// Links holds the links of this pod and runs a listener for each one that is on.
type Links struct {
	st      *store.Store
	peers   plugins.Peers
	changed func()

	mu   sync.Mutex
	list []Link
	runs map[string]*linkRun
	// ended is why a link turned off by itself (cut by its pod), shown until it is on again.
	ended map[string]string
}

// linkRun is a link that is on: its listener and what it did.
type linkRun struct {
	ln       net.Listener
	srv      *http.Server
	port     int
	state    atomic.Value // string
	requests atomic.Int64
	bytes    atomic.Int64
}

// LoadLinks reads the links; Start runs the ones that are on.
func LoadLinks(st *store.Store, peers plugins.Peers, changed func()) (*Links, error) {
	l := &Links{st: st, peers: peers, changed: changed, runs: map[string]*linkRun{}, ended: map[string]string{}}
	if err := st.ReadJSON(LinksFile, &l.list); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return l, err
	}
	return l, nil
}

// Start turns on the links that were on (the pod started): no window needed.
func (l *Links) Start() {
	l.mu.Lock()
	var on []Link
	for _, k := range l.list {
		if k.On {
			on = append(on, k)
		}
	}
	l.mu.Unlock()
	for _, k := range on {
		go l.run(k, false) // a pod reached through the relay takes a while: one at a time would add up
	}
}

// Stop closes every listener (the pod stops; the links stay on for the next one).
func (l *Links) Stop() {
	l.mu.Lock()
	runs := l.runs
	l.runs = map[string]*linkRun{}
	l.mu.Unlock()
	for _, r := range runs {
		_ = r.srv.Close()
	}
}

// List is every link with its state.
func (l *Links) List() []LinkView {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := []LinkView{}
	for _, k := range l.list {
		v := LinkView{Link: k, State: StateOff}
		if r := l.runs[k.ID]; r != nil {
			v.State, v.Listen = r.state.Load().(string), r.port
			v.Requests, v.Bytes = r.requests.Load(), r.bytes.Load()
		} else if k.On && k.Port == 0 && k.ServicePort == 0 {
			v.State = StateOffline // the port of the service is not known yet: retried
		} else if k.On {
			v.State = StatePortTaken
		} else if e := l.ended[k.ID]; e != "" {
			v.State = e
		}
		if k.On {
			v.Route = l.peers.Route(k.Pod)
		}
		out = append(out, v)
	}
	return out
}

// Set turns the link to a service of a pod on (a new one, or its port and scope changed) or off.
func (l *Links) Set(k Link) (LinkView, error) {
	if k.Scope != ScopeLAN {
		k.Scope = ScopeLocal
	}
	if k.Port < 0 || k.Port > 65535 {
		return LinkView{}, i18n.Errorf("the port must be between 1 and 65535")
	}
	l.mu.Lock()
	i := slices.IndexFunc(l.list, func(o Link) bool { return o.Pod == k.Pod && o.Service == k.Service })
	if i >= 0 {
		k.ID = l.list[i].ID
	} else {
		k.ID = newID()
	}
	delete(l.ended, k.ID)
	if i >= 0 {
		k.ServicePort = l.list[i].ServicePort
	}
	l.mu.Unlock()
	l.stop(k.ID)
	if k.On {
		l.save(k)
		l.run(k, true)
	} else {
		l.detach(k)
		l.save(k)
	}
	l.changed()
	for _, v := range l.List() {
		if v.ID == k.ID {
			return v, nil
		}
	}
	return LinkView{}, nil
}

// Delete forgets a link.
func (l *Links) Delete(id string) {
	l.mu.Lock()
	i := slices.IndexFunc(l.list, func(o Link) bool { return o.ID == id })
	var k Link
	if i >= 0 {
		k = l.list[i]
		l.list = slices.Delete(l.list, i, i+1)
		_ = l.st.WriteJSON(LinksFile, l.list)
	}
	l.mu.Unlock()
	if i >= 0 {
		l.stop(id)
		l.detach(k)
		l.changed()
	}
}

func (l *Links) save(k Link) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if i := slices.IndexFunc(l.list, func(o Link) bool { return o.ID == k.ID }); i >= 0 {
		l.list[i] = k
	} else {
		l.list = append(l.list, k)
	}
	_ = l.st.WriteJSON(LinksFile, l.list)
}

func (l *Links) stop(id string) {
	l.mu.Lock()
	r := l.runs[id]
	delete(l.runs, id)
	l.mu.Unlock()
	if r != nil {
		_ = r.srv.Close()
	}
}

// run attaches the link on its pod (relink: the user turned it on) and opens its listener.
func (l *Links) run(k Link, relink bool) {
	state, port := l.attach(k, relink)
	if state == StateCut {
		l.turnOff(k.ID, StateCut)
		return
	}
	if port > 0 && port != k.ServicePort {
		k.ServicePort = port
		l.save(k)
	}
	want := k.Port
	if want == 0 {
		want = k.ServicePort
	}
	if want == 0 {
		// Offline before its port was ever known: tried again.
		time.AfterFunc(30*time.Second, func() { l.retry(k.ID) })
		l.changed()
		return
	}
	host := "127.0.0.1"
	if k.Scope == ScopeLAN {
		host = "0.0.0.0"
	}
	var ln net.Listener
	var err error
	// The port typed is that one or none; the port of the service may be taken here (the same
	// service running on this machine too): the next free one.
	tries := 1
	if k.Port == 0 {
		tries = 20
	}
	for i := 0; i < tries && want > 0; i++ {
		if ln, err = net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(want+i))); err == nil {
			break
		}
	}
	if ln == nil {
		l.changed() // port taken: shown, the link stays on
		return
	}
	r := &linkRun{ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
	r.state.Store(state)
	r.srv = &http.Server{Handler: l.proxy(k, r), ReadHeaderTimeout: 30 * time.Second}
	l.mu.Lock()
	l.runs[k.ID] = r
	l.mu.Unlock()
	go func() { _ = r.srv.Serve(ln) }()
}

// retry runs a link that is on and still has no listener.
func (l *Links) retry(id string) {
	l.mu.Lock()
	i := slices.IndexFunc(l.list, func(o Link) bool { return o.ID == id })
	running := l.runs[id] != nil
	var k Link
	if i >= 0 {
		k = l.list[i]
	}
	l.mu.Unlock()
	if i >= 0 && k.On && !running {
		l.run(k, false)
	}
}

// attach asks the pod for the link: on (with the port of the service), cut, gone, or offline.
func (l *Links) attach(k Link, relink bool) (string, int) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.base(k)+"$attach", nil)
	if relink {
		req.Header.Set(RelinkHeader, "1")
	}
	resp, err := l.peers.Transport().RoundTrip(req)
	if err != nil {
		return StateOffline, 0
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
		var a struct {
			Port int `json:"port"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 1024)).Decode(&a)
		return StateOn, a.Port
	case http.StatusConflict:
		return StateCut, 0
	case http.StatusNotFound:
		return StateGone, 0
	}
	return StateOffline, 0
}

func (l *Links) detach(k Link) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, l.base(k)+"$detach", nil)
	if resp, err := l.peers.Transport().RoundTrip(req); err == nil {
		resp.Body.Close()
	}
}

// turnOff turns a link off by itself (cut by its pod): saved off, the state kept for the page.
func (l *Links) turnOff(id, state string) {
	l.stop(id)
	l.mu.Lock()
	l.ended[id] = state
	i := slices.IndexFunc(l.list, func(o Link) bool { return o.ID == id })
	if i >= 0 {
		l.list[i].On = false
		_ = l.st.WriteJSON(LinksFile, l.list)
	}
	l.mu.Unlock()
	l.changed()
}

// base is the route of the service on its pod.
func (l *Links) base(k Link) string {
	return "http://" + k.Pod + "/_peer/services/" + url.PathEscape(k.Service) + "/"
}

// proxy serves the listener of a link: each request goes to the pod, the route chosen by the
// transport per request; an answer streamed, a WebSocket upgraded.
func (l *Links) proxy(k Link, r *linkRun) http.Handler {
	target, _ := url.Parse(l.base(k))
	setState := func(s string) {
		if r.state.Swap(s) != s {
			l.changed()
		}
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = k.Pod
		},
		Transport:     &reattach{l: l, k: k, cut: func() { go l.turnOff(k.ID, StateCut) }},
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			if resp.Header.Get(ServiceHeader) == "off" {
				setState(StateGone)
			} else {
				setState(StateOn)
			}
			resp.Body = &countBody{ReadCloser: resp.Body, n: &r.bytes}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, req *http.Request, err error) {
			setState(StateOffline)
			http.Error(w, i18n.T(i18n.Detect(req.Header.Get("Accept-Language")), "%s is offline", k.PodName), http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		rp.ServeHTTP(w, req)
	})
}

// reattach sends a request to the pod; refused as not attached (the pod restarted and forgot its
// links), it attaches again by itself and sends it once more; refused because the pod cut it, the
// link turns off.
type reattach struct {
	l   *Links
	k   Link
	cut func()
}

func (t *reattach) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil && req.Body != http.NoBody {
		// A body is read once: kept to send it again (a request to a service is small; a big
		// upload is not sent twice).
		b, err := io.ReadAll(io.LimitReader(req.Body, 4<<20))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
		req.Body = io.NopCloser(bytes.NewReader(body))
	}
	resp, err := t.l.peers.Transport().RoundTrip(req)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		return resp, err
	}
	state, _ := t.l.attach(t.k, false)
	switch state {
	case StateCut:
		t.cut()
		return resp, nil
	case StateOn:
		resp.Body.Close()
		again := req.Clone(req.Context())
		if body != nil {
			again.Body = io.NopCloser(bytes.NewReader(body))
		}
		return t.l.peers.Transport().RoundTrip(again)
	}
	return resp, nil
}

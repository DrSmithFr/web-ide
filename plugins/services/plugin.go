// Package services is the plugin of the shared services (docs/design/relay.md, *Services*): an
// HTTP service of this machine (Ollama, a web UI) the other pods of the account reach. Pod-wide,
// like the previews: the services are the pod's (services.json), its methods are global and its
// part of the page is a section of the home page. The core lets only the pods of the account
// reach its handler (Host.HandlePeer, /_peer/services/), and publishes the services that are on
// in the project list and the LAN $info (Host.Advertise).
package services

import (
	"context"
	_ "embed"
	"encoding/json"
	"log"
	"net/http"

	"antennae.space/ide/lib/tunnel"

	"antennae.space/ide/pod/plugins"
)

//go:embed plugin.json
var manifest []byte

type Plugin struct{ m plugins.Manifest }

func New() *Plugin {
	m, err := plugins.Parse(manifest)
	if err != nil {
		panic(err)
	}
	return &Plugin{m: m}
}

func (p *Plugin) Manifest() plugins.Manifest { return p.m }

func bind[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(raw) == 0 || string(raw) == "null" {
		return v, nil
	}
	err := json.Unmarshal(raw, &v)
	return v, err
}

func (p *Plugin) Register(h plugins.Host) {
	pod := h.Pod()
	var m *Manager
	// The attached pods by the name the account knows them by (their certificate holds the host
	// name).
	list := func() []View {
		names := map[string]string{}
		for _, p := range pod.Peers().List() {
			names[p.Key] = p.Name
		}
		l := m.List()
		for i := range l {
			for j, a := range l[i].Attached {
				if n := names[a.Key]; n != "" {
					l[i].Attached[j].Name = n
				}
			}
		}
		return l
	}
	m, err := Load(pod.Services().Store, func() {
		pod.Broadcast("services.changed", list())
		pod.AdvertiseChanged()
	})
	if err != nil {
		log.Printf("services: %s: %v", File, err)
	}
	h.HandlePeer(m.Handler())
	h.Advertise("services", func() any { return m.Published() })

	// The links of this pod to the services of the other pods.
	links, err := LoadLinks(pod.Services().Store, lazyPeers{pod}, func() { pod.Broadcast("services.links", nil) })
	if err != nil {
		log.Printf("services: %s: %v", LinksFile, err)
	}
	h.OnStart(links.Start)
	h.OnStop(links.Stop)
	h.HandleGlobal("services.remote", func(ctx context.Context, c plugins.Call) (any, error) {
		return remote(pod.Peers(), links), nil
	})
	h.HandleGlobal("services.link", func(ctx context.Context, c plugins.Call) (any, error) {
		k, err := bind[Link](c.Params)
		if err != nil {
			return nil, err
		}
		return links.Set(k)
	})
	h.HandleGlobal("services.forget", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		links.Delete(a.ID)
		return nil, nil
	})

	h.HandleGlobal("services.list", func(ctx context.Context, c plugins.Call) (any, error) {
		return list(), nil
	})
	h.HandleGlobal("services.save", func(ctx context.Context, c plugins.Call) (any, error) {
		s, err := bind[Service](c.Params)
		if err != nil {
			return nil, err
		}
		return m.Save(s)
	})
	h.HandleGlobal("services.delete", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID string `json:"id"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		return nil, m.Delete(a.ID)
	})
	// Cut detaches a pod: its next request is refused (#79 tells it, its link shows cut).
	h.HandleGlobal("services.cut", func(ctx context.Context, c plugins.Call) (any, error) {
		a, err := bind[struct {
			ID  string `json:"id"`
			Pod string `json:"pod"`
		}](c.Params)
		if err != nil {
			return nil, err
		}
		m.Cut(a.ID, a.Pod)
		return list(), nil
	})
}

// lazyPeers asks the pod for its way to the other pods at each use: the pod sets it after the
// plugins registered.
type lazyPeers struct{ pod plugins.Pod }

func (p lazyPeers) Transport() http.RoundTripper  { return p.pod.Peers().Transport() }
func (p lazyPeers) Route(key string) tunnel.Route { return p.pod.Peers().Route(key) }
func (p lazyPeers) List() []plugins.PeerPod       { return p.pod.Peers().List() }

// RemotePod is another pod of the account with the services it shares and the links of this pod
// to them, as the home page shows it.
type RemotePod struct {
	Key      string          `json:"key"`
	Name     string          `json:"name"`
	Online   bool            `json:"online"`
	Route    tunnel.Route    `json:"route"`
	Services []RemoteService `json:"services"`
}

// RemoteService is a service of another pod, with the link of this pod if it has one.
type RemoteService struct {
	ID   string    `json:"id"`
	Name string    `json:"name"`
	Link *LinkView `json:"link,omitempty"`
}

// remote lists the services of the other pods, and the links to services no longer shared.
func remote(peers plugins.Peers, links *Links) []RemotePod {
	byPod := map[string][]LinkView{}
	for _, v := range links.List() {
		byPod[v.Pod] = append(byPod[v.Pod], v)
	}
	out := []RemotePod{}
	seen := map[string]bool{}
	add := func(p RemotePod, refs []Ref) {
		used := map[string]bool{}
		for _, r := range refs {
			s := RemoteService{ID: r.ID, Name: r.Name}
			for _, v := range byPod[p.Key] {
				if v.Service == r.ID {
					s.Link = &v
					used[r.ID] = true
				}
			}
			p.Services = append(p.Services, s)
		}
		for _, v := range byPod[p.Key] {
			if !used[v.Service] {
				v := v
				p.Services = append(p.Services, RemoteService{ID: v.Service, Name: v.Name, Link: &v})
			}
		}
		if len(p.Services) > 0 {
			out = append(out, p)
		}
		seen[p.Key] = true
	}
	for _, pp := range peers.List() {
		var refs []Ref
		_ = json.Unmarshal(pp.Advertised["services"], &refs)
		add(RemotePod{Key: pp.Key, Name: pp.Name, Online: pp.Online, Route: peers.Route(pp.Key), Services: []RemoteService{}}, refs)
	}
	// A link to a pod the directory no longer lists.
	for key, l := range byPod {
		if !seen[key] {
			add(RemotePod{Key: key, Name: l[0].PodName, Route: peers.Route(key), Services: []RemoteService{}}, nil)
		}
	}
	return out
}

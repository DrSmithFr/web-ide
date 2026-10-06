package preview

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/coder/websocket"
)

type fakeTS struct {
	mu  sync.Mutex
	on  map[int]string // port → target (funnel ports too)
	err error
}

func (f *fakeTS) Host() (string, bool) { return "box.tail.ts.net", true }
func (f *fakeTS) Serve(port int, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.on[port] = target
	return nil
}
func (f *fakeTS) Funnel(port int, target string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return f.err
	}
	f.on[port] = "funnel " + target
	return nil
}
func (f *fakeTS) Off(port int, funnel bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.on, port)
	return nil
}
func (f *fakeTS) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.on) }

// app is a fake dev server: it echoes its Host and cookies, redirects /go to its own
// address, and echoes WebSocket messages on /ws.
func app(t *testing.T) int {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ws":
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			typ, data, err := c.Read(r.Context())
			if err == nil {
				_ = c.Write(r.Context(), typ, append([]byte("echo "), data...))
			}
		case "/go":
			http.Redirect(w, r, "http://localhost:"+strings.Split(r.Host, ":")[1]+"/there", http.StatusFound)
		default:
			fmt.Fprintf(w, "app host=%s cookie=%s", r.Host, r.Header.Get("Cookie"))
		}
	}))
	t.Cleanup(srv.Close)
	port, _ := strconv.Atoi(strings.Split(srv.Listener.Addr().String(), ":")[1])
	return port
}

const ideToken = "secret"

func authorized(r *http.Request) bool {
	c, err := r.Cookie("webide_token")
	return err == nil && c.Value == ideToken
}

// local is the address of the listener of a preview (the URL is on the tailnet).
func local(m *Manager, id string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return "http://" + m.previews[id].l.Addr().String()
}

func get(t *testing.T, c *http.Client, u string, cookie string) (int, string) {
	req, _ := http.NewRequest("GET", u, nil)
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	return res.StatusCode, string(body)
}

func TestPrivatePreviewNeedsTheIDECookie(t *testing.T) {
	port := app(t)
	ts := &fakeTS{on: map[int]string{}}
	m := New(ts, authorized, nil)
	defer m.Shutdown()
	st, err := m.Start(Spec{Project: "p", Command: "npm run dev", AppPort: port}, "c1", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if st.URL != "https://box.tail.ts.net:8401/" || !st.Tailscale || ts.on[8401] != local(m, st.ID) {
		t.Fatalf("url %q, serve %v", st.URL, ts.on)
	}
	base := local(m, st.ID)
	if code, body := get(t, http.DefaultClient, base+"/", ""); code != 401 || !strings.Contains(body, "private") {
		t.Fatalf("without cookie: %d %s", code, body)
	}
	code, body := get(t, http.DefaultClient, base+"/", "webide_token="+ideToken+"; theme=dark")
	if code != 200 || body != fmt.Sprintf("app host=localhost:%d cookie=theme=dark", port) {
		t.Fatalf("with cookie: %d %s", code, body)
	}
	// A second preview takes the next port; the same spec is found again.
	if f, ok := m.Find(Spec{Project: "p", Command: "npm run dev", AppPort: port}); !ok || f.ID != st.ID {
		t.Fatal("not found again")
	}
	st2, _ := m.Start(Spec{Project: "p", Command: "other", AppPort: port}, "c2", nil, nil)
	if !strings.Contains(st2.URL, ":8402/") {
		t.Fatal(st2.URL)
	}
	// Redirections to the app's own address stay on the preview.
	nofollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest("GET", base+"/go", nil)
	req.Header.Set("Cookie", "webide_token="+ideToken)
	res, err := nofollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.Header.Get("Location") != "/there" {
		t.Fatal(res.Header.Get("Location"))
	}
}

func TestPublicPreviewWithItsToken(t *testing.T) {
	port := app(t)
	ts := &fakeTS{on: map[int]string{}}
	m := New(ts, authorized, nil)
	defer m.Shutdown()
	st, _ := m.Start(Spec{Project: "p", Command: "x", AppPort: port}, "c1", nil, nil)
	base := local(m, st.ID)
	pub, err := m.SetPublic(st.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(pub.PublicURL, "https://box.tail.ts.net:8443/?t=") || !strings.HasPrefix(ts.on[8443], "funnel ") {
		t.Fatal(pub.PublicURL, ts.on)
	}
	token := strings.Split(pub.PublicURL, "?t=")[1]
	if code, _ := get(t, http.DefaultClient, base+"/?t=wrong", ""); code != 401 {
		t.Fatal("wrong token accepted")
	}
	jar, _ := cookiejar.New(nil)
	visitor := &http.Client{Jar: jar}
	code, body := get(t, visitor, base+"/page?t="+token, "")
	if code != 200 || strings.Contains(body, "webide_preview") {
		t.Fatalf("token: %d %s", code, body)
	}
	// The cookie is enough afterwards.
	if code, _ := get(t, visitor, base+"/other", ""); code != 200 {
		t.Fatal("cookie refused", code)
	}
	// Three public previews: Funnel has two ports.
	st2, _ := m.Start(Spec{Project: "p", Command: "y", AppPort: port}, "c2", nil, nil)
	st3, _ := m.Start(Spec{Project: "p", Command: "z", AppPort: port}, "c3", nil, nil)
	if _, err := m.SetPublic(st2.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.SetPublic(st3.ID, true); err == nil {
		t.Fatal("a third public preview")
	}
	// Private again: the token no longer works, Funnel is off.
	if _, err := m.SetPublic(st.ID, false); err != nil {
		t.Fatal(err)
	}
	if _, ok := ts.on[8443]; ok {
		t.Fatal("funnel still on")
	}
	if code, _ := get(t, &http.Client{Jar: jar}, base+"/other", ""); code != 401 {
		t.Fatal("private preview open with the token")
	}
}

func TestWebSocketThroughThePreview(t *testing.T) {
	port := app(t)
	var dials atomic.Int32
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials.Add(1) // as an SSH project does
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	m := New(nil, authorized, nil)
	defer m.Shutdown()
	st, _ := m.Start(Spec{Project: "p", Command: "x", AppPort: port}, "c1", dial, nil)
	if st.Tailscale || !strings.HasPrefix(st.URL, "http://127.0.0.1:") {
		t.Fatal(st.URL)
	}
	ctx := context.Background()
	h := http.Header{}
	h.Set("Cookie", "webide_token="+ideToken)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(st.URL, "http")+"/ws", &websocket.DialOptions{HTTPHeader: h})
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	_ = c.Write(ctx, websocket.MessageText, []byte("hmr"))
	_, data, err := c.Read(ctx)
	if err != nil || string(data) != "echo hmr" {
		t.Fatal(string(data), err)
	}
	if dials.Load() == 0 {
		t.Fatal("the dialer of the project was not used")
	}
	if _, err := m.SetPublic(st.ID, true); err == nil {
		t.Fatal("public without Tailscale")
	}
}

func TestPreviewStopsWithItsCommand(t *testing.T) {
	port := app(t)
	ts := &fakeTS{on: map[int]string{}}
	var changes atomic.Int32
	m := New(ts, authorized, func() { changes.Add(1) })
	defer m.Shutdown()
	var running atomic.Bool
	running.Store(true)
	st, _ := m.Start(Spec{Project: "p", Command: "x", AppPort: port}, "c1", nil, running.Load)
	base := local(m, st.ID)
	m.Check()
	if len(m.List()) != 1 {
		t.Fatal("stopped while running")
	}
	running.Store(false)
	m.Check()
	if len(m.List()) != 0 || ts.count() != 0 {
		t.Fatal("still there", ts.on)
	}
	if _, err := http.Get(base + "/"); err == nil {
		t.Fatal("the listener still answers")
	}
	if changes.Load() < 2 {
		t.Fatal("no change event")
	}
}

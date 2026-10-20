package server

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DrSmithFr/web-ide/lib/identity"
	"github.com/DrSmithFr/web-ide/lib/tunnel"
)

// fakePods records the power requests the handler passes on, and signs as a pod would.
type fakePods struct{ got []*identity.PowerRequest }

func (f *fakePods) List() any                  { return nil }
func (f *fakePods) Preferred() bool            { return false }
func (f *fakePods) SetPreferred(on bool) error { return nil }
func (f *fakePods) Gateway() string            { return "" }
func (f *fakePods) Cluster() string            { return "" }
func (f *fakePods) Own() string                { return "own" }
func (f *fakePods) CanUpdate() bool            { return false }
func (f *fakePods) CanPower() bool             { return false }
func (f *fakePods) Update(ctx context.Context, req *identity.UpdateRequest) (any, error) {
	return nil, nil
}
func (f *fakePods) Power(ctx context.Context, req *identity.PowerRequest) (any, error) {
	f.got = append(f.got, req)
	return map[string]any{"status": "running"}, nil
}
func (f *fakePods) SignPower(req *identity.PowerRequest) error {
	req.Sig = "signed-by-the-pod"
	return nil
}

// TestPowerLocal: a window on the WebSocket of the pod (LAN, 127.0.0.1) asks a power action
// without the passkey and the pod signs it; a window of the relay must sign it itself, and its
// own signature is carried untouched.
func TestPowerLocal(t *testing.T) {
	s, ts := newServer(t)
	pods := &fakePods{}
	s.Pods = pods
	a, err := dial(t, ts, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	if self := a.call("pods.self", nil)["result"].(map[string]any); self["local"] != true {
		t.Fatalf("a window on the WebSocket of the pod is local: %v", self)
	}
	a.call("pods.power", map[string]any{"pod": "bravo", "action": "restart"})
	if len(pods.got) != 1 || pods.got[0].Sig != "signed-by-the-pod" {
		t.Fatalf("the pod signs the request of a local window: %+v", pods.got)
	}

	p := newMsgPipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.ServeTunnel(ctx, p.server, make(chan tunnel.LabeledConn))
	if self := p.call(t, 1, "pods.self", nil)["result"].(map[string]any); self["local"] != false {
		t.Fatalf("a window of the relay is not local: %v", self)
	}
	if r := p.call(t, 2, "pods.power", map[string]any{"pod": "bravo", "action": "shutdown"}); r["error"] == nil {
		t.Fatalf("an unsigned request of a window of the relay is refused: %v", r)
	}
	if len(pods.got) != 1 {
		t.Fatalf("the refused request reached the pods: %+v", pods.got)
	}
	p.call(t, 3, "pods.power", map[string]any{"pod": "bravo", "action": "shutdown", "sig": "signed-by-the-browser"})
	if len(pods.got) != 2 || pods.got[1].Sig != "signed-by-the-browser" {
		t.Fatalf("the signature of the browser is carried: %+v", pods.got)
	}
}

// TestRelayLinks: a paired pod with a relay gives Claude Code its page on the relay origin, and
// the local address beside it; /open asked through the tunnel sends the frame to the project page
// under the path of the pod.
func TestRelayLinks(t *testing.T) {
	s, ts := newServer(t)
	s.Pods = &fakePods{}
	s.RelayStatus = func() RelayInfo { return RelayInfo{URL: "wss://relay.example"} }
	text := s.mcpInstructions()
	local := s.localURL()
	for _, want := range []string{"(https://relay.example/pod/own/open?path=", "([local](" + local + "/open?path="} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in:\n%s", want, text)
		}
	}
	if !strings.HasPrefix(local, "http://") || strings.Contains(local, "relay") {
		t.Fatalf("local address: %q", local)
	}

	a, _ := dial(t, ts, s.Token)
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	p := newMsgPipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	chans := make(chan tunnel.LabeledConn, 1)
	go s.ServeTunnel(ctx, p.server, chans)
	file := filepath.Join(dir, "a.go")
	head, _ := httpChan(t, chans, 1, map[string]any{"path": "/open?path=" + url.QueryEscape(file) + "&line=3", "base": "/pod/own", "method": "GET"})
	hs, _ := head["headers"].(map[string]any)
	want := "/pod/own/project/" + url.PathEscape(id) + "?open=" + url.QueryEscape(file+":3")
	if head["status"] != float64(302) || hs["Location"] != want {
		t.Fatalf("/open through the tunnel: %v (want %s)", head, want)
	}
}

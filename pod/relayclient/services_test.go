package relayclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"antennae.space/ide/lib/identity"
	"antennae.space/ide/lib/tunnel"
)

// TestServicesPodToPod: a service bravo shares is reached by alpha pod to pod through the relay,
// on conn:<n> channels: an answer streamed chunk by chunk, a WebSocket that echoes.
func TestServicesPodToPod(t *testing.T) {
	back := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ws" {
			c, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer c.CloseNow()
			typ, b, err := c.Read(r.Context())
			if err == nil {
				_ = c.Write(r.Context(), typ, append([]byte("echo "), b...))
			}
			return
		}
		for i := range 3 {
			fmt.Fprintf(w, "token %d\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(400 * time.Millisecond)
		}
	}))
	defer back.Close()
	u, _ := url.Parse(back.URL)
	port, _ := strconv.Atoi(u.Port())

	a, err := identity.FromPhrase(vectorsPhrase)
	if err != nil {
		t.Fatal(err)
	}
	podA := pairAccount(t, a, "alpha")
	podB := pairAccount(t, a, "bravo")
	env := startEnv(t, podA, nil)
	startSecond(t, env, podB, "bravo-pod")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	peer, err := dialTry(ctx, env.cli, "bravo-pod", podB.ID())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer peer.Close()
	readMsg(t, peer.RPC(), 10*time.Second) // hello
	res := rpcCallMsg(t, peer.RPC(), 1, "services.save", map[string]any{"name": "back", "port": port, "enabled": true})
	var svc struct{ ID string }
	_ = json.Unmarshal(res.Result, &svc)

	var n atomic.Int32
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		ch, err := peer.Open(ctx, "conn:"+strconv.Itoa(int(n.Add(1))))
		if err != nil {
			return nil, err
		}
		return tunnel.NetConn(ch), nil
	}}}
	base := "http://bravo/_peer/services/" + svc.ID + "/"
	resp, err := client.Post(base+"$attach", "", nil)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("attach: %v %v", resp, err)
	}
	resp.Body.Close()

	t0 := time.Now()
	resp, err = client.Get(base + "stream")
	if err != nil {
		t.Fatal(err)
	}
	rd := bufio.NewReader(resp.Body)
	var at []time.Duration
	for {
		if _, err := rd.ReadString('\n'); err != nil {
			break
		}
		at = append(at, time.Since(t0))
	}
	resp.Body.Close()
	t.Logf("tokens at %v", at)
	if len(at) != 3 || at[0] > 300*time.Millisecond {
		t.Fatalf("not streamed: %v", at)
	}

	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	c, _, err := websocket.Dial(wctx, "ws://bravo/_peer/services/"+svc.ID+"/ws", &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatalf("websocket: %v", err)
	}
	defer c.CloseNow()
	_ = c.Write(ctx, websocket.MessageText, []byte("hi"))
	if _, b, err := c.Read(ctx); err != nil || string(b) != "echo hi" {
		t.Fatalf("echo: %q %v", b, err)
	}
}

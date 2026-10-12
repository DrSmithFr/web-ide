package server

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/push"
)

type sentPush struct {
	sub, topic string
	body       []byte
}

// TestPushNotifications: a subscribed device gets the failure of a test command, encrypted for
// its keys, with the link that opens the project; not while it shows a window, nor once the
// kind is off; feedback by the assistant is sent, the user's own is not.
func TestPushNotifications(t *testing.T) {
	s, ts := newServer(t)
	subs, err := push.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sent := make(chan sentPush, 8)
	s.Push = subs
	s.PushSend = func(sub string, body []byte, ttl int, urgency, topic string) error {
		sent <- sentPush{sub, topic, body}
		return nil
	}
	s.RelayStatus = func() RelayInfo { return RelayInfo{PodID: "pod1"} }
	a, err := dial(t, ts, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	id := a.call("projects.create", map[string]any{"type": "local", "path": dir})["result"].(map[string]any)["id"].(string)
	a.call("project.open", map[string]any{"id": id})

	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)
	a.call("push.subscribe", map[string]any{"device": "dev1", "sub": "sub1", "p256dh": identity.B64u(ua.PublicKey().Bytes()), "auth": identity.B64u(auth)})
	st := a.call("push.status", map[string]any{"device": "dev1"})["result"].(map[string]any)
	if st["available"] != true || st["subscribed"] != true || st["choices"].(map[string]any)["tests"] != true {
		t.Fatalf("status: %+v", st)
	}

	read := func(why string) map[string]string {
		t.Helper()
		select {
		case p := <-sent:
			plain, err := push.Decrypt(ua, auth, p.body)
			if err != nil || p.sub != "sub1" {
				t.Fatalf("%s: %q %v", why, p.sub, err)
			}
			var m map[string]string
			_ = json.Unmarshal(plain, &m)
			return m
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: nothing sent", why)
		}
		return nil
	}
	none := func(why string) {
		t.Helper()
		select {
		case <-sent:
			t.Fatalf("%s: a notification was sent", why)
		case <-time.After(700 * time.Millisecond):
		}
	}

	saved := a.call("commands.save", map[string]any{"commands": []map[string]any{
		{"name": "Unit tests", "command": "exit 3", "flag": "test"},
		{"name": "Build", "command": "exit 2", "flag": "build"},
	}})["result"].([]any)
	tests, build := saved[0].(map[string]any)["id"].(string), saved[1].(map[string]any)["id"].(string)

	a.call("commands.run", map[string]any{"id": tests})
	m := read("tests failed")
	if m["title"] != "Tests failed" || !strings.Contains(m["body"], "Unit tests ended with code 3") ||
		m["url"] != "/#pod=pod1&open=%2Fproject%2F"+id || m["tag"] != "tests-"+tests {
		t.Fatalf("tests failed: %+v", m)
	}
	// Another flag is no test.
	a.call("commands.run", map[string]any{"id": build})
	none("a build command")

	// A visible window of the device: nothing.
	a.call("window.visible", map[string]any{"device": "dev1", "visible": true})
	a.call("commands.run", map[string]any{"id": tests})
	none("a visible window")
	a.call("window.visible", map[string]any{"device": "dev1", "visible": false})

	// The kind turned off.
	a.call("push.settings", map[string]any{"device": "dev1", "kind": "tests", "on": false})
	a.call("commands.run", map[string]any{"id": tests})
	none("the kind is off")

	// Feedback: by the user, nothing; by the assistant, a notification in French for a device
	// that subscribed in French.
	a.call("client.lang", map[string]any{"lang": "fr"})
	a.call("push.subscribe", map[string]any{"device": "dev1", "sub": "sub1", "p256dh": identity.B64u(ua.PublicKey().Bytes()), "auth": identity.B64u(auth)})
	a.call("kanban.create", map[string]any{"title": "Export"})
	a.call("kanban.feedback", map[string]any{"id": 1, "feedback": map[string]any{"op": "add", "kind": "info", "text": "mine"}})
	none("feedback by the user")
	p, _ := s.Projects.Get(id)
	s.notifyFeedback(kanbanLoc(p), 1, "the export fails")
	m = read("feedback")
	if m["title"] != "Retour sur #1 Export" || !strings.HasSuffix(m["body"], "the export fails") || !strings.Contains(m["url"], "ticket%3D1") {
		t.Fatalf("feedback: %+v", m)
	}

	// The test notification, and a subscription the relay dropped.
	a.call("push.test", map[string]any{"device": "dev1"})
	if m = read("test"); m["title"] != "Les notifications fonctionnent" {
		t.Fatalf("test: %+v", m)
	}
	subs.Gone("sub1")
	if r := a.callRaw("push.test", map[string]any{"device": "dev1"}); r["error"] == nil {
		t.Fatal("a device whose subscription is gone has no notification")
	}
}

func TestPushTopic(t *testing.T) {
	if got := pushTopic("tests-ab.c/d"); got != "tests-abcd" {
		t.Fatal(got)
	}
	if got := pushTopic(strings.Repeat("x", 40)); len(got) != 32 {
		t.Fatal(got)
	}
}

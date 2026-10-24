package multipod

import (
	"encoding/json"
	"testing"

	"antennae.space/ide/lib/identity"
	"antennae.space/ide/pod/settingsync"
)

// TestPublishRoundTrip: what a pod writes under its kind is what another pod reads back.
func TestPublishRoundTrip(t *testing.T) {
	hub, p, a := hubOf(t)
	var sent []settingsync.Msg
	hub.SetTransport(fakeTr{fn: func(m settingsync.Msg) { sent = append(sent, m) }})
	src := NewProjectsSource(p.ID(), "this pod", "1.0", func() []Project {
		return []Project{{ID: "a", Title: "quiet reader", Description: "d", Type: "local", Path: "/x"}}
	})
	// What the plugins advertise goes with it (the shared services).
	src.Advertised = func() map[string]any {
		return map[string]any{"services": []map[string]string{{"id": "s1", "name": "Ollama"}}}
	}
	if _, err := hub.AddSource(src); err != nil {
		t.Fatal(err)
	}
	hub.SetWrap(wrapFor(t, a, p, 0))
	if err := hub.Source(identity.ProjectsKind(p.ID())).Push(); err != nil {
		t.Fatal(err)
	}
	var rec *identity.BlobRecord
	for _, m := range sent {
		if m.Type == "blobPut" {
			rec = m.Blob
		}
	}
	if rec == nil {
		t.Fatalf("no blobPut sent: %+v", sent)
	}
	plain, err := identity.Unseal(identity.KindKey(identity.EpochKey(a.SyncSeed(), 0), rec.Kind),
		identity.BlobAAD(rec.Account, rec.Kind, rec.Epoch, rec.Seq), rec.Nonce, rec.Data)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("published: %s", plain)
	var st settingsync.State
	if err := json.Unmarshal(plain, &st); err != nil {
		t.Fatalf("the published blob is not a state: %v (%s)", err, plain)
	}
	content := st.Project(nil)
	t.Logf("content: %s", content)
	var l List
	if err := json.Unmarshal(content, &l); err != nil {
		t.Fatalf("the published list does not decode: %v (%s)", err, content)
	}
	if len(l.Projects) != 1 || l.Projects[0].Title != "quiet reader" {
		t.Fatalf("the list lost its projects: %s", plain)
	}
	if string(l.Advertised["services"]) != `[{"id":"s1","name":"Ollama"}]` {
		t.Fatalf("the list lost what the plugins advertise: %s", content)
	}
}

type fakeTr struct{ fn func(settingsync.Msg) }

func (f fakeTr) Send(m settingsync.Msg) error {
	f.fn(m)
	return nil
}

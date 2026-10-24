package server

import (
	"strings"
	"testing"

	"antennae.space/ide/lib/i18n"
)

// TestRelaySet: Settings > Remote access changes the relay the pod registers with — the handler
// passes the URL on and answers the new state, and a URL that is not a ws:// one is refused.
func TestRelaySet(t *testing.T) {
	s, ts := newServer(t)
	var got string
	s.RelaySet = func(url string) (RelayInfo, error) {
		if url != "" && !strings.HasPrefix(url, "ws://") && !strings.HasPrefix(url, "wss://") {
			return RelayInfo{}, i18n.Errorf("the relay must be a ws:// or wss:// URL")
		}
		got = url
		return RelayInfo{URL: url, DefaultURL: "wss://antennae.space", State: "connecting"}, nil
	}
	a, err := dial(t, ts, s.Token)
	if err != nil {
		t.Fatal(err)
	}
	r := a.call("relay.set", map[string]any{"url": "wss://antennae.space"})["result"].(map[string]any)
	if got != "wss://antennae.space" || r["url"] != "wss://antennae.space" || r["defaultUrl"] != "wss://antennae.space" {
		t.Fatalf("relay.set: %v (%q)", r, got)
	}
	if a.callRaw("relay.set", map[string]any{"url": "antennae.space"})["error"] == nil {
		t.Fatal("a URL that is not ws:// or wss:// is refused")
	}
	if got != "wss://antennae.space" {
		t.Fatalf("the refused URL reached the pod: %q", got)
	}
	if r := a.call("relay.set", map[string]any{"url": ""})["result"].(map[string]any); got != "" || r["state"] != "connecting" {
		t.Fatalf("turning remote access off: %v (%q)", r, got)
	}
}

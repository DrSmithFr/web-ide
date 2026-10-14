package main

import "testing"

// TestParseSize: the byte counts the relay flags take (-quota 10GB), with or without the B.
func TestParseSize(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want int64
	}{{"1MB", 1 << 20}, {"10GB", 10 << 30}, {"500k", 500 << 10}, {"1M", 1 << 20}, {"4096", 4096}, {"", 0}} {
		got, err := parseSize(tc.in)
		if err != nil || got != tc.want {
			t.Fatalf("-quota %s: got %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"lots", "10X", "b"} {
		if _, err := parseSize(bad); err == nil {
			t.Fatalf("-quota %s: accepted", bad)
		}
	}
}

// TestRelayLink: the remote link the pod prints and the Settings page shows is a page the user
// opens in a browser, not the websocket the pod dials.
func TestRelayLink(t *testing.T) {
	if got, want := relayLink("wss://antennae.space", "astra-1", "tok"), "https://antennae.space/#pod=astra-1&t=tok"; got != want {
		t.Fatalf("wss relay: got %q, want %q", got, want)
	}
	if got, want := relayLink("ws://127.0.0.1:4430", "astra-1", "tok"), "http://127.0.0.1:4430/#pod=astra-1&t=tok"; got != want {
		t.Fatalf("ws relay: got %q, want %q", got, want)
	}
}

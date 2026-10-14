package main

import "testing"

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

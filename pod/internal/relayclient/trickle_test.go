package relayclient

import (
	"encoding/json"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/pion/webrtc/v4"
)

// TestCandidateInit: a pod reads the candidate object a browser sends and the string a pod sends.
func TestCandidateInit(t *testing.T) {
	obj := json.RawMessage(`{"candidate":"candidate:1 1 udp 2130706431 192.168.1.2 5000 typ host","sdpMid":"0"}`)
	str, _ := json.Marshal(string(obj))
	for _, raw := range []json.RawMessage{obj, str} {
		init, ok := candidateInit(raw)
		if !ok || !strings.Contains(init.Candidate, "typ host") || init.SDPMid == nil || *init.SDPMid != "0" {
			t.Fatalf("%s: %+v %v", raw, init, ok)
		}
	}
	if _, ok := candidateInit(json.RawMessage(`"not json"`)); ok {
		t.Fatal("a string that is no candidate")
	}
}

// TestTrickleSlowTURN: a TURN server that never answers (a TLS port that accepts and stays silent)
// does not hold the description: it leaves after earlyWait with the host candidates, where the
// whole gathering would wait for the TURN client to give up (#75).
func TestTrickleSlowTURN(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			defer c.Close() // held open, never answered
		}
	}()
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{ICEServers: []webrtc.ICEServer{{
		URLs: []string{"turns:localhost:" + strings.Split(ln.Addr().String(), ":")[1] + "?transport=tcp"}, Username: "u", Credential: "p",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.CreateDataChannel("rpc", nil); err != nil {
		t.Fatal(err)
	}
	offer, _ := pc.CreateOffer(nil)
	late := make(chan json.RawMessage, 16)
	tr := trickle(pc, func(c json.RawMessage) { late <- c })
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	desc, _ := tr.describe(pc, gather)
	if d := time.Since(start); d > time.Second {
		t.Fatalf("the description waited %s for a silent TURN server", d)
	}
	if !strings.Contains(desc.SDP, "typ host") || !strings.Contains(desc.SDP, "a=fingerprint:") {
		t.Fatalf("the description carries the host candidates and the fingerprint:\n%s", desc.SDP)
	}
	select {
	case c := <-late:
		t.Fatalf("a candidate sent before the description left: %s", c)
	default:
	}
	tr.sent()
}

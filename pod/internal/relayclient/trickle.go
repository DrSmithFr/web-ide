package relayclient

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/pion/webrtc/v4"
)

// earlyWait is how long a pod waits for its candidates before it sends its offer or its answer
// (#75). Host and server-reflexive candidates come in a few milliseconds and ride in the SDP, so a
// peer that reads no trickled candidate (a page or a pod older than this) still has them; a TURN
// allocation that comes later is trickled. It replaces a wait for the whole gathering, capped at
// 8 s: one slow TURN server held every tunnel that long.
const earlyWait = 250 * time.Millisecond

// trickler sends the candidates a PeerConnection gathers after its description left: the ones
// gathered before are in that description already.
type trickler struct {
	mu    sync.Mutex
	taken bool              // the description was taken: the next candidates are late
	live  bool              // the description left: a late candidate is sent at once
	late  []json.RawMessage // late candidates waiting for the description to leave
	send  func(json.RawMessage)
	n     int // candidates gathered, in the description or trickled
}

// trickle starts sending the late candidates of pc with send. It must be called before
// SetLocalDescription, which starts the gathering.
func trickle(pc *webrtc.PeerConnection, send func(json.RawMessage)) *trickler {
	t := &trickler{send: send}
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		t.n++
		switch {
		case t.live:
			send(candidateMsg(c))
		case t.taken:
			t.late = append(t.late, candidateMsg(c))
		}
	})
	return t
}

// describe waits for the gathering to complete, at most earlyWait, and returns the local
// description with every candidate gathered so far. The next ones are late: a candidate whose
// callback held the lock before is in the description, one after waits for sent.
func (t *trickler) describe(pc *webrtc.PeerConnection, gather <-chan struct{}) (webrtc.SessionDescription, int) {
	select {
	case <-gather:
	case <-time.After(earlyWait):
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.taken = true
	return *pc.LocalDescription(), t.n
}

// sent is called once the description left: the late candidates follow it, never before it (a
// peer cannot add a candidate before the description it belongs to).
func (t *trickler) sent() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.live = true
	for _, c := range t.late {
		t.send(c)
	}
	t.late = nil
}

// candidateMsg is a candidate as the relay carries it to a browser: its JSON, as a string — the
// page parses it (JSON.parse(m.candidate)). A pod reads both forms (candidateInit).
func candidateMsg(c *webrtc.ICECandidate) json.RawMessage {
	b, _ := json.Marshal(c.ToJSON())
	s, _ := json.Marshal(string(b))
	return s
}

// candidateInit reads a candidate the relay carried: the JSON object a browser sends, or the
// string of it a pod sends.
func candidateInit(raw json.RawMessage) (webrtc.ICECandidateInit, bool) {
	var init webrtc.ICECandidateInit
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return init, false
		}
		raw = json.RawMessage(s)
	}
	return init, json.Unmarshal(raw, &init) == nil && init.Candidate != ""
}

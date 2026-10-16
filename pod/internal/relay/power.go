package relay

import (
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// carryPower routes a power request between two pods of one account, as an update request: to the
// online pod it names, with a hop id of the relay. A wake names a pod that sleeps, so it goes to
// every other online pod of the account instead: one of them may share its network. The relay
// never checks the request itself, the pod that acts on it does.
func (s *Server) carryPower(pc *podConn, m sigMsg) {
	if m.Power == nil || m.Device == "" || pc.certified == "" {
		return
	}
	type branch struct {
		to  *podConn
		hop string
	}
	var out []branch
	s.mu.Lock()
	if m.Power.Action == identity.PowerWake {
		fan := &wakeFan{}
		for _, p := range s.pods {
			if p != pc && p.key != "" && p.key != m.Device && p.certified == pc.certified {
				fan.pending++
				out = append(out, branch{to: p, hop: s.openHop(pc, m.Req, fan)})
			}
		}
	} else {
		for _, p := range s.pods {
			if p.key != "" && p.key == m.Device && p.certified == pc.certified {
				out = append(out, branch{to: p, hop: s.openHop(pc, m.Req, nil)})
				break
			}
		}
	}
	s.mu.Unlock()
	if len(out) == 0 {
		msg := "the pod is not online at the relay"
		if m.Power.Action == identity.PowerWake {
			msg = "no awake pod is on the network of that machine"
		}
		_ = pc.send(sigMsg{Type: "power.done", Req: m.Req, Status: "refused", Message: msg})
		return
	}
	for _, b := range out {
		_ = b.to.send(sigMsg{Type: "power", Device: m.Device, Req: b.hop, Power: m.Power})
	}
}

// powerDone passes an answer back to the pod that asked. A branch of a wake passes the first
// "sent" only; the last refusal of a wake no pod could send says so.
func (s *Server) powerDone(m sigMsg) {
	s.mu.Lock()
	h := s.updateHops[m.Req]
	if h != nil {
		delete(s.updateHops, m.Req)
	}
	pass := h != nil && time.Since(h.at) <= updateHopTTL
	if pass && h.fan != nil {
		f := h.fan
		f.pending--
		switch {
		case f.done:
			pass = false
		case m.Status == "sent":
			f.done = true
		case f.pending > 0:
			pass = false
		default:
			f.done = true
			m.Message = "no awake pod is on the network of that machine"
		}
	}
	s.mu.Unlock()
	if pass {
		_ = h.pc.send(sigMsg{Type: "power.done", Req: h.req, Status: m.Status, Message: m.Message, Command: m.Command})
	}
}

package lan

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// The pairing and admin link of the Devices panel when the pod has no relay: the same
// messages as the link of the relay (pod/internal/relay/identity.go), answered from what the
// pod knows — its certificate, the pods of the LAN, the browsers that registered here, the
// pods waiting to join on the LAN. The pod still signs nothing: the admin browser signs the
// certificates and the revocation lists, the pod checks them and passes them on.

// idMsg is one message of the link (the shape of the relay's).
type idMsg struct {
	Type     string                `json:"type"`
	Account  string                `json:"account,omitempty"`
	Key      string                `json:"key,omitempty"`
	Name     string                `json:"name,omitempty"`
	Kind     string                `json:"kind,omitempty"`
	Code     string                `json:"code,omitempty"`
	Cert     *identity.Cert        `json:"cert,omitempty"`
	List     *identity.Revocations `json:"list,omitempty"`
	Devices  []device              `json:"devices,omitempty"`
	Pairing  *JoinRequest          `json:"pairing,omitempty"`
	Pairings []JoinRequest         `json:"pairings,omitempty"`
	Lang     string                `json:"lang,omitempty"`
	Message  string                `json:"message,omitempty"`
}

type device struct {
	Account string         `json:"account"`
	Key     string         `json:"key"`
	Cert    *identity.Cert `json:"cert"`
}

// idLink is one open link of a panel.
type idLink struct {
	ws   *websocket.Conn
	mu   sync.Mutex
	lang string
}

func (l *idLink) send(m idMsg) {
	b, _ := json.Marshal(m)
	l.mu.Lock()
	defer l.mu.Unlock()
	_ = l.ws.Write(context.Background(), websocket.MessageText, b)
}

func (l *idLink) fail(err error) { l.send(idMsg{Type: "error", Message: i18n.Translate(l.lang, err)}) }

// ServeIdentity answers the Devices panel of the page on /_relay/identity.
func (m *Manager) ServeIdentity(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer ws.CloseNow()
	l := &idLink{ws: ws, lang: "en"}
	m.mu.Lock()
	m.idLinks[l] = true
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		delete(m.idLinks, l)
		m.mu.Unlock()
	}()
	ctx := r.Context()
	for {
		_, b, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var msg idMsg
		if json.Unmarshal(b, &msg) != nil {
			continue
		}
		account, _, list := m.cfg.Identity.State()
		switch msg.Type {
		case "register":
			if msg.Lang != "" {
				l.lang = msg.Lang
			}
			if account == "" {
				l.fail(i18n.New("this pod is not paired: web-ide-pod pair"))
				continue
			}
			if msg.Account != "" && msg.Account != account {
				l.fail(i18n.New("this pod belongs to another account"))
				continue
			}
			// The certificate of the browser, listed among the devices while the pod runs.
			if msg.Cert != nil && m.cfg.Identity.PeerError(msg.Cert) == nil {
				m.mu.Lock()
				m.browsers[msg.Cert.Key] = msg.Cert
				m.mu.Unlock()
			}
			l.send(idMsg{Type: "ready", Account: account, List: list})
		case "devices":
			l.send(idMsg{Type: "devices", Account: account, Devices: m.devices()})
		case "revocations":
			l.send(idMsg{Type: "revocations", Account: account, List: list})
		case "pairings":
			l.send(idMsg{Type: "pairings", Account: account, Pairings: m.Joins()})
		case "pairApprove":
			p, err := m.ApproveJoin(ctx, msg.Code, msg.Cert)
			if err != nil {
				l.fail(err)
				continue
			}
			l.send(idMsg{Type: "pair", Pairing: p})
		case "revoke":
			// Checked against the pinned account; the LAN links pass it on (refresh).
			newer, err := m.cfg.Identity.Apply(msg.List)
			if err != nil {
				l.fail(err)
				continue
			}
			stored := "kept"
			if newer {
				stored = "stored"
				m.pushIdentity(idMsg{Type: "revocations", Account: account, List: msg.List}, l)
			}
			l.send(idMsg{Type: "revoked", Account: account, List: msg.List, Message: stored})
		case "pair":
			l.fail(i18n.New("inviting a phone needs a relay: on the local network, run web-ide-pod pair join on the pod to add"))
		}
	}
}

// devices are the devices of the account the pod knows: itself, the pods of the LAN and the
// browsers that registered on its panel — the revoked ones left out (the panel shows the list
// it signed for those).
func (m *Manager) devices() []device {
	account, own, _ := m.cfg.Identity.State()
	seen := map[string]bool{}
	out := []device{}
	add := func(c *identity.Cert) {
		if c == nil || seen[c.Key] || m.cfg.Identity.PeerError(c) != nil {
			return
		}
		seen[c.Key] = true
		out = append(out, device{Account: account, Key: c.Key, Cert: c})
	}
	add(own)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.peers {
		add(p.cert)
	}
	for _, c := range m.browsers {
		add(c)
	}
	return out
}

// pushIdentity sends a message to the other open panels.
func (m *Manager) pushIdentity(msg idMsg, except *idLink) {
	m.mu.Lock()
	var links []*idLink
	for l := range m.idLinks {
		if l != except {
			links = append(links, l)
		}
	}
	m.mu.Unlock()
	for _, l := range links {
		l.send(msg)
	}
}

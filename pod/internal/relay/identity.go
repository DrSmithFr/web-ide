package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// idMsg is one identity message on the pairing and admin link. The relay never holds a
// private key: it can check a signature against an account id but never make one, so only a
// device that holds the account key can approve a pairing or push a revocation list.
type idMsg struct {
	Type     string                `json:"type"`
	Account  string                `json:"account,omitempty"`
	Key      string                `json:"key,omitempty"`
	Name     string                `json:"name,omitempty"`
	Kind     string                `json:"kind,omitempty"`
	Code     string                `json:"code,omitempty"`
	Cert     *identity.Cert        `json:"cert,omitempty"`
	List     *identity.Revocations `json:"list,omitempty"`
	Devices  []Device              `json:"devices,omitempty"`
	Device   string                `json:"device,omitempty"` // the device a wrap was made for
	Pairing  *Pairing              `json:"pairing,omitempty"`
	Pairings []Pairing             `json:"pairings,omitempty"`
	// Sync (milestone 4): the published X25519 key of a device, the wrap of the epoch key for
	// it, and the encrypted blobs. The relay cannot read a blob, it only checks the signature.
	SyncKey  *identity.SyncKeyRecord  `json:"syncKey,omitempty"`
	SyncKeys []identity.SyncKeyRecord `json:"syncKeys,omitempty"`
	Wrap     *identity.WrapRecord     `json:"wrap,omitempty"`
	Wraps    []identity.WrapRecord    `json:"wraps,omitempty"`
	Blob     *identity.BlobRecord     `json:"blob,omitempty"`
	Updated  int64                    `json:"updated,omitempty"` // time the stored blob or wrap was written
	Lang     string                   `json:"lang,omitempty"`    // language of the link, for the errors it is sent
	Message  string                   `json:"message,omitempty"`
}

// identityConn is one live pairing/admin link: a browser (or the pod CLI) that pairs a
// device, lists the devices of its account and signs revocation lists.
type identityConn struct {
	ws      *websocket.Conn
	mu      sync.Mutex
	account string
	lang    string
}

func (c *identityConn) send(m idMsg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(m)
	return c.ws.Write(context.Background(), websocket.MessageText, b)
}

// serveIdentity is the pairing rendezvous and the device list of an account:
//   - a joining device posts its public key with a name and a kind and gets a short code;
//   - an admin reads the code, compares the fingerprint of the key with the one the device
//     shows, signs the certificate and posts it back;
//   - an admin posts signed revocation lists, which the relay stores (only a higher seq) and
//     pushes to the online devices of the account.
//
// The relay cannot approve a pairing by itself: the certificate it is shown must verify
// against the account id, which only a device holding the account key can sign.
func (s *Server) serveIdentity(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	c := &identityConn{ws: ws}
	defer func() {
		s.mu.Lock()
		if s.links[c] {
			delete(s.links, c)
		}
		s.mu.Unlock()
		_ = ws.CloseNow()
	}()

	for {
		_, b, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		var m idMsg
		if err := json.Unmarshal(b, &m); err != nil {
			continue
		}
		switch m.Type {
		case "register":
			// The link announces the account it belongs to, with its own certificate. The
			// certificate must verify against the account it claims.
			if m.Cert != nil {
				if err := s.store.StoreDevice(m.Cert); err != nil {
					_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
					return
				}
			}
			s.mu.Lock()
			c.account = m.Account
			if m.Lang != "" {
				c.lang = m.Lang
			}
			s.links[c] = true
			s.mu.Unlock()
			list, err := s.store.Revocations(m.Account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				return
			}
			_ = c.send(idMsg{Type: "ready", Account: m.Account, List: list})
		case "devices":
			devs, err := s.store.Devices(c.account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "devices", Account: c.account, Devices: devs})
		case "revocations":
			list, err := s.store.Revocations(c.account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "revocations", Account: c.account, List: list})
		case "pairings":
			// The open requests an admin must approve, with the key the device posted: the
			// fingerprint the joining device showed must match the key read here.
			list, err := s.store.Pairings(c.account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "pairings", Account: c.account, Pairings: list})
		case "revoke":
			// Only a list signed by the pinned account, with a higher seq, is kept.
			kept, err := s.store.SaveRevocations(m.List)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "revoked", Account: c.account, List: m.List, Message: storedMessage(kept)})
			if kept {
				s.pushRevocations(c.account, m.List, c)
			}
		case "pair":
			p, err := s.store.CreatePairing(m.Key, m.Name, m.Kind)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			s.mu.Lock()
			s.codes[p.Code] = c
			s.mu.Unlock()
			_ = c.send(idMsg{Type: "pair", Pairing: p})
		case "pairAttach":
			// The device that opened the invite link posts its key to the code shown by the
			// inviting device. A code that already holds another key is refused: the
			// fingerprint the joining device shows must match the key the admin reads.
			p, err := s.store.AttachPairing(m.Code, m.Key, m.Name, m.Kind)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "pair", Pairing: p})
			// The admin that showed the code (or the device that printed the QR) sees the key
			// posted to it: the fingerprint it compares is the one of this key.
			s.mu.Lock()
			creator := s.codes[p.Code]
			s.joiners[p.Code] = c
			s.mu.Unlock()
			if creator != nil && creator != c {
				_ = creator.send(idMsg{Type: "pair", Pairing: p})
			}
		case "pairGet":
			p, err := s.store.ClaimPairing(m.Code, c.account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "pair", Pairing: p})
		case "syncKey", "syncKeys", "wrap", "wraps", "wrapGet", "blobGet", "blobPut":
			s.handleSync(c, m)
		case "pairApprove":
			p, err := s.store.ApprovePairing(m.Code, c.account, m.Cert)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			// The certificate the admin signed is stored: the relay lists the devices of an
			// account from the certificates it can verify.
			if err := s.store.StoreDevice(m.Cert); err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "pair", Pairing: p})
			// The device that asked for the pairing gets the signed certificate back: the one
			// that created the code (a pod printing its QR), or the one that opened the invite
			// link and posted its key to it.
			s.mu.Lock()
			joiner := s.joiners[p.Code]
			if joiner == nil {
				joiner = s.codes[p.Code]
			}
			s.mu.Unlock()
			if joiner != nil && joiner != c {
				_ = joiner.send(idMsg{Type: "pair", Pairing: p})
			}
		}
	}
}

// pushRevocations sends a stored list to the online links and pods of an account, except the
// link that posted it (it already got the confirmation). Every pod checks the list itself:
// the relay is only the courier.
func (s *Server) pushRevocations(account string, list *identity.Revocations, from *identityConn) {
	s.mu.Lock()
	var links []*identityConn
	for c := range s.links {
		if c.account == account && c != from {
			links = append(links, c)
		}
	}
	var pods []*podConn
	for _, p := range s.pods {
		if p.account == account {
			pods = append(pods, p)
		}
	}
	s.mu.Unlock()
	for _, c := range links {
		_ = c.send(idMsg{Type: "revocations", Account: account, List: list})
	}
	for _, p := range pods {
		_ = p.send(sigMsg{Type: "revocations", Account: account, List: list})
	}
}

func storedMessage(kept bool) string {
	if kept {
		return "stored"
	}
	return "older"
}

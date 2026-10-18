package relay

import (
	"context"
	"encoding/json"
	"errors"
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
	// Clusters (milestone 3): `challenge`/`proof` for the proof of possession, `pods` the pods of the
	// account the relay sees, `gateway` the entry point of each cluster.
	Prove   bool      `json:"prove,omitempty"`
	Nonce   string    `json:"nonce,omitempty"`
	Sig     string    `json:"sig,omitempty"`
	Cluster string    `json:"site,omitempty"` // "site" on the wire: the cluster (GLOSSARY.md)
	Gateway string    `json:"gateway,omitempty"`
	Pods    []PodInfo `json:"pods,omitempty"`
	// GitHub (milestone 5): the login an account is linked to, whether it sponsors the project,
	// and the URL the panel opens to link it. The relay never holds a user token.
	GitHubID  string `json:"githubId,omitempty"`
	Login     string `json:"login,omitempty"`
	LoginURL  string `json:"loginUrl,omitempty"`
	Supporter bool   `json:"supporter,omitempty"`
}

// identityConn is one live pairing/admin link: a browser (or the pod CLI) that pairs a
// device, lists the devices of its account and signs revocation lists.
type identityConn struct {
	ws      *websocket.Conn
	mu      sync.Mutex
	account string
	lang    string
	// The device id this link published a sync key under: an admin panel never publishes one, so
	// an empty device means a panel, which reads the wraps of the whole account.
	device string
	// The device key this link proved possession of (empty: it did not, so it gets no presence).
	key string
	// The tunnels the page signals on this link (#75), by session: read and written by the loop
	// of the link only.
	sessions map[string]*browserConn
}

// writeRaw writes one message the link carries for a tunnel of its page.
func (c *identityConn) writeRaw(b []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ws.Write(context.Background(), websocket.MessageText, b)
}

func (c *identityConn) send(m idMsg) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	b, _ := json.Marshal(m)
	return c.ws.Write(context.Background(), websocket.MessageText, b)
}

// identitySignal carries the signaling of a tunnel of the page on its identity link (#75): one
// WebSocket instead of two, and the offer can leave while the link proves itself. "signal" opens a
// session to a pod (the ICE servers come back), then offer, candidate and bye go as on
// /_relay/signal, and every answer of the pod carries the session. It needs no proof, as
// /_relay/signal does not. Reports whether the message was one.
func (s *Server) identitySignal(c *identityConn, b []byte) bool {
	var m sigMsg
	if json.Unmarshal(b, &m) != nil || m.Session == "" {
		return false
	}
	switch m.Type {
	case "signal":
		if !s.store.OnlinePod(m.Pod) {
			_ = c.writeRaw(mustJSON(sigMsg{Type: "error", Session: m.Session, Message: "offline"}))
			return true
		}
		bc := &browserConn{podID: m.Pod, session: m.Session, write: c.writeRaw}
		if prev := c.sessions[m.Session]; prev != nil {
			s.dropBrowser(prev)
		}
		c.sessions[m.Session] = bc
		s.openBrowser(bc)
	case "offer", "candidate", "bye":
		bc := c.sessions[m.Session]
		if bc == nil {
			return true
		}
		s.browserSignal(bc, m)
		if m.Type == "bye" {
			delete(c.sessions, m.Session)
		}
	default:
		return false
	}
	return true
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
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
	ws.SetReadLimit(wsReadLimit)
	c := &identityConn{ws: ws, sessions: map[string]*browserConn{}}
	defer func() {
		s.mu.Lock()
		if s.links[c] {
			delete(s.links, c)
		}
		s.mu.Unlock()
		for _, bc := range c.sessions {
			s.dropBrowser(bc)
		}
		_ = ws.CloseNow()
	}()
	signal := func(b []byte) bool { return s.identitySignal(c, b) }
	// A page that knows its pod names it in the URL of the link (#75): the session opens with the
	// link, and its ICE servers come without a round trip of their own.
	if q := r.URL.Query(); q.Get("pod") != "" && q.Get("session") != "" {
		signal(mustJSON(sigMsg{Type: "signal", Pod: q.Get("pod"), Session: q.Get("session")}))
	}

	for {
		_, b, err := ws.Read(context.Background())
		if err != nil {
			return
		}
		if signal(b) {
			continue
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
				// A link that announces the proof signs the nonce the relay sends with the key its
				// certificate names: only a device that holds the private half gets the presence of
				// the account (the pods list, the gateways).
				if m.Prove {
					nonce := relayNonce()
					if err := c.send(idMsg{Type: "challenge", Nonce: nonce}); err != nil {
						return
					}
					if err := s.proofOfPossession(ws, r.Host, m.Cert, nonce, signal); err != nil {
						_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
						return
					}
					c.key = m.Cert.Key
				}
			}
			s.mu.Lock()
			c.account = m.Account
			if m.Lang != "" {
				c.lang = m.Lang
			}
			s.links[c] = true
			s.mu.Unlock()
			s.store.TouchAccount(m.Account)
			list, err := s.store.Revocations(m.Account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				return
			}
			if c.key != "" && list.Has(c.key) {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, identity.ErrRevoked)})
				return
			}
			_ = c.send(idMsg{Type: "ready", Account: m.Account, List: list})
			if c.key != "" && !s.recomputeClusters(m.Account) {
				s.sendClusters(m.Account, c, nil)
			}
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
		case "github":
			// The panel asking what the account is linked to, and whether that login sponsors
			// the project (which lifts the TURN quota).
			if s.GitHub == nil {
				_ = c.send(idMsg{Type: "github", Account: c.account})
				continue
			}
			id, login, err := s.store.GitHubLink(c.account)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "github", Account: c.account, GitHubID: id, Login: login,
				Supporter: s.GitHub.isSupporter(c.account)})
		case "githubStart":
			if s.GitHub == nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, errors.New("this relay has no GitHub link"))})
				continue
			}
			target, err := s.githubStart(c)
			if err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			_ = c.send(idMsg{Type: "githubStart", Account: c.account, LoginURL: target})
		case "githubUnlink":
			if s.GitHub == nil {
				continue
			}
			if c.key == "" {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, errors.New("only a device of the account can unlink it"))})
				continue
			}
			if err := s.store.UnlinkGitHub(c.account); err != nil {
				_ = c.send(idMsg{Type: "error", Message: i18n.Translate(c.lang, err)})
				continue
			}
			s.tellQuota([]string{c.account})
			_ = c.send(idMsg{Type: "github", Account: c.account})
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

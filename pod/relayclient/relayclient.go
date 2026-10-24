// Package relayclient is the pod's side of the relay: it keeps one outbound WebSocket
// link to the relay and, for each browser that opens it, a WebRTC PeerConnection that
// carries the DataChannels. The pod token is proved inside the tunnel (bound to both
// DTLS fingerprints); it never reaches the relay.
//
// Channels by label: "rpc" carries the JSON protocol (unchanged); "term:<id>",
// "xfer:<n>" and "http:<n>" carry terminal I/O, big transfers and HTTP requests, one
// channel each, so a 2 MB file read does not stall keystrokes. The pod creates the
// "rpc" channel and answers offers; the browser creates the rest, and the pod routes
// them.
package relayclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/pion/webrtc/v4"

	"antennae.space/ide/lib/i18n"
	"antennae.space/ide/lib/identity"
	"antennae.space/ide/lib/tunnel"
	"antennae.space/ide/pod/server"
	"antennae.space/ide/pod/settingsync"
	"antennae.space/ide/pod/update"
)

// sigMsg is one signaling message between the relay and the pod (mirrored by the relay).
type sigMsg struct {
	Type       string                `json:"type"`
	Session    string                `json:"session,omitempty"`
	URLs       []string              `json:"urls,omitempty"`
	Username   string                `json:"username,omitempty"`
	Credential string                `json:"credential,omitempty"`
	TTL        int64                 `json:"ttl,omitempty"` // seconds the TURN credential is valid from now
	SDP        string                `json:"sdp,omitempty"`
	Candidate  json.RawMessage       `json:"candidate,omitempty"`
	ID         string                `json:"id,omitempty"`
	Secret     string                `json:"secret,omitempty"`
	Name       string                `json:"name,omitempty"`
	Account    string                `json:"account,omitempty"`
	Cert       *identity.Cert        `json:"cert,omitempty"`
	List       *identity.Revocations `json:"list,omitempty"` // pushed to the pods of an account
	// Sync (milestone 4): the pod publishes its X25519 key, reads the wrap of the epoch key made
	// for it, and exchanges encrypted blobs. The relay cannot read a blob, it only checks the
	// signature against the device that wrote it.
	SyncKey *identity.SyncKeyRecord `json:"syncKey,omitempty"`
	Wrap    *identity.WrapRecord    `json:"wrap,omitempty"`
	Blob    *identity.BlobRecord    `json:"blob,omitempty"`
	Device  string                  `json:"device,omitempty"`
	Kind    string                  `json:"kind,omitempty"`
	Updated int64                   `json:"updated,omitempty"`
	Message string                  `json:"message,omitempty"`
	// Clusters (milestone 3): register announces that this pod answers the proof of possession and
	// carries its version and the preferred switch, `site` the device keys of its LAN peers,
	// `challenge`/`proof` the nonce, `pods`/`gateway` what the relay sees of the account.
	Prove     bool      `json:"prove,omitempty"`
	Nonce     string    `json:"nonce,omitempty"`
	Sig       string    `json:"sig,omitempty"`
	Version   string    `json:"version,omitempty"`
	Build     string    `json:"build,omitempty"` // the fingerprint of the front end (server.Build)
	Protocol  int       `json:"protocol,omitempty"`
	Preferred bool      `json:"preferred,omitempty"`
	Renew     bool      `json:"renew,omitempty"` // register: this pod answers an offer on a live session (#86)
	Peers     []string  `json:"peers,omitempty"`
	Cluster   string    `json:"site,omitempty"` // "site" on the wire: the cluster (GLOSSARY.md)
	Gateway   string    `json:"gateway,omitempty"`
	Pods      []PodInfo `json:"pods,omitempty"`
	// Update this pod (#60): the relay only carries a signed update request between two pods of the
	// same account. req is the id of the hop the relay handed out, echoed on the answer.
	Update *identity.UpdateRequest `json:"update,omitempty"`
	// Power (relay.md, *Power*): a signed power request carried the same way; a wake goes to every
	// other online pod of the account, the relay passes back the first that sent the packet.
	Power   *identity.PowerRequest `json:"power,omitempty"`
	Req     string                 `json:"req,omitempty"`
	Status  string                 `json:"status,omitempty"`
	Command string                 `json:"command,omitempty"`
	// Quota (milestone 5): the TURN month of the account, told on ready and on the ice messages
	// (mirrors relay.QuotaState).
	Quota *QuotaState `json:"quota,omitempty"`
}

// QuotaState is the monthly TURN quota the relay applies to this pod's account (mirrors
// relay.QuotaState): the bytes moved this month, the limit (0: none), whether the account is
// freed from it, and its level (#87: "", backer, supporter, vip; its end, the level of the
// sponsorship alone and the VIP list), which an older relay does not send.
type QuotaState struct {
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Supporter bool   `json:"supporter,omitempty"`
	Level     string `json:"level,omitempty"`
	Until     int64  `json:"until,omitempty"`
	Sponsor   string `json:"sponsor,omitempty"`
	VIP       bool   `json:"vip,omitempty"`
	Team      string `json:"team,omitempty"` // the team that makes the account a VIP (#88)
	Over      bool   `json:"over,omitempty"`
}

// PodInfo is one pod of the account as the relay sees it (mirrors relay.PodInfo).
type PodInfo struct {
	Key      string `json:"key"`
	PodID    string `json:"podId"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	Cluster  string `json:"site"` // "site" on the wire: the cluster (GLOSSARY.md)
	Gateway  bool   `json:"gateway"`
	Since    int64  `json:"since"`
}

// build is the fingerprint of the front end the pod serves, told to the relay with its version.
func (c *Client) build() string {
	if c.cfg.Server == nil {
		return ""
	}
	return c.cfg.Server.Build()
}

// toSync turns the sync part of a signaling message into the message the sync loop reads.
func toSync(m sigMsg) settingsync.Msg {
	return settingsync.Msg{Type: m.Type, Account: m.Account, Device: m.Device, Kind: m.Kind,
		SyncKey: m.SyncKey, Wrap: m.Wrap, Blob: m.Blob, Updated: m.Updated, Message: m.Message}
}

// PeerRoute opens one channel of a pod of the account this pod can reach without the relay: a
// peer of its cluster, over the LAN link (pod/lan.OpenTunnel). window is the id of the
// browser's tunnel on this pod, via the certificate of the browser this pod authenticated, and
// fpBrowser / fpPod the DTLS fingerprints of that tunnel — the peer binds the browser's proof to
// them. Implemented by pod/multipod.Route.
type PeerRoute interface {
	Open(ctx context.Context, key, window, label string, via *identity.Cert, fpBrowser, fpPod string) (tunnel.MsgConn, error)
}

// RoutedLabel is the label of a channel of another pod, asked on this pod's tunnel:
// @<device key>/<label>. The gateway pipes it to that pod; it never serves it itself.
const RoutedLabel = "@"

// splitRouted splits a routed label into the device key of the target pod and its own label.
func splitRouted(label string) (key, rest string, ok bool) {
	if !strings.HasPrefix(label, RoutedLabel) {
		return "", "", false
	}
	key, rest, found := strings.Cut(strings.TrimPrefix(label, RoutedLabel), "/")
	if !found || key == "" || rest == "" {
		return "", "", false
	}
	return key, rest, true
}

// Config of a relayclient.
type Config struct {
	RelayURL string // ws:// or wss:// URL of the relay (the base, without /_relay/pod)
	// SignalURL is the base of a pod-to-pod signaling link: the relay by default. A test can put
	// a rewriting relay in front of it to check what a swapped offer breaks.
	SignalURL string
	PodID     string // the pod id registered with the relay
	Secret    string // the pod secret (register, or re-register)
	Name      string // a human name for the relay UI (optional)
	Server    *server.Server
	Token     string                // the pod token, proved inside the tunnel (token mode)
	Identity  *identity.PodIdentity // the paired pod: its device key and its pinned account
	// Sync is the settings sync loop of the pod: it sends its messages on this link and receives
	// the answers and the pushes of the relay (nil: the pod does not sync).
	Sync   *settingsync.Hub
	Limits *tunnel.RateLimiter
	// Version is the pod version, told to the relay (it lists the pods of the account).
	Version string
	// Protocol is the multi-pod protocol of this pod (multipod.Protocol), told to the relay: a pod
	// of another one is listed with its version and never elected gateway.
	Protocol int
	// Preferred marks this pod as the entry point of its cluster (Settings > Remote access).
	Preferred bool
	// OnPods is called with the pods of the account the relay sees, OnGateway with the gateway of
	// a cluster (multipod.Directory).
	OnPods    func([]PodInfo)
	OnGateway func(cluster, key string)
	// OnForeignWrap is called with a wrap the relay pushed for another member: this pod passes it
	// on over its LAN links, which is how a pod with no link of its own gets its key back.
	OnForeignWrap func(*identity.WrapRecord)
	// Route reaches a pod of this pod's cluster that has no link of its own: a browser tunneled to
	// this pod gets its channels through it (relay.md, *Multi-pod*).
	Route PeerRoute
	// OnUpdate runs an update request the relay carried to this pod: the pod checks the request
	// itself, the relay is a plain carrier.
	OnUpdate func(*identity.UpdateRequest) update.Answer
	// OnPower runs a power request the relay carried to this pod, checked by the pod itself.
	OnPower func(*identity.PowerRequest) update.Answer
	// ForceTurn only advertises relay (TURN) candidates (e2e).
	ForceTurn bool
	// OnState is called with the state of the link to the relay (connecting, connected,
	// disconnected); the page shows it in Settings > Remote access.
	OnState func(state string)
	// OnReady is called once the link is up and the sync loop published.
	OnReady func()
	// OnPushGone is called with a push subscription the relay dropped (the push service said
	// 410, or the subscription is not of this account).
	OnPushGone func(sub string)
}

// Client is one pod's relay client: one outbound link to the relay and one
// PeerConnection per browser session.
type Client struct {
	cfg      Config
	limits   *tunnel.RateLimiter
	mu       sync.Mutex
	sessions map[string]*session
	// link is the link to the relay while it is up (PushRevocations).
	link   *podLink
	iceMu  sync.Mutex
	iceFor map[string]webrtc.ICEServer
	// updates holds the update and power requests this pod sent through the relay, by the id the
	// relay gave the hop: the answer comes back on the same link.
	updates map[string]chan update.Answer
	// quota is the TURN month the relay applies to this pod's account (nil: the relay says
	// nothing, an old one or no quota at all).
	quota *QuotaState
}

// New builds a relayclient Client.
func New(cfg Config) *Client {
	if cfg.Limits == nil {
		cfg.Limits = tunnel.NewRateLimiter(5, 5*time.Minute)
	}
	return &Client{cfg: cfg, limits: cfg.Limits, sessions: map[string]*session{}, iceFor: map[string]webrtc.ICEServer{},
		updates: map[string]chan update.Answer{}}
}

// Run dials the relay, registers, and routes signaling until ctx ends. It reconnects
// with backoff if the link drops.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		c.tell("connecting")
		if err := c.runOnce(ctx); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c.tell("disconnected")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
				if backoff < 30*time.Second {
					backoff *= 2
				}
			}
		} else {
			backoff = time.Second
		}
	}
}

func (c *Client) tell(state string) {
	if c.cfg.OnState != nil {
		c.cfg.OnState(state)
	}
}

// runOnce is one live link to the relay: register, then route signaling.
func (c *Client) runOnce(ctx context.Context) error {
	u := strings.TrimSuffix(c.cfg.RelayURL, "/") + "/_relay/pod"
	conn, _, err := websocket.Dial(ctx, u, nil)
	if err != nil {
		return err
	}
	link := &podLink{c: conn}
	defer conn.Close(websocket.StatusNormalClosure, "")

	// Register: the relay stores the secret and marks the pod online. A paired pod also gives its
	// account and its certificate — the relay can check them but never make one — and answers the
	// nonce it is sent with the device key: the proof of possession. Without it the relay knows
	// nothing about this pod: no presence, no gateway role.
	var reg sigMsg
	if c.cfg.Identity != nil && c.cfg.Identity.Paired() {
		account, cert, _ := c.cfg.Identity.State()
		reg = sigMsg{Type: "register", ID: c.cfg.PodID, Secret: c.cfg.Secret, Name: c.cfg.Name,
			Account: account, Cert: cert, Prove: true, Version: c.cfg.Version, Build: c.build(), Protocol: c.cfg.Protocol, Preferred: c.cfg.Preferred, Renew: true}
	} else {
		reg = sigMsg{Type: "register", ID: c.cfg.PodID, Secret: c.cfg.Secret, Name: c.cfg.Name, Version: c.cfg.Version, Build: c.build(), Protocol: c.cfg.Protocol, Renew: true}
	}
	if err := link.send(reg); err != nil {
		return err
	}
	m, err := link.recv(ctx)
	if err != nil {
		return err
	}
	if m.Type == "challenge" {
		sig := identity.Sign(c.cfg.Identity.Key(), []byte(identity.RelayBinding(relayHost(c.cfg.RelayURL), m.Nonce)))
		if err := link.send(sigMsg{Type: "proof", Nonce: m.Nonce, Sig: sig}); err != nil {
			return err
		}
		m, err = link.recv(ctx)
		if err != nil {
			return err
		}
	}
	if m.Type != "ready" {
		return errors.New("relay refused the pod: " + m.Type)
	}
	// The revocation list the relay holds: the pod checks it itself and refuses the devices
	// on it, whatever the relay says.
	if err := c.applyRevocations(m.List); err != nil {
		return err
	}
	// The other way: a newer list the pod holds goes to the relay, so that the next list an
	// admin signs from the relay's copy keeps its revocations.
	if c.cfg.Identity != nil {
		if _, _, own := c.cfg.Identity.State(); own != nil && (m.List == nil || own.Seq > m.List.Seq) {
			_ = link.send(sigMsg{Type: "revocations", List: own})
		}
	}
	c.mu.Lock()
	c.link = link
	if m.Quota != nil {
		c.quota = m.Quota
	}
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.link = nil
		c.mu.Unlock()
	}()
	c.tell("connected")
	// The sync loop publishes the X25519 key of this member, reads the wrap an admin made for it
	// and the state the relay holds: a pod that comes back online catches up here.
	if c.cfg.Sync != nil {
		c.cfg.Sync.SetTransport(&syncLink{c, link})
		if err := c.cfg.Sync.Publish(); err != nil {
			log.Printf("relayclient: sync: %v", err)
			c.cfg.Sync.SetError(err)
		}
	}
	// The link is up and the sync loop published: what the pod reads from the relay (the cached
	// project lists of the other pods) can start here.
	if c.cfg.OnReady != nil {
		c.cfg.OnReady()
	}
	for {
		if ctx.Err() != nil {
			// A clean goodbye: the relay moves the gateway role at once instead of waiting for
			// the grace, and this pod starts fresh next time it comes up.
			_ = link.send(sigMsg{Type: "bye"})
			return ctx.Err()
		}
		m, err := link.recv(ctx)
		if err != nil {
			return err
		}
		switch m.Type {
		case "pods":
			if c.cfg.OnPods != nil {
				c.cfg.OnPods(m.Pods)
			}
		case "gateway":
			if c.cfg.OnGateway != nil {
				c.cfg.OnGateway(m.Cluster, m.Gateway)
			}
		case "revocations":
			if err := c.applyRevocations(m.List); err != nil {
				return err
			}
		case "quota":
			// The level of the account moved (a GitHub link, a sponsorship, the VIP list): the relay
			// tells it at once, the page reads it in Settings > Remote access.
			if m.Quota != nil {
				c.mu.Lock()
				c.quota = m.Quota
				c.mu.Unlock()
			}
		case "ice":
			c.setICE(m.Session, m)
			if m.Quota != nil {
				// The relay refused TURN credentials for this session (the month is over): the
				// page reads it in Settings > Remote access.
				c.mu.Lock()
				c.quota = m.Quota
				c.mu.Unlock()
			}
		case "offer":
			if s := c.session(m.Session); s != nil && s.renegotiating() {
				go s.renegotiate(m)
				continue
			}
			go c.handleOffer(c.newSession(m.Session, link), m, link)
		case "candidate":
			c.route(m.Session, func(s *session) { s.addBrowserCandidate(m.Candidate) })
		case "bye":
			c.route(m.Session, func(s *session) { s.close() })
		case "wrap", "blob":
			if m.Type == "wrap" && m.Wrap != nil && c.cfg.Identity != nil && m.Wrap.Device != c.cfg.Identity.ID() {
				// Not for this pod: it is for a pod of its cluster that has no link of its own.
				if c.cfg.OnForeignWrap != nil {
					c.cfg.OnForeignWrap(m.Wrap)
				}
				continue
			}
			if c.cfg.Sync == nil {
				continue
			}
			if err := c.cfg.Sync.Handle(toSync(m)); err != nil {
				log.Printf("relayclient: sync: %v", err)
				c.cfg.Sync.SetError(err)
			}
		case "push.gone":
			if c.cfg.OnPushGone != nil {
				c.cfg.OnPushGone(m.ID)
			}
		case "update":
			// The relay carried an update request to this pod. It checks it itself: the relay is a
			// plain carrier, it never decided who is a device of the account.
			if m.Update != nil && c.cfg.OnUpdate != nil {
				ans := c.cfg.OnUpdate(m.Update)
				_ = link.send(sigMsg{Type: "update.done", Req: m.Req, Status: ans.Status, Message: ans.Message, Command: ans.Command})
			}
		case "power":
			if m.Power != nil && c.cfg.OnPower != nil {
				ans := c.cfg.OnPower(m.Power)
				_ = link.send(sigMsg{Type: "power.done", Req: m.Req, Status: ans.Status, Message: ans.Message, Command: ans.Command})
			}
		case "update.done", "power.done":
			c.mu.Lock()
			ch := c.updates[m.Req]
			if ch != nil {
				delete(c.updates, m.Req)
			}
			c.mu.Unlock()
			if ch != nil {
				select {
				case ch <- update.Answer{Status: m.Status, Message: m.Message, Command: m.Command}:
				default:
				}
			}
		}
	}
}

// syncLink is the transport the sync loop sends its messages on: the same link, so a write and its
// answer stay in order.
type syncLink struct {
	c    *Client
	link *podLink
}

func (l *syncLink) Send(m settingsync.Msg) error {
	return l.link.send(sigMsg{Type: m.Type, Account: m.Account, Device: m.Device, Kind: m.Kind,
		SyncKey: m.SyncKey, Wrap: m.Wrap, Blob: m.Blob, Updated: m.Updated, Message: m.Message})
}

// Push gives the relay a Web Push message for a subscription of the account: the payload is
// already encrypted for the browser (package push), the relay signs and forwards it.
func (c *Client) Push(sub string, body []byte, ttl int, urgency, topic string) error {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return errors.New("relay: not connected")
	}
	return link.sendAny(map[string]any{"type": "push.send", "sub": sub, "body": body, "ttl": ttl, "urgency": urgency, "topic": topic})
}

// PushRevocations gives the relay a newer revocation list the pod received another way (a
// pod of the LAN, the pair command). The relay keeps it only if it is newer than its own.
func (c *Client) PushRevocations(list *identity.Revocations) {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link != nil && list != nil {
		_ = link.send(sigMsg{Type: "revocations", List: list})
	}
}

// UpdateWait is how long this pod waits for the answer of an update request it sent through the
// relay: a pod older than #60 knows no update message and never answers, and the page then shows
// the command to run by hand.
const UpdateWait = 10 * time.Second

// SendUpdate gives the relay a signed update request for another pod of the account and waits for
// its answer. The relay carries it to the online link of that device, for pods of the same account
// only; the target checks the signature itself.
func (c *Client) SendUpdate(ctx context.Context, device string, req *identity.UpdateRequest) (*update.Answer, error) {
	return c.carry(ctx, sigMsg{Type: "update", Device: device, Update: req},
		update.Answer{Status: update.StatusNotService, Command: update.Command(req.Version)})
}

// SendPower gives the relay a signed power request for another pod of the account. A wake is not
// for an online link: the relay asks every other pod of the account, and the answer is the first
// one that sent the packet.
func (c *Client) SendPower(ctx context.Context, device string, req *identity.PowerRequest) (*update.Answer, error) {
	silent := i18n.New("that pod does not answer: it may be older than this one")
	if req.Action == identity.PowerWake {
		silent = i18n.New("no awake pod is on the network of that machine")
	}
	return c.carry(ctx, sigMsg{Type: "power", Device: device, Power: req},
		update.Answer{Status: update.StatusRefused, Message: i18n.Translate("en", silent)})
}

// carry sends a request on the link with an id of this pod and waits for its answer, or for silent
// when no answer comes.
func (c *Client) carry(ctx context.Context, m sigMsg, silent update.Answer) (*update.Answer, error) {
	c.mu.Lock()
	link := c.link
	if link == nil {
		c.mu.Unlock()
		return nil, errors.New("relay: not connected")
	}
	id := randID()
	ch := make(chan update.Answer, 1)
	c.updates[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.updates, id)
		c.mu.Unlock()
	}()
	m.Req = id
	if err := link.send(m); err != nil {
		return nil, err
	}
	select {
	case a := <-ch:
		return &a, nil
	case <-time.After(UpdateWait):
		return &silent, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// randID is the id of one request of this link; the relay hands out its own for the hop it carries.
func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

// ReportCluster tells the relay the device keys of the pods this pod sees on its LAN: the relay
// builds the clusters from what every pod reports, and elects one gateway per cluster.
func (c *Client) ReportCluster(peers []string) {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return
	}
	_ = link.send(sigMsg{Type: "site", Peers: peers}) // "site" on the wire: the cluster (GLOSSARY.md)
}

// PutBlob stores a blob a peer of the cluster handed over: a pod with no link to the relay publishes
// its project list through this pod. The record is forwarded unchanged — the relay checks the
// signature of the device that wrote it, so the hop adds no trust.
func (c *Client) PutBlob(rec *identity.BlobRecord) error {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return errors.New("relay: not connected")
	}
	return link.send(sigMsg{Type: "blobPut", Account: rec.Account, Device: rec.Device, Kind: rec.Kind, Blob: rec})
}

// PutSyncKey hands the X25519 key a peer of the cluster published to the relay, so an admin can wrap
// the key of the epoch for a pod that has no link of its own. The record is forwarded unchanged.
func (c *Client) PutSyncKey(rec *identity.SyncKeyRecord) error {
	c.mu.Lock()
	link := c.link
	c.mu.Unlock()
	if link == nil {
		return errors.New("relay: not connected")
	}
	return link.send(sigMsg{Type: "syncKey", Account: rec.Account, Device: rec.Device, SyncKey: rec})
}

// SetPreferred marks this pod as the entry point of its cluster (Settings > Remote access), and tells
// the relay about the change.
func (c *Client) SetPreferred(on bool) {
	c.mu.Lock()
	c.cfg.Preferred = on
	link := c.link
	c.mu.Unlock()
	if link != nil {
		_ = link.send(sigMsg{Type: "preferred", Preferred: on})
	}
}

// relayHost is the origin the pod dials: the string a proof of possession is bound to, the same
// one the relay read in the Host header of the link.
func relayHost(u string) string {
	p, err := url.Parse(u)
	if err != nil || p.Host == "" {
		return u
	}
	return p.Host
}

// applyRevocations takes a revocation list from the relay. The pod checks the signature
// against the account it pinned — it never trusts the relay to say who is revoked — and
// keeps only a list with a higher seq.
func (c *Client) applyRevocations(list *identity.Revocations) error {
	if c.cfg.Identity == nil || list == nil {
		return nil
	}
	return c.cfg.Identity.ApplyRevocations(list)
}

// route runs fn on the session of an id, if it is alive.
func (c *Client) route(id string, fn func(*session)) {
	if s := c.session(id); s != nil {
		fn(s)
	}
}

func (c *Client) setICE(sessionID string, m sigMsg) {
	srv := iceServer(m)
	c.iceMu.Lock()
	c.iceFor[sessionID] = srv
	c.iceMu.Unlock()
}

// iceServer is the ICE server of an "ice" message of the relay.
func iceServer(m sigMsg) webrtc.ICEServer {
	urls := m.URLs
	if m.Quota != nil && m.Quota.Over {
		// The month is over: no credential came, so the TURN servers are dropped rather than
		// offered with none. The direct and STUN candidates stay, the tunnel simply has fewer
		// paths and may fail on a network that needs TURN.
		urls = nil
		for _, u := range m.URLs {
			if strings.HasPrefix(u, "stun:") {
				urls = append(urls, u)
			}
		}
	}
	return webrtc.ICEServer{URLs: urls, Username: m.Username, Credential: m.Credential}
}

// renewAfter is when to ask fresh TURN credentials for a tunnel whose credentials are valid ttl
// seconds (0: the relay said nothing, no renewal): at three quarters of it, so the ICE restart is
// done well before the relay refuses the old ones (#86).
func renewAfter(ttl int64) (time.Duration, bool) {
	if ttl <= 0 {
		return 0, false
	}
	return max(time.Duration(ttl)*time.Second*3/4, time.Second), true
}

// Quota is the TURN month the relay applies to this pod's account (nil: the relay said nothing).
func (c *Client) Quota() *QuotaState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.quota
}

func (c *Client) ICE(sessionID string) webrtc.ICEServer {
	c.iceMu.Lock()
	defer c.iceMu.Unlock()
	return c.iceFor[sessionID]
}

func (c *Client) session(id string) *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[id]
}

func (c *Client) addSession(id string, s *session) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[id] = s
}

func (c *Client) dropSession(id string) {
	c.mu.Lock()
	delete(c.sessions, id)
	c.mu.Unlock()
}

// handleOffer sets up one PeerConnection for a browser session, answers the offer, and
// serves the tunnel. It runs in its own goroutine (per session), so one handshake does
// not block the signaling loop; its session is made before (newSession), so a candidate the
// browser sends right after its offer finds it.
func (c *Client) handleOffer(s *session, m sigMsg, link *podLink) {
	// One line per step of a tunnel (#74): what a page stuck on its connection waits for can be
	// read in the log of the pod. The session id is the browser's random one, cut short.
	log.Printf("relayclient: tunnel %s: offer received", s.short())
	if err := s.setupOffer(m); err != nil {
		log.Printf("relayclient: offer: %v", err)
		link.send(sigMsg{Type: "error", Session: m.Session, Message: err.Error()})
		s.close()
		return
	}
	// serveRPC waits for the browser's "rpc" DataChannel (OnDataChannel hands it to
	// s.rpcCh, registered synchronously) and runs the handshake and the tunnel.
	go s.serveRPC()
	go s.serveRouted()
}

func (c *Client) newSession(id string, link *podLink) *session {
	s := &session{client: c, link: link, id: id, rpcCh: make(chan *dataChannelChunk, 1), chans: make(chan tunnel.LabeledConn, 64),
		routed: make(chan routedConn, 16), authed: make(chan struct{})}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	c.addSession(id, s)
	return s
}

// podLink is one pod's persistent WebSocket to the relay; writes are serialized.
type podLink struct {
	c  *websocket.Conn
	mu sync.Mutex
}

func (l *podLink) send(m sigMsg) error { return l.sendAny(m) }

func (l *podLink) sendAny(m any) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	b, _ := json.Marshal(m)
	return l.c.Write(context.Background(), websocket.MessageText, b)
}

func (l *podLink) recv(ctx context.Context) (sigMsg, error) {
	var m sigMsg
	_, b, err := l.c.Read(ctx)
	if err != nil {
		return m, err
	}
	_ = json.Unmarshal(b, &m)
	return m, nil
}

// session is one browser's PeerConnection and tunnel, in its own goroutine.
type session struct {
	client *Client
	link   *podLink
	id     string
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	pc     *webrtc.PeerConnection
	answer webrtc.SessionDescription
	offer  webrtc.SessionDescription
	// rpcCh carries the "rpc" DataChannel the browser created: OnDataChannel hands it
	// here (its OnMessage is registered synchronously, before any data can arrive), and
	// serveRPC — started once the answer is set, so its fingerprint is known — takes it.
	rpcCh chan *dataChannelChunk
	// chans carries every other DataChannel of this session (term:<id>, xfer:<n>,
	// http:<n>), framed, to ServeTunnel.
	chans chan tunnel.LabeledConn
	// routed carries the channels of another pod of the cluster (@<key>/<label>): this pod is the
	// gateway of this browser and pipes them to that pod.
	routed chan routedConn
	// authed is closed once the browser proved itself on the rpc channel: only then does this
	// pod relay its channels, with its certificate and the fingerprints of its tunnel.
	authed      chan struct{}
	browserCert *identity.Cert
	fpBrowser   string
	fpPod       string
	// rpcConn is the framed rpc channel, so the pod can answer on it (terminal output).
	rpcConn *tunnel.Framed
	closed  bool
	// The candidates of the browser that came before its offer was set: added then (#75).
	remote bool
	early  []webrtc.ICECandidateInit
}

// routedConn is one channel of another pod asked on this window.
type routedConn struct {
	key   string
	label string
	conn  *tunnel.Framed
}

func (s *session) setupOffer(m sigMsg) error {
	s.offer = webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP}
	policy := webrtc.ICETransportPolicyAll
	if s.client.cfg.ForceTurn {
		policy = webrtc.ICETransportPolicyRelay
	}
	cfg := webrtc.Configuration{ICEServers: []webrtc.ICEServer{s.client.ICE(s.id)}, ICETransportPolicy: policy}
	pc, err := webrtc.NewPeerConnection(cfg)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.pc = pc
	s.mu.Unlock()
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		// Register the incoming-message handler synchronously (before any data can
		// arrive). "rpc" goes to serveRPC; the rest are handed to the pod as labeled
		// channels. The handler runs in its own goroutine: this callback must return
		// before Pion starts the channel's read loop, and the pod reads the channel at its
		// own pace.
		chunk := newDataChannelChunk(dc)
		if dc.Label() == "rpc" {
			select {
			case s.rpcCh <- chunk:
			default:
			}
			return
		}
		conn := tunnel.NewFramed(chunk, 8*1024*1024)
		if key, rest, ok := splitRouted(dc.Label()); ok {
			// A channel of another pod of the cluster: this pod is the gateway of this browser.
			select {
			case s.routed <- routedConn{key: key, label: rest, conn: conn}:
			case <-s.ctx.Done():
			}
			return
		}
		s.chans <- tunnel.LabeledConn{Label: dc.Label(), Conn: conn}
	})
	pc.OnICEConnectionStateChange(func(state webrtc.ICEConnectionState) {
		switch state {
		case webrtc.ICEConnectionStateConnected:
			log.Printf("relayclient: tunnel %s: ICE connected, %s", s.short(), selectedPair(pc))
		case webrtc.ICEConnectionStateDisconnected, webrtc.ICEConnectionStateFailed:
			log.Printf("relayclient: tunnel %s: ICE %s", s.short(), state)
		}
		if state == webrtc.ICEConnectionStateFailed {
			s.close()
		}
	})
	return s.answerOffer(pc, s.offer)
}

// answerOffer sets an offer of the browser on pc and sends the answer. The trickle starts before
// the offer is set: an offer that restarts ICE starts the gathering at once (#86).
func (s *session) answerOffer(pc *webrtc.PeerConnection, offer webrtc.SessionDescription) error {
	// The answer leaves with the candidates gathered within earlyWait (host and reflexive ones,
	// a TURN allocation when it is quick); the later ones are trickled after it (#75). It used to
	// wait for the whole gathering, up to 8 s.
	tr := trickle(pc, func(c json.RawMessage) { _ = s.link.send(sigMsg{Type: "candidate", Session: s.id, Candidate: c}) })
	if err := pc.SetRemoteDescription(offer); err != nil {
		return err
	}
	s.mu.Lock()
	s.remote = true
	early := s.early
	s.early = nil
	s.mu.Unlock()
	for _, c := range early {
		_ = pc.AddICECandidate(c)
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return err
	}
	gather := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return err
	}
	asked := time.Now()
	// The answer SDP carries the pod's DTLS fingerprint (the handshake binds it).
	lc, n := tr.describe(pc, gather)
	s.mu.Lock()
	s.answer = lc
	s.mu.Unlock()
	log.Printf("relayclient: tunnel %s: answer sent after %s, %d candidates (the later ones trickled)", s.short(), time.Since(asked).Round(time.Millisecond), n)
	if err := s.link.send(sigMsg{Type: "answer", Session: s.id, SDP: lc.SDP}); err != nil {
		return err
	}
	tr.sent()
	return nil
}

// renegotiating reports whether an offer for this session is a new one on its live
// PeerConnection: the browser restarts ICE with fresh TURN credentials (#86). Its candidates wait
// for that offer, as the first ones did.
func (s *session) renegotiating() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pc == nil || s.closed {
		return false
	}
	s.remote = false
	return true
}

// renegotiate answers a new offer of the browser on the same PeerConnection: the ICE servers
// take the credentials the relay sent with it, and the offer restarts ICE. The DataChannels and
// the tunnel stay. A failure leaves the current path, which lives until its credentials end.
func (s *session) renegotiate(m sigMsg) {
	log.Printf("relayclient: tunnel %s: new offer (ICE restart with fresh TURN credentials)", s.short())
	s.mu.Lock()
	pc := s.pc
	s.mu.Unlock()
	cfg := pc.GetConfiguration()
	cfg.ICEServers = []webrtc.ICEServer{s.client.ICE(s.id)}
	err := pc.SetConfiguration(cfg)
	if err == nil {
		err = s.answerOffer(pc, webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: m.SDP})
	}
	if err != nil {
		log.Printf("relayclient: tunnel %s: new offer: %v", s.short(), err)
	}
}

// short is the session id cut for the log: enough to follow one tunnel, nothing to reuse.
func (s *session) short() string {
	if len(s.id) > 6 {
		return s.id[:6]
	}
	return s.id
}

// selectedPair names the path ICE chose: both candidate types (host, srflx, relay) and the
// protocol, so a tunnel that went through TURN is told from a direct one.
func selectedPair(pc *webrtc.PeerConnection) string {
	sctp := pc.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return "path unknown"
	}
	p, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || p == nil || p.Local == nil || p.Remote == nil {
		return "path unknown"
	}
	return fmt.Sprintf("pod %s/%s ↔ browser %s/%s", p.Local.Typ, p.Local.Protocol, p.Remote.Typ, p.Remote.Protocol)
}

// pairRoute is the route of a tunnel: through the TURN server when either side is a relay
// candidate, else direct; the addresses of the selected pair.
func pairRoute(pc *webrtc.PeerConnection) tunnel.Route {
	r := tunnel.Route{Name: tunnel.RouteRelayDirect}
	sctp := pc.SCTP()
	if sctp == nil || sctp.Transport() == nil || sctp.Transport().ICETransport() == nil {
		return r
	}
	p, err := sctp.Transport().ICETransport().GetSelectedCandidatePair()
	if err != nil || p == nil || p.Local == nil || p.Remote == nil {
		return r
	}
	if p.Local.Typ == webrtc.ICECandidateTypeRelay || p.Remote.Typ == webrtc.ICECandidateTypeRelay {
		r.Name = tunnel.RouteRelayTURN
	}
	r.Local = fmt.Sprintf("%s:%d", p.Local.Address, p.Local.Port)
	r.Peer = fmt.Sprintf("%s:%d", p.Remote.Address, p.Remote.Port)
	return r
}

func (s *session) addBrowserCandidate(raw json.RawMessage) {
	init, ok := candidateInit(raw)
	if !ok {
		return
	}
	s.mu.Lock()
	pc := s.pc
	if pc == nil || !s.remote {
		// Before the offer is set, pion refuses a candidate: keep it for then.
		s.early = append(s.early, init)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	_ = pc.AddICECandidate(init)
}

// serveRPC runs the handshake on the "rpc" channel (created by the browser), then hands
// the connection to the server. It waits for the DataChannel (OnDataChannel hands it to
// s.rpcCh, registered synchronously) and runs in its own goroutine.
func (s *session) serveRPC() {
	chunk := <-s.rpcCh
	if chunk == nil {
		return
	}
	offer, answer := s.offer, s.answer
	conn := tunnel.NewFramed(chunk, 8*1024*1024)
	hs := &tunnel.Handshake{
		Token:     s.client.cfg.Token,
		Identity:  s.client.cfg.Identity,
		FPBrowser: tunnel.Fingerprint(offer.SDP),
		FPPod:     tunnel.Fingerprint(answer.SDP),
		Limits:    s.client.limits,
		Build:     s.client.build(),
	}
	if err := hs.Run(conn); err != nil {
		log.Printf("relayclient: handshake: %v (fpB=%s fpP=%s)", err, hs.FPBrowser, hs.FPPod)
		// The refusal is the message the user reads ("device revoked", "the pod refuses the
		// token mode"). Closing the channel at once drops the frame still queued in SCTP, and
		// the browser only sees the close: give the refusal a moment to reach the peer.
		time.Sleep(300 * time.Millisecond)
		_ = conn.Close()
		s.close()
		return
	}
	s.browserCert, s.fpBrowser, s.fpPod = hs.PeerCert, hs.FPBrowser, hs.FPPod
	mode := "token"
	if hs.PeerCert != nil {
		mode = "device " + hs.PeerCert.Name
	}
	log.Printf("relayclient: tunnel %s: handshake done (%s)", s.short(), mode)
	close(s.authed)
	// Who asks goes with its requests: the routes of the pods only (/_svc/) read it.
	ctx := identity.WithPeer(s.ctx, hs.PeerCert)
	s.mu.Lock()
	pc := s.pc
	s.mu.Unlock()
	ctx = tunnel.WithRoute(ctx, func() tunnel.Route { return pairRoute(pc) })
	s.client.cfg.Server.ServeTunnel(ctx, conn, s.chans)
	// ServeTunnel returned: the browser closed the tunnel.
	s.close()
}

// serveRouted pipes the channels of another pod of the cluster to that pod, over the LAN link.
// The browser proved itself on this pod's rpc channel first: this pod vouches for it, and the
// end pod checks its certificate against the list it holds itself.
func (s *session) serveRouted() {
	select {
	case <-s.authed:
	case <-s.ctx.Done():
		return
	}
	for {
		select {
		case <-s.ctx.Done():
			return
		case rc := <-s.routed:
			go s.pipe(rc)
		}
	}
}

// pipe opens the channel of the target pod and copies both ways until one side ends.
func (s *session) pipe(rc routedConn) {
	route := s.client.cfg.Route
	if route == nil || s.browserCert == nil {
		_ = rc.conn.Close()
		return
	}
	peer, err := route.Open(s.ctx, rc.key, s.id, rc.label, s.browserCert, s.fpBrowser, s.fpPod)
	if err != nil {
		log.Printf("relayclient: route @%s/%s: %v", rc.key, rc.label, err)
		_ = rc.conn.Close()
		return
	}
	go func() {
		defer peer.Close()
		for {
			b, err := rc.conn.Read()
			if err != nil {
				return
			}
			if err := peer.Write(b); err != nil {
				return
			}
		}
	}()
	for {
		b, err := peer.Read()
		if err != nil {
			break
		}
		if err := rc.conn.Write(b); err != nil {
			break
		}
	}
	_ = rc.conn.Close()
}

// close ends the session (browser bye, ICE failed, or the tunnel closed).
func (s *session) close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	pc := s.pc
	s.mu.Unlock()
	close(s.rpcCh) // unblock serveRPC if it was still waiting for the "rpc" channel
	s.client.dropSession(s.id)
	s.link.send(sigMsg{Type: "bye", Session: s.id})
	if s.cancel != nil {
		s.cancel()
	}
	if pc != nil {
		_ = pc.Close()
	}
}

// dataChannelChunk adapts a WebRTC DataChannel to a tunnel.Chunk: one Send/Read is one
// transport message (a DataChannel message), bounded by the SCTP buffer. Pion delivers
// incoming messages on OnMessage; they are buffered here for ReadMsg.
type dataChannelChunk struct {
	dc   *webrtc.DataChannel
	in   chan []byte
	done chan struct{}
	once sync.Once
}

func newDataChannelChunk(dc *webrtc.DataChannel) *dataChannelChunk {
	c := &dataChannelChunk{dc: dc, in: make(chan []byte, 512), done: make(chan struct{})}
	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		select {
		case c.in <- msg.Data:
		default:
			// The reader is slow; drop rather than grow memory.
		}
	})
	dc.OnClose(func() { c.once.Do(func() { close(c.done) }) })
	return c
}

func (c *dataChannelChunk) ReadMsg() ([]byte, error) {
	select {
	case b := <-c.in:
		return b, nil
	case <-c.done:
		return nil, io.EOF
	}
}

func (c *dataChannelChunk) WriteMsg(b []byte) error { return c.dc.Send(b) }

func (c *dataChannelChunk) Buffered() int { return int(c.dc.BufferedAmount()) }

func (c *dataChannelChunk) Close() error {
	c.once.Do(func() { close(c.done) })
	return c.dc.Close()
}

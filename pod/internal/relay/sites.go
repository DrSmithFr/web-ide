package relay

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// Sites and gateways (milestone 3). Each pod reports the pods it sees on its LAN (the `site`
// message); the connected components of those edges, among the pods of an account that proved
// possession of the device key their certificate names, are the sites. The relay elects one
// gateway per site: the pod the user marked preferred, else the one online the longest. A
// gateway whose link drops without saying bye keeps the role for a moment, so a flaky link does
// not move the entry point of the site.
//
// The relay is not trusted for identity here either: it only says who is online and where.

// siteGrace is how long a gateway that dropped without saying bye keeps the role. A var so the
// tests do not wait ten seconds for a failover.
var siteGrace = 10 * time.Second

// proofTimeout is how long a link that announced a proof of possession has to answer the nonce.
const proofTimeout = 10 * time.Second

// The refusals of a proof of possession: the words the pod and the bootstrap page show.
var (
	errNoProof       = errors.New("the proof of possession is missing")
	errProofMismatch = errors.New("the proof of possession does not match")
)

// PodInfo is one pod of an account as the relay sees it: the device key that proved possession,
// the pod id its link registered with, and its place in its site.
type PodInfo struct {
	Key     string `json:"key"`
	PodID   string `json:"podId"`
	Name    string `json:"name"`
	Version string `json:"version"`
	// Protocol is the multi-pod protocol the pod told at register (0: a pod older than #60).
	Protocol int    `json:"protocol"`
	Site     string `json:"site"`
	Gateway  bool   `json:"gateway"`
	// Mismatch: this pod does not speak the protocol of its site ("outdated" or "newer"). The
	// relay never elects one as gateway, and a pod that routes says so (relay.md, *Multi-pod*).
	Mismatch string `json:"mismatch,omitempty"`
	// Preferred: the pod was marked as the entry point of its site by its user.
	Preferred bool  `json:"preferred,omitempty"`
	Since     int64 `json:"since"` // when its link came up (kept across a link that dropped uncleanly)
}

// graceEntry is a gateway kept in its role after its link dropped without saying bye.
type graceEntry struct {
	account string
	until   time.Time
	timer   *time.Timer
}

// relayNonce is the nonce of a proof of possession.
func relayNonce() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return ""
	}
	return hex.EncodeToString(b)
}

// proofOfPossession waits for the proof that answers the nonce the relay sent: the nonce signed
// with the key the certificate names. A copied certificate — a device.key read off another
// machine — cannot answer it, so the link is refused and the pod keeps no presence.
//
// The caller checked the certificate against the account; this checks that the link holds the
// private half. The link is read here, in order, right after the challenge was sent.
func (s *Server) proofOfPossession(ws *websocket.Conn, origin string, c *identity.Cert, nonce string) error {
	ctx, cancel := context.WithTimeout(context.Background(), proofTimeout)
	defer cancel()
	_, b, err := ws.Read(ctx)
	if err != nil {
		return err
	}
	var m sigMsg
	if json.Unmarshal(b, &m) != nil || m.Type != "proof" || m.Nonce != nonce {
		return errNoProof
	}
	if err := identity.VerifySig(c.Key, m.Sig, identity.RelayBinding(origin, nonce)); err != nil {
		return errProofMismatch
	}
	return nil
}

// podArrives records a pod link that proved possession: when it came up (the oldest keeps the
// gateway role), and ends the grace of a gateway that came back. It runs with s.mu held.
func (s *Server) podArrives(pc *podConn) {
	if g, ok := s.grace[pc.key]; ok {
		if g.timer != nil {
			g.timer.Stop()
		}
		delete(s.grace, pc.key)
	}
	if _, ok := s.since[pc.key]; !ok {
		s.since[pc.key] = time.Now()
	}
}

// podLeaves forgets a pod link. A link that ended without saying bye keeps its place: the gateway
// role stays its for siteGrace, and its "since" is not reset, so a flaky link does not make it
// the newest of its site. It runs with s.mu held.
func (s *Server) podLeaves(pc *podConn) {
	if pc.key == "" {
		return
	}
	if pc.clean {
		delete(s.since, pc.key)
		delete(s.siteOf, pc.key)
		return
	}
	if s.gateways[s.siteOf[pc.key]] == pc.key {
		key, account := pc.key, pc.account
		if g := s.grace[key]; g != nil && g.timer != nil {
			g.timer.Stop()
		}
		s.grace[key] = &graceEntry{account: account, until: time.Now().Add(siteGrace), timer: time.AfterFunc(siteGrace, func() { s.recomputeSites(account) })}
	}
}

// setSite records what a pod reports about the pods it sees on its LAN. Only a pod that proved
// possession can shape a site: an unproven link says nothing about the network.
func (s *Server) setSite(pc *podConn, peers []string) {
	if pc.key == "" {
		return
	}
	clean := make([]string, 0, len(peers))
	for _, p := range peers {
		if p != "" && p != pc.key {
			clean = append(clean, p)
		}
	}
	sort.Strings(clean)
	pc.peers = clean
}

// recomputeSites rebuilds the sites of an account from what its pods reported, elects one gateway
// per site and pushes the result to its links. It runs on each event that can move a site: a pod
// registering, leaving, or reporting its LAN peers, and when the grace of a gateway ends. It
// reports whether the sites moved, and so was pushed.
func (s *Server) recomputeSites(account string) bool {
	if account == "" {
		return false
	}
	s.mu.Lock()
	// A grace that ended no longer holds the role.
	for k, g := range s.grace {
		if g.account == account && !time.Now().Before(g.until) {
			delete(s.grace, k)
		}
	}
	online := map[string]*podConn{}
	for _, pc := range s.pods {
		if pc.account == account && pc.key != "" {
			online[pc.key] = pc
		}
	}
	// The nodes of the graph: the proven pods online now, plus the gateways kept in grace.
	parent := map[string]string{}
	for k := range online {
		parent[k] = k
	}
	for k, g := range s.grace {
		if g.account == account {
			parent[k] = k
		}
	}
	var find func(string) string
	find = func(k string) string {
		if parent[k] != k {
			parent[k] = find(parent[k])
		}
		return parent[k]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			if ra < rb {
				parent[rb] = ra
			} else {
				parent[ra] = rb
			}
		}
	}
	// An edge counts when either end reports it and both are devices of the account online here.
	for k, pc := range online {
		for _, p := range pc.peers {
			if _, ok := parent[p]; ok {
				union(k, p)
			}
		}
	}
	// A gateway in grace stays in the site it left: it is tied to the members it was with, so a
	// pod leaving does not rename the site nor move the role before the grace ends.
	for k, g := range s.grace {
		if g.account != account || s.siteOf[k] == "" {
			continue
		}
		for m := range parent {
			if m != k && s.siteOf[m] == s.siteOf[k] {
				union(k, m)
			}
		}
	}
	comps := map[string][]string{}
	for k := range parent {
		r := find(k)
		comps[r] = append(comps[r], k)
	}
	gateways := map[string]string{}
	siteOf := map[string]string{}
	infos := []PodInfo{}
	for _, keys := range comps {
		sort.Strings(keys)
		site := keys[0] // the site id is the smallest device key of the component
		for _, k := range keys {
			siteOf[k] = site
		}
		gateway := ""
		// A gateway must speak the protocol of its site: a browser entering through it routes every
		// pod of the site through it, and a pod of another protocol cannot carry those channels.
		proto := siteProtocol(keys, online)
		// A gateway in grace keeps the role even though its link is down.
		if prev := s.gateways[site]; prev != "" && s.grace[prev] != nil {
			for _, k := range keys {
				if k == prev {
					gateway = prev
					break
				}
			}
		}
		if gateway == "" {
			for _, k := range keys {
				pc := online[k]
				if pc == nil {
					continue // a gateway in grace is not online: it is kept above, never elected
				}
				if pc.protocol != proto {
					continue
				}
				if gateway == "" || betterGateway(s.since[pc.key], pc.preferred, s.since[gateway], online[gateway].preferred) {
					gateway = k
				}
			}
		}
		if gateway != "" {
			gateways[site] = gateway
		}
		for _, k := range keys {
			pc := online[k]
			if pc == nil {
				continue
			}
			infos = append(infos, PodInfo{Key: k, PodID: pc.id, Name: pc.name, Version: pc.version,
				Protocol: pc.protocol, Mismatch: protocolMismatch(pc.protocol, proto),
				Site: site, Gateway: gateways[site] == k, Preferred: pc.preferred, Since: s.since[k].Unix()})
		}
	}
	// Forget the sites of this account that no longer exist.
	for id := range s.siteAccount {
		if s.siteAccount[id] != account {
			continue
		}
		if _, ok := gateways[id]; !ok {
			delete(s.gateways, id)
			delete(s.siteAccount, id)
		}
	}
	for id, g := range gateways {
		s.gateways[id] = g
		s.siteAccount[id] = account
	}
	for k, site := range siteOf {
		s.siteOf[k] = site
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Key < infos[j].Key })
	s.sitePods[account] = infos
	s.mu.Unlock()

	if !s.sitesChanged(account, gateways, infos) {
		return false
	}
	s.pushSites(account, gateways, infos)
	return true
}

// siteProtocol is the protocol of a site: the one its online pods mostly run, the highest winning
// a tie. A pod of another one is listed with its version but never elected gateway.
func siteProtocol(keys []string, online map[string]*podConn) int {
	best, count := 0, -1
	for _, k := range keys {
		pc := online[k]
		if pc == nil {
			continue
		}
		c := 0
		for _, o := range keys {
			if po := online[o]; po != nil && po.protocol == pc.protocol {
				c++
			}
		}
		if c > count || (c == count && pc.protocol > best) {
			best, count = pc.protocol, c
		}
	}
	return best
}

// protocolMismatch names what a pod that does not run the protocol of its site is: outdated (an
// older one, so *Update this pod*) or newer (this pod is the one to update).
func protocolMismatch(p, site int) string {
	switch {
	case p < site:
		return "outdated"
	case p > site:
		return "newer"
	}
	return ""
}

// betterGateway elects the gateway of a site: the preferred pod, else the one online the longest,
// ties broken by device key so both sides reach the same answer.
func betterGateway(aSince time.Time, aPreferred bool, bSince time.Time, bPreferred bool) bool {
	if aPreferred != bPreferred {
		return aPreferred
	}
	if !aSince.Equal(bSince) {
		return aSince.Before(bSince)
	}
	return false
}

// sitesChanged reports whether the sites of an account moved since the last push, so a `site`
// message that changes nothing does not wake every window.
func (s *Server) sitesChanged(account string, gateways map[string]string, infos []PodInfo) bool {
	var b strings.Builder
	for _, id := range sortedKeys(gateways) {
		b.WriteString(id + "=" + gateways[id] + ";")
	}
	for _, p := range infos {
		b.WriteString(p.Key + "@" + p.Site + ";")
	}
	now := b.String()
	changed := s.siteText[account] != now
	s.siteText[account] = now
	return changed
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// sendSites gives a link that just proved possession the pods of its account and the gateway of
// each site, as the relay sees them now.
func (s *Server) sendSites(account string, c *identityConn, pc *podConn) {
	if account == "" {
		return
	}
	s.mu.Lock()
	infos := append([]PodInfo{}, s.sitePods[account]...)
	gateways := map[string]string{}
	for id, g := range s.gateways {
		if s.siteAccount[id] == account {
			gateways[id] = g
		}
	}
	s.mu.Unlock()
	for _, id := range sortedKeys(gateways) {
		if c != nil {
			_ = c.send(idMsg{Type: "gateway", Account: account, Site: id, Gateway: gateways[id]})
		}
		if pc != nil {
			_ = pc.send(sigMsg{Type: "gateway", Account: account, Site: id, Gateway: gateways[id]})
		}
	}
	if c != nil {
		_ = c.send(idMsg{Type: "pods", Account: account, Pods: infos})
	}
	if pc != nil {
		_ = pc.send(sigMsg{Type: "pods", Account: account, Pods: infos})
	}
}

// pushSites sends the sites of an account to its proven pods and its identity links: the pods list
// (who is online, in which site) and the gateway of each site. The relay only says where it sees
// the pods; every pod checks the certificates itself.
func (s *Server) pushSites(account string, gateways map[string]string, infos []PodInfo) {
	s.mu.Lock()
	var links []*identityConn
	for c := range s.links {
		if c.account == account && c.key != "" {
			links = append(links, c)
		}
	}
	var pods []*podConn
	for _, p := range s.pods {
		if p.account == account && p.key != "" {
			pods = append(pods, p)
		}
	}
	s.mu.Unlock()
	for _, c := range links {
		_ = c.send(idMsg{Type: "pods", Account: account, Pods: infos})
	}
	for _, p := range pods {
		_ = p.send(sigMsg{Type: "pods", Account: account, Pods: infos})
	}
	for _, id := range sortedKeys(gateways) {
		for _, c := range links {
			_ = c.send(idMsg{Type: "gateway", Account: account, Site: id, Gateway: gateways[id]})
		}
		for _, p := range pods {
			_ = p.send(sigMsg{Type: "gateway", Account: account, Site: id, Gateway: gateways[id]})
		}
	}
}

package lan

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/tunnel"
)

// A browser that reaches a pod of the cluster through its gateway (relay.md, *Multi-pod*) gets one
// channel of the pod per channel of its own tunnel: the gateway dials the LAN port of the peer
// once per channel, with its own certificate, and pipes the bytes. No multiplexer — the peer
// tells the channels of one window apart by the window id it is given.
//
// Trust stays hop by hop: the gateway authenticated the browser on its own tunnel and hands its
// certificate over (X-Webide-Via); the peer checks it against the revocation list *it* holds, so
// a browser revoked since the gateway last heard about it is refused. The fingerprints of the
// browser's DTLS link come along too: the proof the browser signs on the peer's rpc channel is
// bound to them, and only the gateway — a pod the browser itself verified — could know them.

// TunnelPath is the LAN endpoint of a channel of a bridged window.
const TunnelPath = "/_lan/tunnel"

// ViaHeader carries the certificate of the browser the gateway relays.
const ViaHeader = "X-Webide-Via"

// errNoWindow: a channel of a window whose rpc channel is not open on this pod.
var errNoWindow = errors.New("lan: no such window")

// tunnelWindow is one bridged window: the channels the gateway opens for it, handed to the
// server as they arrive.
type tunnelWindow struct {
	chans chan tunnel.LabeledConn
}

// serveTunnel answers one channel of a bridged window on the LAN port. The caller checked that
// the gateway is a pod of the account with a control link up.
func (m *Manager) serveTunnel(w http.ResponseWriter, r *http.Request, fp string) {
	q := r.URL.Query()
	window, label := q.Get("w"), q.Get("label")
	if window == "" || label == "" {
		http.Error(w, "w and label are needed", http.StatusBadRequest)
		return
	}
	// The browser the gateway vouches for: refused when *this* pod already knows it is revoked.
	var via *identity.Cert
	if s := r.Header.Get(ViaHeader); s != "" {
		var c identity.Cert
		if json.Unmarshal([]byte(s), &c) != nil {
			http.Error(w, "the browser certificate cannot be read", http.StatusBadRequest)
			return
		}
		if err := m.cfg.Identity.PeerError(&c); err != nil {
			// The refusal must reach the browser, not just the gateway: the frame is written on
			// the channel it asked for, then the channel ends.
			conn, err2 := websocket.Accept(w, r, nil)
			if err2 != nil {
				return
			}
			_ = tunnel.SendError(&wsConn{c: conn, ctx: r.Context()}, errorCode(err))
			time.Sleep(300 * time.Millisecond) // let the refusal reach the peer
			_ = conn.CloseNow()
			return
		}
		via = &c
	}
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	c.SetReadLimit(8 << 20)
	// The request context ends when this handler returns: a channel of a window must outlive it,
	// so it lives on its own context.
	conn := &wsConn{c: c, ctx: context.Background()}
	if label != "rpc" {
		// An extra channel of a window already open (term:<id>, xfer:<n>, http:<n>): the server
		// serves it as a channel of that window.
		m.mu.Lock()
		tw := m.tunnels[window+"|"+fp]
		m.mu.Unlock()
		if tw == nil {
			_ = c.Close(websocket.StatusBadGateway, "no such window")
			return
		}
		tw.chans <- tunnel.LabeledConn{Label: label, Conn: conn}
		return
	}
	// The rpc channel: the browser's proof, bound to the fingerprints of its own tunnel, read on
	// the peer's side. The handshake checks the certificate against the account this pod pinned
	// and the list this pod holds.
	hs := &tunnel.Handshake{
		Identity:  m.cfg.Identity,
		Token:     nonce() + nonce(), // the token mode is not for a bridged window
		FPBrowser: q.Get("fpb"),
		FPPod:     q.Get("fpp"),
		Limits:    m.limits,
		Build:     m.cfg.Build,
	}
	if err := hs.Run(conn); err != nil {
		log.Printf("lan: bridged window %s: handshake: %v", window, err)
		time.Sleep(300 * time.Millisecond) // let the refusal reach the browser
		_ = c.CloseNow()
		return
	}
	if hs.PeerCert == nil || (via != nil && hs.PeerCert.Key != via.Key) {
		// The gateway must vouch for the very browser that signed the proof.
		log.Printf("lan: bridged window %s: the gateway vouches for %v, the proof is %v", window, via, hs.PeerCert)
		_ = c.CloseNow()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tw := &tunnelWindow{chans: make(chan tunnel.LabeledConn, 16)}
	m.mu.Lock()
	m.tunnels[window+"|"+fp] = tw
	m.mu.Unlock()
	if m.cfg.ServeTunnel != nil {
		m.cfg.ServeTunnel(ctx, conn, tw.chans)
	}
	// The window ended: its gateway is told so by the closed channels, and the next label of
	// that id is refused.
	m.mu.Lock()
	if m.tunnels[window+"|"+fp] == tw {
		delete(m.tunnels, window+"|"+fp)
	}
	m.mu.Unlock()
}

// OpenTunnel dials one channel of a peer's bridged window: this pod is the gateway of a browser
// it authenticated, and the peer is a pod of its cluster. The channel is returned as a
// whole-message connection, to be piped to the browser's DataChannel.
//
// window is the id of the browser's tunnel on this pod; fpBrowser and fpPod the DTLS fingerprints
// of that tunnel, which the browser's proof on the peer is bound to; via the certificate of the
// browser this pod checked.
func (m *Manager) OpenTunnel(ctx context.Context, key, window, label string, via *identity.Cert, fpBrowser, fpPod string) (tunnel.MsgConn, error) {
	m.mu.Lock()
	var addr, fp string
	if p := m.peers[key]; p != nil && p.out != nil && !p.out.closed() {
		addr, fp = p.out.addr, p.out.serverFP
	}
	m.mu.Unlock()
	// A pod of another protocol is never routed to: the channels of a window would not agree on
	// the other side. The home page offers to update it instead.
	if name, _, gap := m.ProtocolGap(key); gap != 0 {
		if name == "" {
			name = key
		}
		return nil, i18n.Errorf("%s runs another multi-pod protocol: update it", name)
	}
	if addr == "" {
		return nil, errors.New("lan: this pod is not a peer of the local network")
	}
	u := url.URL{Scheme: "wss", Host: addr, Path: TunnelPath}
	q := u.Query()
	q.Set("w", window)
	q.Set("label", label)
	if label == "rpc" {
		q.Set("fpb", fpBrowser)
		q.Set("fpp", fpPod)
	}
	u.RawQuery = q.Encode()
	header := http.Header{}
	if via != nil {
		b, _ := json.Marshal(via)
		header.Set(ViaHeader, string(b))
	}
	dctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(dctx, u.String(), &websocket.DialOptions{HTTPHeader: header, HTTPClient: &http.Client{Transport: m.transport(fp)}})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(8 << 20)
	return &wsConn{c: c, ctx: ctx}, nil
}

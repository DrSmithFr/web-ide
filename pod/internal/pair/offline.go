package pair

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
	"github.com/DrSmithFr/web-ide/pod/internal/lan"
)

// The commands that need no admin device: the phrase typed is the authority. It signs, then
// it is forgotten — like restore, nothing of it is stored.

// JoinWithPhrase pairs the pod with the account of the phrase typed, under its own device
// key: unlike restore, the pod does not become an admin device (its key is not the one the
// phrase derives), so two pods joined this way stay two devices that can be revoked one by
// one. It needs no relay, for a pod that only meets the others on the LAN.
func JoinWithPhrase(env *Env) error {
	phrase, err := readLine(env.In)
	if err != nil {
		return err
	}
	a, err := identity.FromPhrase(phrase)
	if err != nil {
		return err
	}
	p, err := identity.Open(env.Dir)
	if err != nil {
		return err
	}
	if p.ID() == identity.KeyID(identity.KeyFromSeed(identity.DeviceSeed(a.Seed))) {
		return errors.New("pair: this pod holds the admin key of the phrase, use restore")
	}
	return save(env, a)
}

// Revoke revokes a device by its key, with the account key derived from the phrase typed.
// The list follows the newest one known — the pod's, or the relay's when there is one — so
// no revocation is lost; the pod keeps it at once, passes it to the pods of the LAN, and
// gives it to the relay.
func Revoke(env *Env, key string) error {
	if _, err := identity.B64d(key); err != nil || len(key) != 43 {
		return fmt.Errorf("pair: %q is not a device key (pair status prints it)", key)
	}
	p, err := identity.Open(env.Dir)
	if err != nil {
		return err
	}
	if !p.Paired() {
		return errors.New("pair: this pod is not paired")
	}
	phrase, err := readLine(env.In)
	if err != nil {
		return err
	}
	a, err := identity.FromPhrase(phrase)
	if err != nil {
		return err
	}
	if a.ID != p.Account {
		return errors.New("pair: this phrase is not the one of the account of the pod")
	}

	base := p.Revocations
	var ws *websocket.Conn
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if env.RelayURL != "" {
		ws, base, err = relayList(ctx, env.RelayURL, p.Account, base)
		if err != nil {
			fmt.Fprintf(env.Out, "relay not reachable (%v): the list stays on this pod and the LAN, the relay gets it when the pod connects\n", err)
		} else {
			defer ws.Close(websocket.StatusNormalClosure, "")
		}
	}
	list := &identity.Revocations{Created: time.Now().Unix()}
	if base != nil {
		list.Seq, list.Epoch, list.Revoked = base.Seq, base.Epoch, append([]string{}, base.Revoked...)
	}
	if slices.Contains(list.Revoked, key) {
		fmt.Fprintf(env.Out, "already revoked: %s (%s)\n", key, identity.Fingerprint(key))
		return nil
	}
	list.Seq++
	list.Epoch++
	list.Revoked = append(list.Revoked, key)
	if err := identity.SignRevocations(a.Priv, list); err != nil {
		return err
	}
	if err := p.ApplyRevocations(list); err != nil {
		return err
	}
	if ws != nil {
		if err := send(ctx, ws, msg{Type: "revoke", List: list}); err != nil {
			return err
		}
		if m, err := recv(ctx, ws); err == nil && m.Type == "error" {
			return errors.New("relay: " + m.Message)
		}
	}
	fmt.Fprintf(env.Out, "revoked     : %s (%s)\n", key, identity.Fingerprint(key))
	fmt.Fprintf(env.Out, "revocations : seq %d, epoch %d, %d device(s)\n", list.Seq, list.Epoch, len(list.Revoked))
	return nil
}

// relayList reads the revocation list the relay holds, and returns the newer of it and the
// pod's, with the link kept open to give the new list back.
func relayList(ctx context.Context, relayURL, account string, own *identity.Revocations) (*websocket.Conn, *identity.Revocations, error) {
	ws, _, err := websocket.Dial(ctx, strings.TrimSuffix(relayURL, "/")+"/_relay/identity", nil)
	if err != nil {
		return nil, own, err
	}
	if err := send(ctx, ws, msg{Type: "register", Account: account}); err != nil {
		ws.CloseNow()
		return nil, own, err
	}
	m, err := recv(ctx, ws)
	if err != nil || m.Type != "ready" {
		ws.CloseNow()
		if err == nil {
			err = errors.New(m.Type + " " + m.Message)
		}
		return nil, own, err
	}
	if m.List != nil && identity.VerifyRevocations(m.List, account) == nil && (own == nil || m.List.Seq > own.Seq) {
		return ws, m.List, nil
	}
	return ws, own, nil
}

// JoinLAN waits on the local network for an admin, with no relay: the pod serves its request
// on its LAN port and announces it by mDNS; a paired pod of the LAN shows it in the Devices
// panel of its page, where the admin compares the fingerprint and approves.
func JoinLAN(env *Env) error {
	if env.LANAddr == "off" {
		return errors.New("pair: join needs a relay (-relay) or the LAN link; or join --phrase")
	}
	p, err := identity.Open(env.Dir)
	if err != nil {
		return err
	}
	var resolver lan.Resolver
	if env.MDNS {
		resolver = lan.MDNS{}
	}
	cert, err := lan.WaitJoin(context.Background(), lan.JoinConfig{
		Dir: env.Dir, Addr: env.LANAddr, Identity: p, Name: env.Name, Resolver: resolver, Timeout: env.timeout(),
		OnListen: func(code string, port int) {
			fmt.Fprintf(env.Out, "Pairing code: %s\n", code)
			fmt.Fprintf(env.Out, "Fingerprint of this device: %s\n", identity.Fingerprint(p.ID()))
			fmt.Fprintf(env.Out, "Waiting on the local network: open Settings > Remote access > Devices on a paired pod (on localhost).\n")
			fmt.Fprintf(env.Out, "If it does not show up, type one of these addresses in its Local network settings: %s\n", strings.Join(lan.LocalAddrs(port), ", "))
		},
	})
	if err != nil {
		return err
	}
	if err := p.Pin(cert.Account); err != nil {
		return err
	}
	if err := p.SetCert(cert); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Paired with account %s (%s)\n", cert.Account, identity.Fingerprint(cert.Account))
	return nil
}

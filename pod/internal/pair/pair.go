// Package pair is the "web-ide-pod pair" subcommand: the pod makes its account from a
// 24-word phrase, or joins an account an admin already made. The phrase is shown once and
// never stored — on disk or in the relay; the pod keeps only its own device key, so it can
// check the certificates it is shown but cannot sign one.
package pair

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/skip2/go-qrcode"

	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// Env is the environment of the command: the data folder, the relay, the terminal.
type Env struct {
	Dir      string // the pod data folder (device.key and identity.json live here)
	RelayURL string // ws:// or wss:// URL of the relay ("" → offline: join is not possible)
	Name     string // the device name written in the certificate
	In       io.Reader
	Out      io.Writer
	// Positions returns the three words asked back after the phrase is shown. Random by
	// default; a test sets fixed ones.
	Positions func() []int
	// Entropy is the entropy of the new phrase (a test sets a fixed one).
	Entropy []byte
	// Timeout bounds the wait for an approval (default 5 min).
	Timeout time.Duration
	// LANAddr is the LAN listen address of the pod: join waits there when there is no relay.
	LANAddr string
	// MDNS announces the request of join on the LAN (false: its address is typed by hand).
	MDNS bool
}

// Run dispatches one call of the subcommand: pair create | restore | join | status.
func Run(env *Env, args []string) error {
	if len(args) == 0 {
		return errors.New("pair: create, restore, join [--phrase], revoke <key> or status")
	}
	switch args[0] {
	case "create":
		return Create(env)
	case "restore":
		return Restore(env)
	case "join":
		if len(args) > 1 && args[1] == "--phrase" {
			return JoinWithPhrase(env)
		}
		code := ""
		if len(args) > 1 {
			code = args[1]
		}
		return Join(env, code)
	case "revoke":
		if len(args) < 2 {
			return errors.New("pair: revoke <device key>")
		}
		return Revoke(env, args[1])
	case "status":
		return Status(env)
	default:
		return fmt.Errorf("pair: unknown command %q", args[0])
	}
}

// Create makes a new account: 24 words, shown once, three of them asked back to be sure the
// user wrote them down. Nothing is stored before the check passes, and the phrase itself is
// never stored: only the account id (its public key) and the certificate of this device.
func Create(env *Env) error {
	e := env.Entropy
	if len(e) == 0 {
		e = make([]byte, 32)
		if _, err := rand.Read(e); err != nil {
			return err
		}
	}
	phrase, err := identity.Generate(e)
	if err != nil {
		return err
	}
	fmt.Fprintln(env.Out, "Account phrase — shown once, never stored. Write it down:")
	printPhrase(env.Out, phrase)
	if err := confirm(env, phrase); err != nil {
		return err
	}
	a, err := identity.FromPhrase(phrase)
	if err != nil {
		return err
	}
	return save(env, a)
}

// Restore rebuilds the identity of a device from a phrase typed by the user: the same
// account, and the device key derived from the phrase — the key of an admin device, the one
// that can sign certificates.
func Restore(env *Env) error {
	phrase, err := readLine(env.In)
	if err != nil {
		return err
	}
	a, err := identity.FromPhrase(phrase)
	if err != nil {
		return err
	}
	if err := writeDeviceKey(env.Dir, identity.DeviceSeed(a.Seed)); err != nil {
		return err
	}
	return save(env, a)
}

// Join posts the public key of this member to the relay, prints the code an admin reads, the
// QR of the invite link and the 4-word fingerprint of the key, then waits for the approval.
// With a code, the key is posted to the code an admin already showed: the fingerprint the admin
// reads is the one of this key, and the code stays the one the panel displays.
// The certificate comes back signed by the account key: the pod checks it against the account
// it names, as it does for every peer.
func Join(env *Env, code string) error {
	if env.RelayURL == "" {
		return JoinLAN(env)
	}
	p, err := identity.Open(env.Dir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), env.timeout())
	defer cancel()

	ws, _, err := websocket.Dial(ctx, strings.TrimSuffix(env.RelayURL, "/")+"/_relay/identity", nil)
	if err != nil {
		return err
	}
	defer ws.Close(websocket.StatusNormalClosure, "")

	// The account is empty for a device that has none; a device that already has one
	// re-pairs under it (after a revocation, the admin signs a new certificate).
	if err := send(ctx, ws, msg{Type: "register", Account: p.Account, Name: env.Name}); err != nil {
		return err
	}
	m, err := recv(ctx, ws)
	if err != nil {
		return err
	}
	if m.Type == "error" {
		return errors.New("relay: " + m.Message)
	}
	t := msg{Type: "pair", Key: p.ID(), Name: env.Name, Kind: identity.KindPod}
	if code != "" {
		t.Type, t.Code = "pairAttach", code
	}
	if err := send(ctx, ws, t); err != nil {
		return err
	}
	if m, err = recv(ctx, ws); err != nil {
		return err
	}
	if m.Type != "pair" || m.Pairing == nil || m.Pairing.Code == "" {
		return errors.New("relay: no code (" + m.Type + ")")
	}
	code = m.Pairing.Code

	fmt.Fprintf(env.Out, "Pairing code: %s\n", code)
	fmt.Fprintf(env.Out, "Invite link:  %s\n", inviteLink(env.RelayURL, code))
	if qr, err := qrcode.New(inviteLink(env.RelayURL, code), qrcode.Medium); err == nil {
		fmt.Fprintln(env.Out, qr.ToSmallString(false))
	}
	fmt.Fprintf(env.Out, "Fingerprint of this device: %s\n", identity.Fingerprint(p.ID()))
	fmt.Fprintln(env.Out, "Waiting for an admin to approve (the fingerprint must match on both sides)…")

	m, err = recv(ctx, ws)
	if err != nil {
		return err
	}
	if m.Type == "error" {
		return errors.New("relay: " + m.Message)
	}
	if m.Type != "pair" || m.Pairing == nil || m.Pairing.Cert == nil {
		return errors.New("pair: the approval did not come")
	}
	c := m.Pairing.Cert
	// The relay cannot sign: a certificate it sends must verify against the account it
	// claims, which only a device holding the account key can do.
	if err := identity.VerifyCert(c, c.Account, nil); err != nil {
		return err
	}
	p.Account = c.Account
	if err := p.SetCert(c); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "Paired with account %s (%s)\n", c.Account, identity.Fingerprint(c.Account))
	return nil
}

// Status prints what the pod knows about itself. It works without a relay.
func Status(env *Env) error {
	p, err := identity.Open(env.Dir)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "device      : %s (%s)\n", p.ID(), identity.Fingerprint(p.ID()))
	if !p.Paired() {
		fmt.Fprintln(env.Out, "not paired  : pair create, restore or join")
		return nil
	}
	fmt.Fprintf(env.Out, "account     : %s (%s)\n", p.Account, identity.Fingerprint(p.Account))
	if p.Cert != nil {
		fmt.Fprintf(env.Out, "certificate : %s, %s, %s\n", p.Cert.Name, p.Cert.Kind, time.Unix(p.Cert.Created, 0).Format("2006-01-02"))
	}
	if p.Revocations != nil {
		fmt.Fprintf(env.Out, "revocations : seq %d, epoch %d, %d device(s)\n", p.Revocations.Seq, p.Revocations.Epoch, len(p.Revocations.Revoked))
	}
	return nil
}

// positions returns the three words asked back: random, or the ones the test set.
func (env *Env) positions() ([]int, error) {
	if env.Positions != nil {
		return env.Positions(), nil
	}
	var pos []int
	for len(pos) < 3 {
		n, err := rand.Int(rand.Reader, big.NewInt(24))
		if err != nil {
			return nil, err
		}
		i := int(n.Int64()) + 1
		seen := false
		for _, p := range pos {
			if p == i {
				seen = true
			}
		}
		if !seen {
			pos = append(pos, i)
		}
	}
	return pos, nil
}

// printPhrase shows the words numbered, eight rows of three, so the user can write them down
// and find the ones the check asks for.
func printPhrase(w io.Writer, phrase string) {
	words := strings.Fields(phrase)
	for i := 0; i < len(words); i += 3 {
		for j := i; j < i+3 && j < len(words); j++ {
			fmt.Fprintf(w, "%2d) %-9s", j+1, words[j])
		}
		fmt.Fprintln(w)
	}
}

// confirm asks three words of the phrase back. A wrong answer means the phrase was not
// written down: nothing is stored. The positions are sorted, so the words are typed in the
// order they are numbered on the screen.
func confirm(env *Env, phrase string) error {
	words := strings.Fields(phrase)
	pos, err := env.positions()
	if err != nil {
		return err
	}
	if len(pos) != 3 {
		return errors.New("pair: the check asks three words")
	}
	sort.Ints(pos)
	fmt.Fprint(env.Out, "Confirm — type word ")
	for i, p := range pos {
		if i > 0 {
			fmt.Fprint(env.Out, ", ")
		}
		fmt.Fprintf(env.Out, "%d", p)
	}
	fmt.Fprintln(env.Out, ":")
	sc := bufio.NewScanner(env.In)
	for _, p := range pos {
		if !sc.Scan() {
			return errors.New("pair: the check was not answered")
		}
		if strings.TrimSpace(sc.Text()) != words[p-1] {
			return fmt.Errorf("pair: word %d does not match, nothing was stored", p)
		}
	}
	return nil
}

// save pins the account and signs the certificate of this device, then writes them.
func save(env *Env, a *identity.Account) error {
	p, err := identity.Open(env.Dir)
	if err != nil {
		return err
	}
	if err := p.Pin(a.ID); err != nil {
		return err
	}
	name := env.Name
	if name == "" {
		name = "pod"
	}
	if err := p.SetCert(identity.NewCert(a, p.Key(), name, identity.KindPod)); err != nil {
		return err
	}
	fmt.Fprintf(env.Out, "device      : %s (%s)\n", p.ID(), identity.Fingerprint(p.ID()))
	fmt.Fprintf(env.Out, "account     : %s (%s)\n", p.Account, identity.Fingerprint(p.Account))
	fmt.Fprintln(env.Out, "stored      : device.key (0600) and identity.json — no phrase, no account key")
	return nil
}

// writeDeviceKey stores a device seed (0600): the key of an admin device, derived from the
// phrase.
func writeDeviceKey(dir string, seed []byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "device.key"), []byte(hex.EncodeToString(seed)+"\n"), 0o600)
}

func readLine(r io.Reader) (string, error) {
	sc := bufio.NewScanner(r)
	if !sc.Scan() {
		return "", errors.New("pair: nothing to read")
	}
	return strings.TrimSpace(sc.Text()), nil
}

// inviteLink is the address a phone opens from the QR: the relay origin, the code in the
// hash (the hash is not sent to the server).
func inviteLink(relayURL, code string) string {
	u := strings.TrimSuffix(relayURL, "/")
	u = strings.Replace(u, "wss:", "https:", 1)
	u = strings.Replace(u, "ws:", "http:", 1)
	return u + "/#join=" + code
}

func (env *Env) timeout() time.Duration {
	if env.Timeout > 0 {
		return env.Timeout
	}
	return 5 * time.Minute
}

// msg is one message of the pairing link (the shape the relay reads on /_relay/identity).
type msg struct {
	Type    string                `json:"type"`
	Account string                `json:"account,omitempty"`
	Key     string                `json:"key,omitempty"`
	Name    string                `json:"name,omitempty"`
	Kind    string                `json:"kind,omitempty"`
	Code    string                `json:"code,omitempty"`
	Cert    *identity.Cert        `json:"cert,omitempty"`
	Pairing *pairing              `json:"pairing,omitempty"`
	List    *identity.Revocations `json:"list,omitempty"`
	Message string                `json:"message,omitempty"`
}

type pairing struct {
	Code string         `json:"code"`
	Key  string         `json:"key"`
	Cert *identity.Cert `json:"cert,omitempty"`
	Used bool           `json:"used"`
}

func send(ctx context.Context, ws *websocket.Conn, m msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return ws.Write(ctx, websocket.MessageText, b)
}

func recv(ctx context.Context, ws *websocket.Conn) (msg, error) {
	_, b, err := ws.Read(ctx)
	var m msg
	if err != nil {
		return m, err
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

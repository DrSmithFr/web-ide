package lan

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base32"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/DrSmithFr/web-ide/pod/internal/i18n"
	"github.com/DrSmithFr/web-ide/pod/internal/identity"
)

// Pairing over the LAN, with no relay: the joining pod serves its request on its LAN port
// (and announces it by mDNS); a paired pod that finds it shows it in the Devices panel of
// its page, where an admin compares the 4-word fingerprint and signs the certificate. The
// certificate is posted back to the joining pod, which checks it is for its own key.

// joinPath serves the request of a joining pod (GET) and takes its certificate (POST).
const joinPath = "/_lan/join"

// JoinRequest is what a joining pod shows: the same shape as a pairing of the relay, so the
// Devices panel reads both.
type JoinRequest struct {
	Code    string         `json:"code"`
	Account string         `json:"account"`
	Key     string         `json:"key"`
	Name    string         `json:"name"`
	Kind    string         `json:"kind"`
	Cert    *identity.Cert `json:"cert,omitempty"`
	Created int64          `json:"created"`
	Expires int64          `json:"expires"`
	Used    bool           `json:"used"`
}

// JoinConfig is the joining side.
type JoinConfig struct {
	Dir      string // the data folder (lan.crt)
	Addr     string // the LAN listen address (":4436")
	Identity *identity.PodIdentity
	Name     string
	Resolver Resolver // announces the request by mDNS; nil: the address is typed by hand
	// OnListen is told the port listened on, to print the addresses to type.
	OnListen func(code string, port int)
	Timeout  time.Duration
}

// WaitJoin serves the request of this pod on the LAN until an admin approves it, and returns
// the certificate signed for its key (checked against the account it names).
func WaitJoin(ctx context.Context, cfg JoinConfig) (*identity.Cert, error) {
	if cfg.Addr == "" {
		cfg.Addr = DefaultAddr
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	cert, err := LoadCert(cfg.Dir)
	if err != nil {
		return nil, err
	}
	code := newCode()
	now := time.Now()
	req := JoinRequest{Code: code, Key: cfg.Identity.ID(), Name: cfg.Name, Kind: identity.KindPod,
		Created: now.Unix(), Expires: now.Add(cfg.Timeout).Unix()}
	got := make(chan *identity.Cert, 1)
	mux := http.NewServeMux()
	mux.HandleFunc(joinPath, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(req)
			return
		}
		var body struct {
			Code string         `json:"code"`
			Cert *identity.Cert `json:"cert"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body) != nil || body.Code != code || body.Cert == nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		// Only a certificate for this key, signed by the account it names.
		if body.Cert.Key != req.Key || body.Cert.Kind != identity.KindPod || identity.VerifyCert(body.Cert, body.Cert.Account, nil) != nil {
			http.Error(w, "not a certificate of this device", http.StatusBadRequest)
			return
		}
		select {
		case got <- body.Cert:
		default:
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	ln, err := tls.Listen("tcp", cfg.Addr, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13, NextProtos: []string{"http/1.1"}})
	if err != nil {
		return nil, err
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second, ErrorLog: quiet}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if cfg.Resolver != nil {
		if stop, err := cfg.Resolver.Announce(port, Record{Pod: shortID(req.Key), Join: code}); err == nil {
			defer stop()
		}
	}
	if cfg.OnListen != nil {
		cfg.OnListen(code, port)
	}
	select {
	case c := <-got:
		time.Sleep(200 * time.Millisecond) // the answer of the POST leaves first
		return c, nil
	case <-ctx.Done():
		return nil, i18n.New("the approval did not come")
	}
}

// LocalAddrs are the addresses of this machine on the LAN, with a port: what to type in
// Settings of a paired pod when mDNS does not find the joining one.
func LocalAddrs(port int) []string {
	var out []string
	for _, ip := range localIPs() {
		out = append(out, net.JoinHostPort(ip.String(), strconv.Itoa(port)))
	}
	return out
}

func newCode() string {
	b := make([]byte, 5)
	_, _ = rand.Read(b)
	return base32.StdEncoding.EncodeToString(b)
}

// ---------- the paired side ----------

// joinReq is a joining pod found on the LAN.
type joinReq struct {
	JoinRequest
	addr string
	fp   string // the TLS certificate it showed: the approval goes to the same one
}

// probeJoin reads the request of a joining pod at addr, if there is one.
func (m *Manager) probeJoin(ctx context.Context, addr string) {
	var fp string
	cli := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13,
		VerifyConnection: func(cs tls.ConnectionState) error { fp = leafFingerprint(&cs); return nil },
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+addr+joinPath, nil)
	res, err := cli.Do(req)
	var j JoinRequest
	ok := err == nil && res.StatusCode == http.StatusOK && json.NewDecoder(res.Body).Decode(&j) == nil && j.Code != "" && j.Key != ""
	if res != nil {
		res.Body.Close()
	}
	m.mu.Lock()
	if !ok {
		_, had := m.joins[addr]
		delete(m.joins, addr)
		m.mu.Unlock()
		if had {
			m.changed()
		}
		return
	}
	if old := m.joins[addr]; old != nil && old.Code == j.Code {
		m.mu.Unlock()
		return
	}
	m.joins[addr] = &joinReq{JoinRequest: j, addr: addr, fp: fp}
	m.mu.Unlock()
	m.changed()
	account, _, _ := m.cfg.Identity.State()
	m.pushIdentity(idMsg{Type: "pairings", Account: account, Pairings: m.Joins()}, nil)
}

// Joins are the joining pods found on the LAN, for the Devices panel.
func (m *Manager) Joins() []JoinRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []JoinRequest{}
	for _, j := range m.joins {
		out = append(out, j.JoinRequest)
	}
	return out
}

// ApproveJoin posts the certificate an admin signed to the joining pod of the code.
func (m *Manager) ApproveJoin(ctx context.Context, code string, cert *identity.Cert) (*JoinRequest, error) {
	account, _, _ := m.cfg.Identity.State()
	if err := identity.VerifyCert(cert, account, nil); err != nil {
		return nil, err
	}
	m.mu.Lock()
	var j *joinReq
	for _, x := range m.joins {
		if x.Code == code {
			j = x
		}
	}
	m.mu.Unlock()
	if j == nil {
		return nil, i18n.New("no pairing request with this code")
	}
	if cert.Key != j.Key {
		return nil, i18n.New("the certificate is not for the device of this request")
	}
	body, _ := json.Marshal(map[string]any{"code": code, "cert": cert})
	cli := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{
		InsecureSkipVerify: true, MinVersion: tls.VersionTLS13,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if leafFingerprint(&cs) != j.fp {
				return errors.New("lan: the joining pod changed its certificate")
			}
			return nil
		},
	}}}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+j.addr+joinPath, bytes.NewReader(body))
	res, err := cli.Do(req)
	if err != nil {
		return nil, i18n.New("the joining pod does not answer")
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, i18n.New("the joining pod refused the certificate")
	}
	m.mu.Lock()
	j.Used, j.Cert, j.Account = true, cert, account
	out := j.JoinRequest
	m.mu.Unlock()
	m.changed()
	return &out, nil
}

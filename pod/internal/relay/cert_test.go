package relay

import (
	"crypto/tls"
	"net"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/crypto/acme/autocert"
)

// TestCertFilesReload: a certificate read from -cert/-key is read again as soon as the file
// changes, so a renewal that only copies new files needs no restart of the relay.
func TestCertFilesReload(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeSelfSigned(t, dir, certPath, keyPath)

	cfg, _, err := TLSConfig(dir, TLSOptions{CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	first := certPEM(t, cfg)

	// The same files: the certificate is kept in memory, not read on every hello.
	if got := certPEM(t, cfg); string(got) != string(first) {
		t.Fatal("the same certificate must be served while the files do not change")
	}

	writeSelfSigned(t, t.TempDir(), certPath, keyPath)
	second := certPEM(t, cfg)
	if string(second) == string(first) {
		t.Fatal("a certificate replaced on disk must be served without a restart")
	}
}

// TestCertFilesKeepsTheLastGoodOne: a pair that does not load (a renewal half-copied) leaves
// the one already served in place rather than cutting the TLS side.
func TestCertFilesKeepsTheLastGoodOne(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")
	writeSelfSigned(t, dir, certPath, keyPath)
	cfg, _, err := TLSConfig(dir, TLSOptions{CertFile: certPath, KeyFile: keyPath})
	if err != nil {
		t.Fatal(err)
	}
	first := certPEM(t, cfg)

	if err := os.WriteFile(keyPath, []byte("not a key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := certPEM(t, cfg); string(got) != string(first) {
		t.Fatal("a broken key must not drop the certificate in use")
	}
}

// TestAutocertDomain: with -domain, the relay gets an autocert manager (its cache lives in the
// data folder, so a restart does not ask Let's Encrypt again) and the TLS side offers the ALPN
// of the TLS-ALPN-01 check the demuxer routes to it.
func TestAutocertDomain(t *testing.T) {
	dir := t.TempDir()
	cfg, acme, err := TLSConfig(dir, TLSOptions{Domain: "antennae.space"})
	if err != nil {
		t.Fatal(err)
	}
	if acme == nil {
		t.Fatal("-domain: the relay must hold an autocert manager")
	}
	cache, ok := acme.Cache.(autocert.DirCache)
	if !ok {
		t.Fatal("the certificates must be cached in a folder (rate limits), not in memory")
	}
	// The cache lives under the data folder: it is backed up with it, and a restart does not
	// ask Let's Encrypt again.
	if string(cache) != filepath.Join(dir, "acme") {
		t.Fatalf("the ACME cache is %q, expected %q", string(cache), filepath.Join(dir, "acme"))
	}
	if acme.HTTPHandler(nil) == nil {
		t.Fatal("port 80 must answer the http-01 challenges and redirect to https")
	}
	var found bool
	for _, p := range cfg.NextProtos {
		if p == acmeTLSALPN {
			found = true
		}
	}
	if !found {
		t.Fatalf("the TLS side must offer %q, got %v", acmeTLSALPN, cfg.NextProtos)
	}
	if cfg.GetCertificate == nil {
		t.Fatal("the certificate is chosen per hello (SNI)")
	}
}

// TestSelfSignedFallback: with no -cert/-key and no -domain, the relay makes a self-signed
// certificate in its data folder, as before.
func TestSelfSignedFallback(t *testing.T) {
	dir := t.TempDir()
	cfg, acme, err := TLSConfig(dir, TLSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if acme != nil {
		t.Fatal("no -domain: no autocert manager, nothing to ask a CA for")
	}
	if len(cfg.Certificates) != 1 {
		t.Fatalf("one self-signed certificate expected, got %d", len(cfg.Certificates))
	}
	if _, err := os.Stat(filepath.Join(dir, "cert.pem")); err != nil {
		t.Fatal("the certificate is kept in the data folder:", err)
	}
}

// writeSelfSigned makes a self-signed certificate in src and copies it to certPath/keyPath (two
// runs give two different certificates).
func writeSelfSigned(t *testing.T, src, certPath, keyPath string) {
	t.Helper()
	if _, err := selfSigned(src); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"cert.pem", certPath}, {"key.pem", keyPath}} {
		data, err := os.ReadFile(filepath.Join(src, pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(pair[1], data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// certPEM asks the config for the certificate it serves and returns its PEM bytes.
func certPEM(t *testing.T, cfg *tls.Config) []byte {
	t.Helper()
	hello := &tls.ClientHelloInfo{ServerName: "localhost", Conn: &net.TCPConn{}}
	var cert *tls.Certificate
	var err error
	switch {
	case cfg.GetCertificate != nil:
		cert, err = cfg.GetCertificate(hello)
	default:
		cert = &cfg.Certificates[0]
	}
	if err != nil {
		t.Fatal("GetCertificate:", err)
	}
	if len(cert.Certificate) == 0 {
		t.Fatal("no certificate")
	}
	return cert.Certificate[0]
}

// TestDemuxACME: the TLS-ALPN-01 connections Let's Encrypt dials to validate a domain go to the
// HTTPS side of the demuxer, where autocert answers them; they are not mistaken for TURN.
func TestDemuxACME(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	cfg, _, err := TLSConfig(t.TempDir(), TLSOptions{})
	if err != nil {
		t.Fatal(err)
	}
	d, err := NewDemux(cfg, ln)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	httpsConn := make(chan error, 1)
	go func() {
		c, err := d.HTTPS.Accept()
		if err != nil {
			httpsConn <- err
			return
		}
		httpsConn <- c.(*tls.Conn).Handshake()
	}()
	turnConn := make(chan net.Conn, 1)
	go func() {
		if c, err := d.Turn.Accept(); err == nil {
			turnConn <- c
		}
	}()

	client := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost", NextProtos: []string{acmeTLSALPN}}
	c, err := tls.Dial("tcp", ln.Addr().String(), client)
	if err != nil {
		t.Fatal("the acme-tls/1 hello must reach the HTTPS listener:", err)
	}
	defer c.Close()
	if got := c.ConnectionState().NegotiatedProtocol; got != acmeTLSALPN {
		t.Fatalf("negotiated protocol %q, expected %q", got, acmeTLSALPN)
	}
	select {
	case err := <-httpsConn:
		if err != nil {
			t.Fatalf("the acme connection was not routed: %v", err)
		}
	case cc := <-turnConn:
		cc.Close()
		t.Fatal("an acme-tls/1 hello must not reach the TURN listener")
	}
}

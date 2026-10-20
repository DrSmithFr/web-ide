// Package lan lets the pods of one account reach each other on the local network with no
// relay: each paired pod listens on a TLS port of its own, finds the others by mDNS (or by
// an address typed by hand), and proves who it is with the handshake v2 of the tunnel,
// bound to the TLS certificate fingerprints instead of the DTLS ones. The local pod then
// acts as the gateway: one local port per peer, a reverse proxy to it.
package lan

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The TLS key and certificate of the pod on the LAN, in the data folder. They are
// self-signed: TLS only encrypts, the handshake inside proves who holds the device key.
const (
	certFile = "lan.crt"
	keyFile  = "lan.key"
)

// LoadCert reads the TLS certificate of the pod, or makes it on first run. The same
// certificate serves the listener and is shown as a client certificate when dialing, so a
// peer that checked it once trusts the requests that carry it.
func LoadCert(dir string) (tls.Certificate, error) {
	crt, key := filepath.Join(dir, certFile), filepath.Join(dir, keyFile)
	if c, err := tls.LoadX509KeyPair(crt, key); err == nil {
		return c, nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "web-ide pod"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(20, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(key, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(crt, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.LoadX509KeyPair(crt, key)
}

// Fingerprint is the SHA-256 of a DER certificate in the form the handshake binds (upper
// case hex, no colons), the same form as a DTLS fingerprint read from an SDP.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// leafFingerprint is the fingerprint of the first certificate a TLS peer showed.
func leafFingerprint(st *tls.ConnectionState) string {
	if st == nil || len(st.PeerCertificates) == 0 {
		return ""
	}
	return Fingerprint(st.PeerCertificates[0].Raw)
}

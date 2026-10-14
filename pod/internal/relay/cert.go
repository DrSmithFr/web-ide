package relay

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/crypto/acme"
	"golang.org/x/crypto/acme/autocert"
)

// acmeTLSALPN is the ALPN protocol of the ACME TLS-ALPN-01 challenge: Let's Encrypt proves
// the domain by dialing the TLS port of the relay and asking for it. The demuxer routes those
// connections to the HTTPS side, where autocert answers them (see -domain).
const acmeTLSALPN = "acme-tls/1"

// acmeStagingDirectory is the test CA: -acme-staging talks to it instead of Let's Encrypt, so
// a domain can be brought up without spending the production rate limits.
const acmeStagingDirectory = "https://acme-staging-v02.api.letsencrypt.org/directory"

// TLSOptions says where the certificate of the relay comes from.
type TLSOptions struct {
	CertFile string // a certificate served as is, reloaded when the file changes
	KeyFile  string
	// Domain: the certificate is issued by Let's Encrypt for this domain (autocert,
	// TLS-ALPN-01 through the demuxer, cached in <data>/acme). A self-signed one answers
	// while the domain does not resolve to this machine yet.
	Domain      string
	ACMEStaging bool
	Email       string // contact of the ACME account (the mailto: of -push-subject when there is one)
}

// TLSConfig builds the TLS config served on the TLS port of the relay: the files named by
// -cert/-key, else the Let's Encrypt certificate of -domain, else a self-signed one made on
// first run. The certificate is read again whenever it changes on disk, so a renewal needs
// no restart. The autocert manager (nil without -domain) also serves port 80.
func TLSConfig(dataDir string, opt TLSOptions) (*tls.Config, *autocert.Manager, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS12,
		NextProtos: []string{alpnTurn, acmeTLSALPN, "h2", "http/1.1"},
	}
	switch {
	case opt.CertFile != "" && opt.KeyFile != "":
		files := &certFiles{cert: opt.CertFile, key: opt.KeyFile}
		if _, err := files.get(); err != nil {
			return nil, nil, err
		}
		cfg.GetCertificate = func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return files.get() }
		return cfg, nil, nil

	case opt.Domain != "":
		mgr := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Cache:      autocert.DirCache(filepath.Join(dataDir, "acme")),
			HostPolicy: autocert.HostWhitelist(opt.Domain),
			Email:      opt.Email,
		}
		if opt.ACMEStaging {
			mgr.Client = &acme.Client{DirectoryURL: acmeStagingDirectory}
		}
		self, err := selfSigned(dataDir)
		if err != nil {
			return nil, nil, err
		}
		cfg.GetCertificate = func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			cert, err := mgr.GetCertificate(hello)
			if err == nil {
				return cert, nil
			}
			// The domain does not answer here yet (DNS not switched, ACME unreachable): the
			// relay keeps serving, on a certificate no browser trusts, rather than refusing.
			log.Printf("relay: certificate for %q: %v (self-signed until %s resolves here)",
				hello.ServerName, err, opt.Domain)
			return &self, nil
		}
		return cfg, mgr, nil

	default:
		cert, err := selfSigned(dataDir)
		if err != nil {
			return nil, nil, err
		}
		cfg.Certificates = []tls.Certificate{cert}
		return cfg, nil, nil
	}
}

// certFiles holds a certificate read from disk and reads it again as soon as one of the two
// files changes: a renewal that only copies new files in place (a deploy hook, a mounted
// secret) needs no restart of the relay. A pair that fails to load leaves the one already
// served in place, so a half-written renewal cannot cut the TLS side.
type certFiles struct {
	cert, key string
	mu        sync.Mutex
	pair      tls.Certificate
	have      bool
	certMod   time.Time
	keyMod    time.Time
}

func (f *certFiles) get() (*tls.Certificate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cm, km := modTime(f.cert), modTime(f.key)
	if f.have && cm.Equal(f.certMod) && km.Equal(f.keyMod) {
		return &f.pair, nil
	}
	pair, err := tls.LoadX509KeyPair(f.cert, f.key)
	if err != nil {
		if f.have {
			log.Printf("relay: certificate %s: %v (keeping the one in use)", f.cert, err)
			return &f.pair, nil
		}
		return nil, err
	}
	f.pair, f.have, f.certMod, f.keyMod = pair, true, cm, km
	return &f.pair, nil
}

func modTime(path string) time.Time {
	st, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return st.ModTime()
}

// selfSigned reads the certificate of the data folder or writes a new one, valid a year.
func selfSigned(dataDir string) (tls.Certificate, error) {
	certPath := filepath.Join(dataDir, "cert.pem")
	keyPath := filepath.Join(dataDir, "key.pem")
	if data, err := os.ReadFile(certPath); err == nil {
		key, err := os.ReadFile(keyPath)
		if err == nil {
			pair, err := tls.X509KeyPair(data, key)
			if err == nil {
				return pair, nil
			}
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "web-ide-relay"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, err
	}
	return tls.X509KeyPair(certPEM, keyPEM)
}

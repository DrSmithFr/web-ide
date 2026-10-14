package relay

import (
	"crypto/tls"
	"net"
	"testing"
	"time"
)

// clientHello builds a TLS ClientHello record with (or without) an ALPN extension.
func clientHello(alpn string) []byte {
	var ext []byte
	if alpn != "" {
		// ALPN extension: 2-byte data length, then the 2-byte length of the protocol list,
		// then one entry per protocol (1-byte length + the name).
		list := append([]byte{0x00, byte(1 + len(alpn)), byte(len(alpn))}, alpn...)
		ext = append(ext, 0x00, 0x10) // ALPN
		ext = append(ext, byte(len(list)>>8), byte(len(list)))
		ext = append(ext, list...)
	}
	var body []byte
	body = append(body, 0x03, 0x03)             // version
	body = append(body, make([]byte, 32)...)    // random
	body = append(body, 0)                      // empty session id
	body = append(body, 0x00, 0x02, 0x00, 0x2f) // one cipher suite
	body = append(body, 1, 0)                   // null compression
	body = append(body, byte(len(ext)>>8), byte(len(ext)))
	body = append(body, ext...)
	msg := append([]byte{0x01}, byte(len(body)>>16), byte(len(body)>>8), byte(len(body)))
	msg = append(msg, body...)
	rec := append([]byte{0x16, 0x03, 0x01}, byte(len(msg)>>8), byte(len(msg)))
	return append(rec, msg...)
}

// TestDemuxALPN: a ClientHello asking for TURN over TLS goes to the TURN side, a plain one
// to the HTTPS side, and the hello is put back so the TLS handshake still runs.
func TestDemuxALPN(t *testing.T) {
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

	// One browser asking for TURN over TLS (RFC 8489), one opening the page over https. The
	// handshake completes only when the accepted side answers, so both sides run here.
	turnConn := make(chan error, 1)
	go func() {
		c, err := d.Turn.Accept()
		if err != nil {
			turnConn <- err
			return
		}
		turnConn <- c.(*tls.Conn).Handshake()
	}()
	httpsConn := make(chan error, 1)
	go func() {
		c, err := d.HTTPS.Accept()
		if err != nil {
			httpsConn <- err
			return
		}
		httpsConn <- c.(*tls.Conn).Handshake()
	}()

	client := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost", NextProtos: []string{alpnTurn}}
	c1, err := tls.Dial("tcp", ln.Addr().String(), client)
	if err != nil {
		t.Fatal("the TURN hello must reach the TURN listener:", err)
	}
	defer c1.Close()
	if c1.ConnectionState().NegotiatedProtocol != alpnTurn {
		t.Fatalf("negotiated protocol %q, expected %q", c1.ConnectionState().NegotiatedProtocol, alpnTurn)
	}

	client2 := &tls.Config{InsecureSkipVerify: true, ServerName: "localhost", NextProtos: []string{"h2"}}
	c2, err := tls.Dial("tcp", ln.Addr().String(), client2)
	if err != nil {
		t.Fatal("the https hello must reach the HTTPS listener:", err)
	}
	defer c2.Close()

	for name, ch := range map[string]chan error{"turn": turnConn, "https": httpsConn} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatalf("the %s connection was not routed: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the demuxer did not route the %s hello", name)
		}
	}
}

// TestAlpnOf: the ALPN extension is read from a ClientHello; a hello without one, or with
// another protocol, is not mistaken for TURN.
func TestAlpnOf(t *testing.T) {
	if got := alpnOf(clientHello(alpnTurn)[5:]); got != alpnTurn {
		t.Fatalf("alpnOf read %q, expected %q", got, alpnTurn)
	}
	if got := alpnOf(clientHello("h2")[5:]); got != "" {
		t.Fatalf("a hello with h2 must not be TURN, got %q", got)
	}
	if got := alpnOf(clientHello("")[5:]); got != "" {
		t.Fatalf("a hello without ALPN must not be TURN, got %q", got)
	}
}

func selfTestCert(t *testing.T) tls.Certificate {
	t.Helper()
	cert, err := selfSigned(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

package relay

import (
	"crypto/tls"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
)

// alpnTurn is the ALPN protocol of TURN over TLS (RFC 8489). A browser that connects to the
// relay on 443 with it reaches the TURN server; without it, the same port serves the
// bootstrap page over HTTPS.
const alpnTurn = "stun.turn"

// Demux accepts TLS connections on one listener and routes each one by the ALPN protocol of
// its ClientHello: TURN to the TURN listener, everything else to HTTPS. One port, 443, is
// enough for both, which is what a browser behind a restrictive firewall needs.
type Demux struct {
	Turn  net.Listener
	HTTPS net.Listener

	cfg *tls.Config
	mu  sync.Mutex
	err error
}

// NewDemux starts the demuxer on a listener (nil → the TLS port, 443 by default).
func NewDemux(cfg *tls.Config, ln net.Listener) (*Demux, error) {
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", ":443")
		if err != nil {
			return nil, err
		}
	}
	d := &Demux{cfg: cfg}
	d.Turn = newChanListener(cfg)
	d.HTTPS = newChanListener(cfg)
	go d.run(ln)
	return d, nil
}

// Serve feeds one more listener to the same demuxer: a relay reachable on IPv4 and IPv6
// accepts on both addresses and routes them the same way (the ACME TLS-ALPN-01 connections
// of Let's Encrypt go to the HTTPS side, like a browser opening the page).
func (d *Demux) Serve(ln net.Listener) { go d.run(ln) }

func (d *Demux) run(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			d.setErr(err)
			return
		}
		pc := &prefixConn{Conn: conn}
		alpn, err := peekALPN(pc)
		if err != nil {
			conn.Close()
			continue
		}
		if alpn == alpnTurn {
			d.Turn.(*chanListener).push(pc)
		} else {
			d.HTTPS.(*chanListener).push(pc)
		}
	}
}

func (d *Demux) setErr(err error) {
	d.mu.Lock()
	d.err = err
	d.mu.Unlock()
	d.Turn.Close()
	d.HTTPS.Close()
}

// Err is the error that stopped the demuxer (nil while it runs).
func (d *Demux) Err() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.err
}

// Close stops the demuxer and its two listeners.
func (d *Demux) Close() error {
	d.Turn.Close()
	d.HTTPS.Close()
	return nil
}

// peekALPN reads the ClientHello and returns the ALPN protocol the client asks for ("" when
// it does not ask for one). The bytes stay in front of the connection so the TLS handshake
// can still run on it.
func peekALPN(pc *prefixConn) (string, error) {
	head := make([]byte, 5)
	if _, err := io.ReadFull(pc.Conn, head); err != nil {
		return "", err
	}
	if head[0] != 0x16 { // not a handshake record
		return "", errors.New("relay: not a TLS handshake")
	}
	n := int(binary.BigEndian.Uint16(head[3:5]))
	if n == 0 || n > 4096 {
		return "", errors.New("relay: bad TLS record")
	}
	rec := make([]byte, n)
	if _, err := io.ReadFull(pc.Conn, rec); err != nil {
		return "", err
	}
	pc.buf = append(pc.buf, head...)
	pc.buf = append(pc.buf, rec...)
	return alpnOf(rec), nil
}

// alpnOf reads the ALPN extension of a ClientHello message.
func alpnOf(rec []byte) string {
	if len(rec) < 4 || rec[0] != 0x01 { // ClientHello
		return ""
	}
	p := 4 + 2 + 32 // handshake header + version + random
	if p >= len(rec) {
		return ""
	}
	skip := func() bool {
		if p+1 > len(rec) {
			return true
		}
		p += 1 + int(rec[p])
		return p > len(rec)
	}
	if skip() { // session id
		return ""
	}
	if p+2 > len(rec) {
		return ""
	}
	p += 2 + int(binary.BigEndian.Uint16(rec[p:p+2])) // cipher suites
	if p >= len(rec) {
		return ""
	}
	if skip() { // compression methods
		return ""
	}
	if p+2 > len(rec) {
		return ""
	}
	end := p + 2 + int(binary.BigEndian.Uint16(rec[p:p+2]))
	p += 2
	for p+4 <= end && end <= len(rec) {
		etype := int(binary.BigEndian.Uint16(rec[p : p+2]))
		elen := int(binary.BigEndian.Uint16(rec[p+2 : p+4]))
		p += 4
		if etype != 16 { // not ALPN
			p += elen
			continue
		}
		// ALPN data: the 2-byte length of the protocol list, then one entry per protocol
		// (1-byte length + the name).
		if elen < 3 || p+2 > end {
			return ""
		}
		total := int(binary.BigEndian.Uint16(rec[p : p+2]))
		if total > elen-2 {
			return ""
		}
		for q := p + 2; q < p+2+total; {
			l := int(rec[q])
			if q+1+l > end {
				return ""
			}
			if string(rec[q+1:q+1+l]) == alpnTurn {
				return alpnTurn
			}
			q += 1 + l
		}
		return ""
	}
	return ""
}

// prefixConn puts the bytes already read back in front of the connection: the demuxer has
// consumed the ClientHello, the TLS server must still see it.
type prefixConn struct {
	net.Conn
	buf []byte
}

func (c *prefixConn) Read(b []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(b, c.buf)
		c.buf = c.buf[n:]
		return n, nil
	}
	return c.Conn.Read(b)
}

// chanListener is a net.Listener fed by the demuxer: the connections it routed. The
// connections are buffered so the demuxer keeps reading new ones while the accepted side has
// not started yet.
type chanListener struct {
	cfg    *tls.Config
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newChanListener(cfg *tls.Config) *chanListener {
	return &chanListener{cfg: cfg, conns: make(chan net.Conn, 64), closed: make(chan struct{})}
}

func (l *chanListener) push(c net.Conn) {
	tlsConn := tls.Server(c, l.cfg)
	select {
	case l.conns <- tlsConn:
	case <-l.closed:
		tlsConn.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c, ok := <-l.conns:
		if !ok {
			return nil, errors.New("relay: the TLS listener is closed")
		}
		return c, nil
	case <-l.closed:
		return nil, errors.New("relay: the TLS listener is closed")
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() {
		close(l.closed)
		close(l.conns)
	})
	return nil
}

func (l *chanListener) Addr() net.Addr { return &net.TCPAddr{Port: 443} }

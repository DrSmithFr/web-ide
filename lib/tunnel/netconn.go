package tunnel

import (
	"net"
	"os"
	"sync"
	"time"
)

// NetConn is a byte stream over a whole-message channel (a conn:<n> channel of a pod-to-pod
// tunnel): each Write sends its bytes as messages of 64 KB at most, Read returns them in order.
// Read deadlines are kept: http.Server sets one in the past to stop its background read before it
// hands the connection over (a WebSocket upgraded), and would wait forever otherwise.
func NetConn(conn MsgConn) net.Conn {
	return &netConn{conn: conn, msgs: make(chan msg, 1), deadline: make(chan struct{}), changed: make(chan struct{})}
}

type msg struct {
	b   []byte
	err error
}

type netConn struct {
	conn MsgConn
	buf  []byte
	once sync.Once
	pump sync.Once
	msgs chan msg
	last error // the error that ended the channel

	mu       sync.Mutex
	deadline chan struct{} // closed when the read deadline passes
	changed  chan struct{} // closed when the deadline changes: a Read waiting looks again
	timer    *time.Timer
}

func (m *netConn) Read(b []byte) (int, error) {
	m.pump.Do(func() {
		go func() {
			for {
				b, err := m.conn.Read()
				m.msgs <- msg{b, err}
				if err != nil {
					return
				}
			}
		}()
	})
	for len(m.buf) == 0 {
		if m.last != nil {
			return 0, m.last
		}
		m.mu.Lock()
		deadline, changed := m.deadline, m.changed
		m.mu.Unlock()
		select {
		case r := <-m.msgs:
			if r.err != nil {
				m.last = r.err
				return 0, r.err
			}
			m.buf = r.b
		case <-deadline:
			return 0, os.ErrDeadlineExceeded
		case <-changed:
		}
	}
	n := copy(b, m.buf)
	m.buf = m.buf[n:]
	return n, nil
}

func (m *netConn) Write(b []byte) (int, error) {
	n := len(b)
	for len(b) > 0 {
		c := min(len(b), 64*1024)
		chunk := make([]byte, c)
		copy(chunk, b[:c])
		if err := m.conn.Write(chunk); err != nil {
			return 0, err
		}
		b = b[c:]
	}
	return n, nil
}

func (m *netConn) Close() error {
	var err error
	m.once.Do(func() { err = m.conn.Close() })
	return err
}

func (m *netConn) SetReadDeadline(t time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.timer != nil {
		m.timer.Stop()
		m.timer = nil
	}
	d := make(chan struct{})
	m.deadline = d
	close(m.changed)
	m.changed = make(chan struct{})
	switch {
	case t.IsZero():
	case !t.After(time.Now()):
		close(d)
	default:
		m.timer = time.AfterFunc(time.Until(t), func() { close(d) })
	}
	return nil
}

func (m *netConn) SetDeadline(t time.Time) error      { return m.SetReadDeadline(t) }
func (m *netConn) SetWriteDeadline(t time.Time) error { return nil }
func (m *netConn) LocalAddr() net.Addr                { return addr{} }
func (m *netConn) RemoteAddr() net.Addr               { return addr{} }

type addr struct{}

func (addr) Network() string { return "tunnel" }
func (addr) String() string  { return "tunnel" }

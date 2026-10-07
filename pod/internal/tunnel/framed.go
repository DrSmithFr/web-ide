package tunnel

import (
	"time"
)

// Framed is a MsgConn over a Chunk transport with back-pressure: a Write waits until
// the transport has sent enough of what it holds (Buffered) so that taking in the
// message stays under the high-water mark. The transport still applies its own
// back-pressure; this keeps memory flat when the peer reads slowly. A high-water mark
// of 0 disables the pre-check.
type Framed struct {
	c   Chunk
	hiW int
}

// NewFramed wraps a chunk transport with back-pressure.
func NewFramed(c Chunk, hiW int) *Framed { return &Framed{c: c, hiW: hiW} }

func (f *Framed) Read() ([]byte, error) { return f.c.ReadMsg() }

func (f *Framed) Write(b []byte) error {
	for f.hiW > 0 && f.c.Buffered()+len(b) >= f.hiW {
		time.Sleep(5 * time.Millisecond)
	}
	return f.c.WriteMsg(b)
}

func (f *Framed) Close() error { return f.c.Close() }

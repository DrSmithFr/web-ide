package tunnel

import (
	"time"
)

// fragMore marks a fragment that is followed by more; a 0 header is the last fragment.
// A whole message is thus 1..N fragments of at most fragSize payload bytes, each with a
// 1-byte header. Chrome caps a DataChannel message at ~256 KB, so a 2 MB transfer is
// split into 128 fragments, far below the cap.
const (
	fragMore = 0x01
	fragSize = 16 * 1024
)

// Framed is a MsgConn over a Chunk transport, with framing and back-pressure. A Write
// splits the message into 16 KB fragments (1-byte header: more/last); a Read
// reassembles them into one whole message. Back-pressure: a Write waits until the
// transport has sent enough of what it holds (Buffered) so that taking in the message
// stays under the high-water mark. The transport still applies its own back-pressure.
type Framed struct {
	c   Chunk
	hiW int
	buf []byte
}

// NewFramed wraps a chunk transport with framing and back-pressure.
func NewFramed(c Chunk, hiW int) *Framed { return &Framed{c: c, hiW: hiW} }

// Read returns one whole message, reassembling its fragments.
func (f *Framed) Read() ([]byte, error) {
	for {
		b, err := f.c.ReadMsg()
		if err != nil {
			f.buf = nil
			return nil, err
		}
		if len(b) == 0 {
			continue
		}
		f.buf = append(f.buf, b[1:]...)
		if b[0]&fragMore == 0 {
			msg := f.buf
			f.buf = nil // the returned message must not be clobbered by the next Read
			return msg, nil
		}
	}
}

// Write sends one whole message, split into fragments of at most fragSize bytes.
func (f *Framed) Write(b []byte) error {
	if f.hiW > 0 {
		for f.c.Buffered()+len(b) >= f.hiW {
			time.Sleep(5 * time.Millisecond)
		}
	}
	for off := 0; ; off += fragSize {
		end := off + fragSize
		if end > len(b) {
			end = len(b)
		}
		frag := make([]byte, 1+end-off)
		if end < len(b) {
			frag[0] = fragMore
		}
		copy(frag[1:], b[off:end])
		if err := f.c.WriteMsg(frag); err != nil {
			return err
		}
		if end >= len(b) {
			return nil
		}
	}
}

func (f *Framed) Close() error { return f.c.Close() }

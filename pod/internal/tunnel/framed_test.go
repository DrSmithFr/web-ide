package tunnel

import (
	"io"
	"sync"
	"testing"
	"time"
)

// chunk is a Chunk transport with a bounded queue, to exercise back-pressure.
type chunk struct {
	mu     sync.Mutex
	queued int
	in     chan []byte
	closed chan struct{}
	oc     sync.Once
}

func newChunk(n int) *chunk {
	return &chunk{in: make(chan []byte, n), closed: make(chan struct{})}
}

func (c *chunk) ReadMsg() ([]byte, error) {
	select {
	case b, ok := <-c.in:
		if !ok {
			return nil, io.EOF
		}
		return b, nil
	case <-c.closed:
		return nil, io.EOF
	}
}

func (c *chunk) WriteMsg(b []byte) error {
	c.mu.Lock()
	c.queued += len(b)
	c.mu.Unlock()
	select {
	case c.in <- b:
	case <-c.closed:
		return io.EOF
	}
	return nil
}

func (c *chunk) Buffered() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queued
}

func (c *chunk) Close() error { c.oc.Do(func() { close(c.closed) }); return nil }

// release drops queued bytes (a reader consuming), lowering Buffered.
func (c *chunk) release(n int) {
	c.mu.Lock()
	c.queued -= n
	if c.queued < 0 {
		c.queued = 0
	}
	c.mu.Unlock()
}

// TestFramedBackPressure: a write above the high-water mark blocks until the transport
// drains enough, then goes through.
func TestFramedBackPressure(t *testing.T) {
	c := newChunk(8)
	f := NewFramed(c, 1024)
	// Fill the transport above the high-water mark.
	for i := 0; i < 2; i++ {
		_ = c.WriteMsg(make([]byte, 600))
	}
	done := make(chan struct{})
	go func() {
		// A write that would exceed 1024 once the transport drains: must block first.
		_ = f.Write(make([]byte, 200))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("write must block while above the high-water mark")
	case <-time.After(30 * time.Millisecond):
	}
	// Drain enough, the write must then go through.
	go func() { c.release(2000); _ = f.Write(make([]byte, 200)) }()
	<-done
}

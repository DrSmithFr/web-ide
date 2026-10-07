package tunnel

import (
	"bytes"
	"io"
	"sync"
	"testing"
	"time"
)

// chunk is a Chunk transport with a bounded queue, to exercise back-pressure and
// framing. It keeps every message whole (a DataChannel does), so framing must not rely
// on the transport reassembling: the 16 KB fragments are separate messages.
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
	select {
	case c.in <- b:
	case <-c.closed:
		return io.EOF
	}
	c.mu.Lock()
	c.queued += len(b)
	c.mu.Unlock()
	return nil
}

func (c *chunk) Buffered() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.queued
}

func (c *chunk) drain(n int) {
	c.mu.Lock()
	c.queued -= n
	if c.queued < 0 {
		c.queued = 0
	}
	c.mu.Unlock()
}

func (c *chunk) Close() error { c.oc.Do(func() { close(c.closed) }); return nil }

// TestFramedSplitsAndReassembles: a 2 MB message is split into 16 KB fragments below
// Chrome's ~256 KB cap, and reassembled back to the exact bytes on the other side.
func TestFramedSplitsAndReassembles(t *testing.T) {
	src, dst := newChunk(64), newChunk(64)
	go pump(src, dst) // forward whole fragments src -> dst, one direction

	f := NewFramed(src, 0)
	// 2 MB, with a pattern so a reorder or drop is caught.
	msg := make([]byte, 2*1024*1024)
	for i := range msg {
		msg[i] = byte(i % 251)
	}
	if err := f.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The other side reads it back whole, reassembling the fragments.
	back := NewFramed(dst, 0)
	got, err := back.Read()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("2 MB message not reassembled: got %d bytes, want %d", len(got), len(msg))
	}
}

// pump forwards whole messages from a to b (one transport direction).
func pump(a, b *chunk) {
	for {
		msg, err := a.ReadMsg()
		if err != nil {
			return
		}
		if err := b.WriteMsg(msg); err != nil {
			return
		}
	}
}

// TestFramedFragmentCap: no fragment exceeds the cap (16 KB payload + 1 header).
func TestFramedFragmentCap(t *testing.T) {
	c := newChunk(64)
	f := NewFramed(c, 0)
	_ = f.Write(make([]byte, 4096))
	// Each message the transport held is one fragment: 1 header + up to 16 KB.
	select {
	case frag := <-c.in:
		if len(frag) > 1+16*1024 {
			t.Fatalf("fragment too large: %d bytes", len(frag))
		}
	default:
		t.Fatal("no fragment written")
	}
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
		_ = f.Write(make([]byte, 200)) // would exceed 1024: must block first
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("write must block while above the high-water mark")
	case <-time.After(30 * time.Millisecond):
	}
	// Drain enough, the write must then go through.
	c.drain(2000)
	<-done
}

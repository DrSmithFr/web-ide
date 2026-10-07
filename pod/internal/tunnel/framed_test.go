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
		c.drain(len(b)) // consumed off the wire: no longer in flight
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
// Chrome's ~256 KB cap, and reassembled back to the exact bytes on the other side,
// with back-pressure on (the transport drains as the reader reads it).
func TestFramedSplitsAndReassembles(t *testing.T) {
	c := newChunk(64) // one wire: whole fragments, bounded
	f := NewFramed(c, 256*1024)
	back := NewFramed(c, 256*1024)
	msg := make([]byte, 2*1024*1024)
	for i := range msg {
		msg[i] = byte(i % 251)
	}
	done := make(chan error, 1)
	go func() { done <- f.Write(msg) }()
	got, err := back.Read() // reassemble, draining the transport as it goes
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, msg) {
		t.Fatalf("2 MB message not reassembled: got %d bytes, want %d", len(got), len(msg))
	}
	if err := <-done; err != nil {
		t.Fatalf("write: %v", err)
	}
}

// TestFramedFragmentCap: no fragment exceeds the cap (16 KB payload + 1 header).
func TestFramedFragmentCap(t *testing.T) {
	c := newChunk(64)
	f := NewFramed(c, 0)
	_ = f.Write(make([]byte, 4096))
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

// TestFramedNoDeadlockLarge: a write far above the high-water mark (2 MB) must not
// deadlock: it back-pressures per 16 KB fragment, not on the whole message. A reader
// drains the transport (keeps Buffered low) while the write runs.
func TestFramedNoDeadlockLarge(t *testing.T) {
	c := newChunk(64)
	f := NewFramed(c, 64*1024)
	// The reader drains the transport so back-pressure can flow; it exits on Close.
	ran := make(chan struct{})
	go func() {
		for {
			if _, err := c.ReadMsg(); err != nil {
				close(ran)
				return
			}
		}
	}()
	werr := make(chan error, 1)
	go func() { werr <- f.Write(make([]byte, 2*1024*1024)) }()
	select {
	case err := <-werr:
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a 2 MB write with back-pressure must not deadlock")
	}
	c.Close() // let the reader exit
	<-ran
}

// Package tunnel is the shared layer of the WebRTC tunnel: whole-message framing with
// back-pressure and the channel-binding handshake that proves the pod token to the
// browser without ever sending it to the relay. It is transport-agnostic: a MsgConn
// reads and writes whole messages, while the underlying Chunk delivers one transport
// message at a time (a WebSocket frame, a WebRTC DataChannel message).
package tunnel

import (
	"errors"
	"io"
)

// MsgConn reads and writes whole messages: one Read returns one complete message, and a
// Write sends one complete message (blocking until the transport has taken it in).
type MsgConn interface {
	Read() ([]byte, error)
	Write(b []byte) error
	Close() error
}

// Chunk delivers one transport message at a time (a WebSocket frame, a WebRTC DataChannel
// message). Buffered reports the bytes still queued at the transport, for back-pressure
// (0 when the transport keeps none of its own).
type Chunk interface {
	ReadMsg() ([]byte, error)
	WriteMsg(b []byte) error
	Buffered() int
	Close() error
}

// ErrClosed is returned once a connection is closed.
var ErrClosed = errors.New("tunnel: connection closed")

// Direct is a MsgConn that maps one to one onto the chunk messages: no framing, no
// back-pressure. It is what runs over a WebSocket, where a frame is already a whole
// message.
type Direct struct{ c Chunk }

// NewDirect wraps a chunk transport as a MsgConn without framing.
func NewDirect(c Chunk) *Direct { return &Direct{c: c} }

func (m *Direct) Read() ([]byte, error) {
	b, err := m.c.ReadMsg()
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (m *Direct) Write(b []byte) error { return m.c.WriteMsg(b) }

func (m *Direct) Close() error { return m.c.Close() }

// IsEOF tells whether a read error means the transport closed cleanly.
func IsEOF(err error) bool {
	return err == io.EOF || errors.Is(err, io.EOF)
}

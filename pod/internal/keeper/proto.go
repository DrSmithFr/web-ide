// Package keeper runs processes for the pod in a second, stable service (web-ide-keeper), so
// that they survive the updates of the pod: it spawns them, holds their PTY and their output
// (numbered by offset), and lets the pod attach again from where it was. It knows processes
// and bytes only: what the pod needs to rebuild its state travels as opaque metadata.
//
// Protocol over a Unix socket: frames of a uint32 big-endian length, then a JSON header, a
// newline and a raw payload (terminal bytes, input). Requests carry an id; their answer, and
// the frames of an attachment, reuse it.
package keeper

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Protocol changes when the keeper and the pod no longer understand each other: the pod then
// runs its processes itself, and install.sh replaces the keeper.
const Protocol = 1

// ScrollbackMax is the output kept for each process.
const ScrollbackMax = 512 * 1024

const maxFrame = 4 << 20

// Request ops.
const (
	OpHello      = "hello"
	OpSpawn      = "spawn"
	OpList       = "list"
	OpAttach     = "attach"
	OpInput      = "input"
	OpCloseStdin = "close_stdin"
	OpResize     = "resize"
	OpSignal     = "signal"
	OpMeta       = "meta"
	OpForget     = "forget"
)

// Answer kinds.
const (
	KindResult = "result"
	KindError  = "error"
	KindOutput = "output"
	KindExit   = "exit"
)

type header struct {
	ID   int64           `json:"id"`
	Op   string          `json:"op,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`

	Kind      string          `json:"kind,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Offset    int64           `json:"offset,omitempty"`
	Truncated bool            `json:"truncated,omitempty"`
	Code      int             `json:"code,omitempty"`
}

type Hello struct {
	Protocol int    `json:"protocol"`
	Pid      int    `json:"pid"`
	Version  string `json:"version"`
	Started  int64  `json:"started"`
}

type Spawn struct {
	Owner string   `json:"owner"`
	Argv  []string `json:"argv"`
	Dir   string   `json:"dir,omitempty"`
	// Env is added to the environment of the keeper.
	Env  []string        `json:"env,omitempty"`
	PTY  bool            `json:"pty,omitempty"`
	Cols int             `json:"cols,omitempty"`
	Rows int             `json:"rows,omitempty"`
	Meta json.RawMessage `json:"meta,omitempty"`
}

type Spawned struct {
	ID  string `json:"id"`
	Pid int    `json:"pid"`
}

// Proc describes a process of the keeper; Base and End are the offsets of the output kept.
type Proc struct {
	ID     string          `json:"id"`
	Owner  string          `json:"owner"`
	Pid    int             `json:"pid"`
	PTY    bool            `json:"pty,omitempty"`
	Exited bool            `json:"exited,omitempty"`
	Code   int             `json:"code,omitempty"`
	Base   int64           `json:"base"`
	End    int64           `json:"end"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

type procArgs struct {
	ID     string          `json:"id"`
	Owner  string          `json:"owner,omitempty"`
	From   int64           `json:"from,omitempty"`
	Cols   int             `json:"cols,omitempty"`
	Rows   int             `json:"rows,omitempty"`
	Signal string          `json:"signal,omitempty"`
	Meta   json.RawMessage `json:"meta,omitempty"`
}

func writeFrame(w io.Writer, h header, payload []byte) error {
	data, err := json.Marshal(h)
	if err != nil {
		return err
	}
	frame := make([]byte, 4, 4+len(data)+1+len(payload))
	binary.BigEndian.PutUint32(frame, uint32(len(data)+1+len(payload)))
	frame = append(append(append(frame, data...), '\n'), payload...)
	_, err = w.Write(frame)
	return err
}

func readFrame(r *bufio.Reader) (header, []byte, error) {
	var h header
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return h, nil, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n > maxFrame {
		return h, nil, fmt.Errorf("keeper: frame of %d bytes", n)
	}
	frame := make([]byte, n)
	if _, err := io.ReadFull(r, frame); err != nil {
		return h, nil, err
	}
	i := bytes.IndexByte(frame, '\n')
	if i < 0 {
		return h, nil, errors.New("keeper: frame without header")
	}
	if err := json.Unmarshal(frame[:i], &h); err != nil {
		return h, nil, err
	}
	return h, frame[i+1:], nil
}

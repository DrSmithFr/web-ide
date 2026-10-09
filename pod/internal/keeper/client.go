package keeper

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"sync"
	"time"
)

// ErrUnreachable: the keeper does not answer (it restarts, or stopped); the client tries
// again every second in the background.
var ErrUnreachable = errors.New("the keeper is not reachable")

// Client is the side of the pod: requests, and attachments that resume from their last
// offset when the connection comes back.
type Client struct {
	path string

	mu       sync.Mutex
	conn     net.Conn
	wmu      sync.Mutex
	nextID   int64
	pending  map[int64]chan header
	attached map[int64]*Attachment // by the id of their attach request
	closed   bool
	// Retry is the delay between two reconnections (1 s).
	Retry time.Duration
}

// Dial connects to the keeper at path and checks its protocol.
func Dial(path string) (*Client, Hello, error) {
	c := &Client{path: path, pending: map[int64]chan header{}, attached: map[int64]*Attachment{}, Retry: time.Second}
	if err := c.connect(); err != nil {
		return nil, Hello{}, err
	}
	var h Hello
	if err := c.call(OpHello, nil, nil, &h); err != nil {
		c.Close()
		return nil, h, err
	}
	if h.Protocol != Protocol {
		c.Close()
		return nil, h, fmt.Errorf("keeper: protocol %d, the pod speaks %d", h.Protocol, Protocol)
	}
	return c, h, nil
}

func (c *Client) connect() error {
	conn, err := net.DialTimeout("unix", c.path, 2*time.Second)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	go c.read(conn)
	return nil
}

func (c *Client) Close() {
	c.mu.Lock()
	c.closed = true
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (c *Client) read(conn net.Conn) {
	r := bufio.NewReader(conn)
	for {
		h, payload, err := readFrame(r)
		if err != nil {
			break
		}
		c.mu.Lock()
		ch := c.pending[h.ID]
		a := c.attached[h.ID]
		c.mu.Unlock()
		switch {
		case h.Kind == KindHead && a != nil:
			if a.Head != nil {
				a.Head(h.Result)
			}
		case h.Kind == KindOutput && a != nil:
			a.output(h, payload)
		case h.Kind == KindExit && a != nil:
			c.mu.Lock()
			delete(c.attached, h.ID)
			c.mu.Unlock()
			a.mu.Lock()
			a.err = h.Error
			a.mu.Unlock()
			a.exit(h.Code)
		case ch != nil:
			ch <- h
		}
	}
	conn.Close()
	c.mu.Lock()
	if c.conn == conn {
		c.conn = nil
	}
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	closed := c.closed
	c.mu.Unlock()
	if !closed {
		go c.reconnect()
	}
}

// reconnect tries again until the keeper answers, then attaches again from the last offsets.
func (c *Client) reconnect() {
	log.Printf("keeper: connection lost, trying again")
	for {
		time.Sleep(c.Retry)
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if closed {
			return
		}
		if c.connect() == nil {
			break
		}
	}
	var h Hello
	if err := c.call(OpHello, nil, nil, &h); err != nil || h.Protocol != Protocol {
		log.Printf("keeper: the keeper came back with another protocol (%d): its processes are no longer followed", h.Protocol)
		c.mu.Lock()
		old := c.attached
		c.attached = map[int64]*Attachment{}
		c.mu.Unlock()
		for _, a := range old {
			a.exit(-1)
		}
		return
	}
	log.Printf("keeper: connected again (pid %d)", h.Pid)
	c.mu.Lock()
	old := c.attached
	c.attached = map[int64]*Attachment{}
	c.mu.Unlock()
	for _, a := range old {
		if err := c.attach(a); err != nil {
			a.exit(-1) // the process is gone (the keeper restarted)
		}
	}
}

func (c *Client) send(id int64, op string, args any, payload []byte) error {
	var raw json.RawMessage
	if args != nil {
		data, err := json.Marshal(args)
		if err != nil {
			return err
		}
		raw = data
	}
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return ErrUnreachable
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := writeFrame(conn, header{ID: id, Op: op, Args: raw}, payload); err != nil {
		return ErrUnreachable
	}
	return nil
}

func (c *Client) newID() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return c.nextID
}

// call sends a request and waits for its answer, decoded into out.
func (c *Client) call(op string, args any, payload []byte, out any) error {
	id := c.newID()
	ch := make(chan header, 1)
	c.mu.Lock()
	c.pending[id] = ch
	c.mu.Unlock()
	if err := c.send(id, op, args, payload); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return err
	}
	var h header
	var ok bool
	select {
	case h, ok = <-ch:
	case <-time.After(10 * time.Second):
	}
	c.mu.Lock()
	delete(c.pending, id)
	c.mu.Unlock()
	if !ok {
		return ErrUnreachable
	}
	if h.Kind == KindError {
		return errors.New(h.Error)
	}
	if out != nil && len(h.Result) > 0 {
		return json.Unmarshal(h.Result, out)
	}
	return nil
}

func (c *Client) Spawn(a Spawn) (Spawned, error) {
	var r Spawned
	err := c.call(OpSpawn, a, nil, &r)
	return r, err
}

// List gives the processes of owner ("" for all).
func (c *Client) List(owner string) ([]Proc, error) {
	var r []Proc
	err := c.call(OpList, procArgs{Owner: owner}, nil, &r)
	return r, err
}

func (c *Client) Input(id string, data []byte) error {
	return c.call(OpInput, procArgs{ID: id}, data, nil)
}

func (c *Client) CloseStdin(id string) error { return c.call(OpCloseStdin, procArgs{ID: id}, nil, nil) }

func (c *Client) Resize(id string, cols, rows int) error {
	return c.call(OpResize, procArgs{ID: id, Cols: cols, Rows: rows}, nil, nil)
}

// Signal: HUP (a terminal closes), INT, TERM, KILL (to the process group).
func (c *Client) Signal(id, sig string) error {
	return c.call(OpSignal, procArgs{ID: id, Signal: sig}, nil, nil)
}

func (c *Client) SetMeta(id string, meta json.RawMessage) error {
	return c.call(OpMeta, procArgs{ID: id, Meta: meta}, nil, nil)
}

// Forget drops a process (killed if it still runs).
func (c *Client) Forget(id string) error { return c.call(OpForget, procArgs{ID: id}, nil, nil) }

// Attachment follows the output of a process. Output gets each chunk with its offset; Exit
// the code once (-1 when the process is gone).
type Attachment struct {
	c      *Client
	id     string
	op     string // OpAttach, OpHTTPAttach
	err    string // the error the followed thing ended with
	mu     sync.Mutex
	next   int64 // offset of the next byte expected
	done   bool
	Output func(offset int64, data []byte, truncated bool)
	Exit   func(code int)
	// Head gets the status and headers of a relayed HTTP response (again after a reconnection).
	Head func(head json.RawMessage)
}

// Attach follows the process id from the offset from.
func (c *Client) Attach(id string, from int64, output func(offset int64, data []byte, truncated bool), exit func(code int)) (*Attachment, error) {
	a := &Attachment{c: c, id: id, op: OpAttach, next: from, Output: output, Exit: exit}
	return a, c.attach(a)
}

func (c *Client) attach(a *Attachment) error {
	reqID := c.newID()
	ch := make(chan header, 1)
	c.mu.Lock()
	c.pending[reqID] = ch
	c.attached[reqID] = a // before the request: output may follow its answer at once
	c.mu.Unlock()
	a.mu.Lock()
	from := a.next
	a.mu.Unlock()
	err := c.send(reqID, a.op, procArgs{ID: a.id, From: from}, nil)
	var h header
	ok := false
	if err == nil {
		select {
		case h, ok = <-ch:
		case <-time.After(10 * time.Second):
		}
	}
	c.mu.Lock()
	delete(c.pending, reqID)
	c.mu.Unlock()
	switch {
	case err != nil || !ok:
		// Lost with the connection: the reconnection attaches it again.
		if err == nil {
			err = ErrUnreachable
		}
		return err
	case h.Kind == KindError:
		c.mu.Lock()
		delete(c.attached, reqID)
		c.mu.Unlock()
		return errors.New(h.Error)
	}
	return nil
}

func (a *Attachment) output(h header, data []byte) {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	// A resumed attachment may send again what was already given: skip it.
	if skip := a.next - h.Offset; skip > 0 && !h.Truncated {
		if skip >= int64(len(data)) {
			a.mu.Unlock()
			return
		}
		data, h.Offset = data[skip:], a.next
	}
	a.next = h.Offset + int64(len(data))
	a.mu.Unlock()
	if a.Output != nil {
		a.Output(h.Offset, data, h.Truncated)
	}
}

func (a *Attachment) exit(code int) {
	a.mu.Lock()
	if a.done {
		a.mu.Unlock()
		return
	}
	a.done = true
	a.mu.Unlock()
	if a.Exit != nil {
		a.Exit(code)
	}
}

// Next is the offset of the next byte expected.
func (a *Attachment) Next() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.next
}

// Stop ends the attachment on the side of the pod (Exit is not called).
func (a *Attachment) Stop() {
	a.mu.Lock()
	a.done = true
	a.mu.Unlock()
	c := a.c
	c.mu.Lock()
	for id, x := range c.attached {
		if x == a {
			delete(c.attached, id)
		}
	}
	c.mu.Unlock()
}

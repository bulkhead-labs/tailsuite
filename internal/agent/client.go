package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

// ErrClosed means the session ended before the agent answered.
var ErrClosed = errors.New("agent session closed")

// Client is the host's side of one agent session. Its methods are safe to
// call concurrently.
type Client struct {
	w io.WriteCloser

	mu      sync.Mutex
	nextID  uint64
	pending map[uint64]chan Response
	err     error // why the session ended, once it has

	done chan struct{}
}

// NewClient starts a client that writes requests to w and reads responses
// from r. Close closes w, which ends the session; the agent goes on.
func NewClient(r io.Reader, w io.WriteCloser) *Client {
	c := &Client{w: w, pending: map[uint64]chan Response{}, done: make(chan struct{})}
	go c.read(r)
	return c
}

func (c *Client) read(r io.Reader) {
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 4096), maxLine)
	var err error
	for lines.Scan() {
		var resp Response
		if err = json.Unmarshal(lines.Bytes(), &resp); err != nil {
			err = fmt.Errorf("bad response from the agent: %w", err)
			break
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
	if err == nil {
		err = lines.Err()
	}
	if err == nil {
		err = ErrClosed
	} else {
		err = fmt.Errorf("%w: %w", ErrClosed, err)
	}
	c.mu.Lock()
	c.err = err
	for id, ch := range c.pending {
		close(ch)
		delete(c.pending, id)
	}
	c.mu.Unlock()
	close(c.done)
}

// call sends one request and waits for its response. An error the agent
// reports comes back with the response, which may still carry a result.
func (c *Client) call(ctx context.Context, req Request) (Response, error) {
	ch := make(chan Response, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return Response{}, err
	}
	c.nextID++
	req.ID = c.nextID
	c.pending[req.ID] = ch
	line, err := json.Marshal(req)
	if err == nil {
		// Whole lines, under the lock, so writes never interleave.
		_, err = c.w.Write(append(line, '\n'))
	}
	if err != nil {
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return Response{}, fmt.Errorf("sending to the agent: %w", err)
	}
	c.mu.Unlock()

	select {
	case resp, ok := <-ch:
		if !ok {
			return Response{}, c.Err()
		}
		if resp.Error != "" {
			return resp, fmt.Errorf("agent: %s", resp.Error)
		}
		return resp, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, req.ID)
		c.mu.Unlock()
		return Response{}, ctx.Err()
	}
}

// Hello checks the agent's protocol version, and returns its node's name.
func (c *Client) Hello(ctx context.Context) (Hello, error) {
	resp, err := c.call(ctx, Request{Op: OpHello})
	if err != nil {
		return Hello{}, err
	}
	if resp.Hello == nil {
		return Hello{}, errors.New("agent: hello without an answer")
	}
	if resp.Hello.Version != ProtocolVersion {
		return *resp.Hello, fmt.Errorf("agent speaks protocol %d, want %d", resp.Hello.Version, ProtocolVersion)
	}
	return *resp.Hello, nil
}

// SetListeners makes the node's listeners exactly want, and returns those
// open afterwards. An empty want closes them all. On error, the result
// still says what's open.
func (c *Client) SetListeners(ctx context.Context, want []Listener) ([]Listener, error) {
	resp, err := c.call(ctx, Request{Op: OpSetListeners, Want: want})
	return resp.Listeners, err
}

// Listeners returns the node's open listeners.
func (c *Client) Listeners(ctx context.Context) ([]Listener, error) {
	resp, err := c.call(ctx, Request{Op: OpListeners})
	return resp.Listeners, err
}

// Probe runs a probe from the agent's node. A non-nil error means the probe
// didn't run, or its result was lost; the outcome is in the result.
func (c *Client) Probe(ctx context.Context, spec ProbeSpec) (ProbeResult, error) {
	resp, err := c.call(ctx, Request{Op: OpProbe, Probe: &spec})
	if err != nil {
		return ProbeResult{}, err
	}
	if resp.Probe == nil {
		return ProbeResult{}, errors.New("agent: probe without a result")
	}
	return *resp.Probe, nil
}

// Close ends the session. The agent, and its listeners, go on.
func (c *Client) Close() error { return c.w.Close() }

// Done is closed when the session has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the session ended, or nil while it's open.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

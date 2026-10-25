package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"
)

// maxLine bounds one request or response line.
const maxLine = 1 << 20

// Agent is one node's agent. It owns the node's listeners, and serves any
// number of sessions, which share them.
type Agent struct {
	node string

	mu        sync.Mutex
	listeners map[Listener]io.Closer
}

// New returns the agent for the node with the given name.
func New(node string) *Agent {
	return &Agent{node: node, listeners: map[Listener]io.Closer{}}
}

// Close closes every listener.
func (a *Agent) Close() {
	a.mu.Lock()
	defer a.mu.Unlock()
	for l, c := range a.listeners {
		_ = c.Close()
		delete(a.listeners, l)
	}
}

// Listeners returns the open listeners, sorted.
func (a *Agent) Listeners() []Listener {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sortedLocked()
}

func (a *Agent) sortedLocked() []Listener {
	out := make([]Listener, 0, len(a.listeners))
	for l := range a.listeners {
		out = append(out, l)
	}
	slices.SortFunc(out, func(x, y Listener) int {
		if x.Port != y.Port {
			return x.Port - y.Port
		}
		if x.Addr != y.Addr {
			if x.Addr < y.Addr {
				return -1
			}
			return 1
		}
		if x.Proto < y.Proto {
			return -1
		}
		if x.Proto > y.Proto {
			return 1
		}
		return 0
	})
	return out
}

// SetListeners makes the node's listeners exactly want: it closes those not
// wanted, keeps those already open, and opens the rest. A listener that
// differs only in protocol is replaced. It returns the listeners open
// afterwards, and an error naming any it couldn't open.
func (a *Agent) SetListeners(want []Listener) ([]Listener, error) {
	wanted := map[Listener]bool{}
	for _, l := range want {
		if err := checkListener(l); err != nil {
			return a.Listeners(), err
		}
		if wanted[l] {
			return a.Listeners(), fmt.Errorf("listener %s/%d on %q is wanted twice", l.Proto, l.Port, l.Addr)
		}
		wanted[l] = true
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	for l, c := range a.listeners {
		if !wanted[l] {
			_ = c.Close()
			delete(a.listeners, l)
		}
	}
	var errs []error
	for _, l := range want {
		if _, open := a.listeners[l]; open {
			continue
		}
		c, err := a.open(l)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		a.listeners[l] = c
	}
	return a.sortedLocked(), errors.Join(errs...)
}

func checkListener(l Listener) error {
	switch l.Proto {
	case ProtoTCP, ProtoHTTP:
	default:
		return fmt.Errorf("listener: unknown protocol %q", l.Proto)
	}
	if l.Port < 1 || l.Port > 65535 {
		return fmt.Errorf("listener: port %d out of range", l.Port)
	}
	return nil
}

func (a *Agent) open(l Listener) (io.Closer, error) {
	addr := net.JoinHostPort(l.Addr, strconv.Itoa(l.Port))
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s on %s: %w", l.Proto, addr, err)
	}
	switch l.Proto {
	case ProtoHTTP:
		srv := &http.Server{
			Handler:           http.HandlerFunc(a.serveHTTP),
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() { _ = srv.Serve(ln) }()
		return srv, nil
	default:
		go a.serveTCP(ln)
		return ln, nil
	}
}

// serveTCP sends each connection a banner with the node's name, then
// closes it.
func (a *Agent) serveTCP(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // closed
		}
		go func() {
			defer func() { _ = conn.Close() }()
			_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			_, _ = io.WriteString(conn, bannerPrefix+a.node+"\n")
		}()
	}
}

func (a *Agent) serveHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(nodeHeader, a.node)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, bannerPrefix+a.node+"\n")
}

// Serve answers one session: it reads requests from r and writes responses
// to w until r ends or ctx is cancelled, then waits for the requests in
// flight. The listeners stay open. A clean end of input returns nil.
func (a *Agent) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	out := json.NewEncoder(w)
	var outMu sync.Mutex
	reply := func(resp Response) {
		outMu.Lock()
		defer outMu.Unlock()
		_ = out.Encode(resp) // if the host has gone, there's no one to tell
	}

	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	lines := bufio.NewScanner(r)
	lines.Buffer(make([]byte, 0, 4096), maxLine)
	for lines.Scan() {
		var req Request
		if err := json.Unmarshal(lines.Bytes(), &req); err != nil {
			reply(Response{Error: "bad request: " + err.Error()})
			continue
		}
		wg.Go(func() { reply(a.handle(ctx, req)) })
	}
	if err := lines.Err(); err != nil && !errors.Is(err, io.ErrClosedPipe) && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("reading requests: %w", err)
	}
	return nil
}

func (a *Agent) handle(ctx context.Context, req Request) Response {
	resp := Response{ID: req.ID}
	switch req.Op {
	case OpHello:
		resp.Hello = &Hello{Version: ProtocolVersion, Node: a.node}
	case OpSetListeners:
		open, err := a.SetListeners(req.Want)
		resp.Listeners = open
		if err != nil {
			resp.Error = err.Error()
		}
	case OpListeners:
		resp.Listeners = a.Listeners()
	case OpProbe:
		if req.Probe == nil {
			resp.Error = "probe: no spec"
			break
		}
		result := Probe(ctx, *req.Probe)
		resp.Probe = &result
	default:
		resp.Error = fmt.Sprintf("unknown op %q", req.Op)
	}
	return resp
}

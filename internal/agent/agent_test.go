package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// session serves one session of a over in-memory pipes. Closing the
// client ends the session; the agent goes on.
func session(t *testing.T, a *Agent) *Client {
	t.Helper()
	toAgent, fromClient := io.Pipe()
	fromAgent, toClient := io.Pipe()
	served := make(chan error, 1)
	go func() {
		served <- a.Serve(context.Background(), toAgent, toClient)
		_ = toClient.Close()
	}()
	c := NewClient(fromAgent, fromClient)
	t.Cleanup(func() {
		_ = c.Close()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("the session didn't end after its input closed")
		}
	})
	return c
}

func newAgent(t *testing.T, node string) *Agent {
	t.Helper()
	a := New(node)
	t.Cleanup(a.Close)
	return a
}

func listenLocal() (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
}

// freePort returns a port that was free a moment ago.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	return port(t, ln.Addr().String())
}

func port(t *testing.T, addr string) int {
	t.Helper()
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func local(proto string, port int) Listener {
	return Listener{Proto: proto, Port: port, Addr: "127.0.0.1"}
}

func probeLocal(proto string, port int) ProbeSpec {
	return ProbeSpec{Proto: proto, Host: "127.0.0.1", Port: port}
}

func TestHello(t *testing.T) {
	c := session(t, newAgent(t, "laptop"))
	h, err := c.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if h.Node != "laptop" || h.Version != ProtocolVersion {
		t.Errorf("hello = %+v", h)
	}
}

func TestListenersAndProbes(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	tcp, web := local(ProtoTCP, freePort(t)), local(ProtoHTTP, freePort(t))
	open, err := c.SetListeners(ctx, []Listener{tcp, web})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 2 {
		t.Fatalf("open = %+v", open)
	}
	for _, l := range []Listener{tcp, web} {
		res, err := c.Probe(ctx, probeLocal(l.Proto, l.Port))
		if err != nil {
			t.Fatal(err)
		}
		if res.Outcome != Success || !res.Connected || res.Peer != "server" {
			t.Errorf("%s probe = %+v, want success from server", l.Proto, res)
		}
		if res.Connect <= 0 || res.Total < res.Connect {
			t.Errorf("%s timings: connect %v, total %v", l.Proto, res.Connect, res.Total)
		}
		if l.Proto == ProtoHTTP && res.HTTPStatus != 200 {
			t.Errorf("HTTP status = %d", res.HTTPStatus)
		}
	}
}

// `tailsuite up` and `tailsuite test` are separate processes: what one
// session opens, the next one finds.
func TestListenersOutliveSessions(t *testing.T) {
	ctx := context.Background()
	a := newAgent(t, "server")
	first := session(t, a)
	want := local(ProtoTCP, freePort(t))
	if _, err := first.SetListeners(ctx, []Listener{want}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	<-first.Done()

	second := session(t, a)
	open, err := second.Listeners(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(open, []Listener{want}) {
		t.Errorf("listeners after the first session = %+v, want %+v", open, want)
	}
	if res, err := second.Probe(ctx, probeLocal(want.Proto, want.Port)); err != nil || res.Outcome != Success {
		t.Errorf("probe after the first session = %+v, %v", res, err)
	}
}

// set_listeners makes the set exactly what's asked: what's no longer
// wanted closes, so a stale listener can't make a probe succeed.
func TestSetListenersReconciles(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	p1, p2, p3 := freePort(t), freePort(t), freePort(t)
	if _, err := c.SetListeners(ctx, []Listener{local(ProtoTCP, p1), local(ProtoTCP, p2)}); err != nil {
		t.Fatal(err)
	}
	// p2 changes protocol; p1 goes; p3 arrives.
	open, err := c.SetListeners(ctx, []Listener{local(ProtoHTTP, p2), local(ProtoTCP, p3)})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Listener{local(ProtoHTTP, p2), local(ProtoTCP, p3)}; !sameSet(open, want) {
		t.Errorf("open = %+v, want %+v", open, want)
	}
	checks := []struct {
		probe ProbeSpec
		want  Outcome
	}{
		{probeLocal(ProtoTCP, p1), Refused},
		{probeLocal(ProtoHTTP, p2), Success},
		{probeLocal(ProtoTCP, p3), Success},
	}
	for _, ch := range checks {
		if res, _ := c.Probe(ctx, ch.probe); res.Outcome != ch.want {
			t.Errorf("%s/%d: %+v, want %s", ch.probe.Proto, ch.probe.Port, res, ch.want)
		}
	}
	if open, err := c.SetListeners(ctx, nil); err != nil || len(open) != 0 {
		t.Errorf("setting none: %+v, %v", open, err)
	}
	if res, _ := c.Probe(ctx, probeLocal(ProtoTCP, p3)); res.Outcome != Refused {
		t.Errorf("after setting none: %+v, want refused", res)
	}
}

func sameSet(a, b []Listener) bool {
	a, b = slices.Clone(a), slices.Clone(b)
	key := func(l Listener) string { return fmt.Sprint(l.Port, l.Addr, l.Proto) }
	sort := func(s []Listener) {
		slices.SortFunc(s, func(x, y Listener) int { return strings.Compare(key(x), key(y)) })
	}
	sort(a)
	sort(b)
	return slices.Equal(a, b)
}

// Two processes declaring the same set, at once, leave one listener each.
func TestSetListenersIsIdempotentAcrossSessions(t *testing.T) {
	ctx := context.Background()
	a := newAgent(t, "server")
	want := []Listener{local(ProtoTCP, freePort(t)), local(ProtoHTTP, freePort(t))}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		c := session(t, a)
		wg.Go(func() {
			_, err := c.SetListeners(ctx, want)
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	if got := a.Listeners(); !sameSet(got, want) {
		t.Errorf("listeners = %+v, want %+v", got, want)
	}
}

func TestSetListenersErrors(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	for name, want := range map[string][]Listener{
		"unknown protocol": {{Proto: "udp", Port: 53}},
		"port 0":           {{Proto: ProtoTCP, Port: 0}},
		"port too big":     {{Proto: ProtoTCP, Port: 70000}},
		"wanted twice":     {local(ProtoTCP, 4000), local(ProtoTCP, 4000)},
	} {
		if _, err := c.SetListeners(ctx, want); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}

	// A port something else holds: the others still open, and the error
	// names the one that didn't.
	busy, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = busy.Close() }()
	ok := local(ProtoTCP, freePort(t))
	open, err := c.SetListeners(ctx, []Listener{ok, local(ProtoTCP, port(t, busy.Addr().String()))})
	if err == nil || !strings.Contains(err.Error(), busy.Addr().String()) {
		t.Errorf("a busy port: err = %v", err)
	}
	if !slices.Equal(open, []Listener{ok}) {
		t.Errorf("open = %+v, want only %+v", open, ok)
	}
}

// A port with nothing on it answers with a refusal: the packets got
// through.
func TestProbeRefused(t *testing.T) {
	free := freePort(t)
	for _, proto := range []string{ProtoTCP, ProtoHTTP} {
		res := Probe(context.Background(), probeLocal(proto, free))
		if res.Outcome != Refused || res.Connected {
			t.Errorf("%s probe of a closed port = %+v, want refused", proto, res)
		}
	}
}

// An HTTP server that accepts and never answers is a timeout, but a
// connected one, unlike a drop.
func TestProbeHTTPConnectedButSilent(t *testing.T) {
	ln, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	var conns []net.Conn
	var mu sync.Mutex
	defer func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	spec := probeLocal(ProtoHTTP, port(t, ln.Addr().String()))
	spec.Timeout = Duration(300 * time.Millisecond)
	res := Probe(context.Background(), spec)
	if res.Outcome != Timeout || !res.Connected || !strings.Contains(res.Detail, "connected") {
		t.Errorf("probe = %+v, want a connected timeout", res)
	}
}

// Any TCP listener counts as reachable; only tailsuite's names its node.
func TestProbeTCPForeignListener(t *testing.T) {
	ln, err := listenLocal()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = io.WriteString(conn, "SSH-2.0-OpenSSH_9.9\r\n")
			_ = conn.Close()
		}
	}()
	res := Probe(context.Background(), probeLocal(ProtoTCP, port(t, ln.Addr().String())))
	if res.Outcome != Success || res.Peer != "" {
		t.Errorf("probe = %+v, want success with no peer", res)
	}
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

func TestClassify(t *testing.T) {
	opErr := func(errno syscall.Errno) error {
		return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
	}
	cases := []struct {
		err  error
		want Outcome
	}{
		{nil, Success},
		{opErr(syscall.ECONNREFUSED), Refused},
		{fmt.Errorf("get: %w", opErr(syscall.ECONNREFUSED)), Refused},
		{context.DeadlineExceeded, Timeout},
		{&net.OpError{Op: "dial", Net: "tcp", Err: timeoutErr{}}, Timeout},
		{opErr(syscall.ENETUNREACH), Error},
		{opErr(syscall.EHOSTUNREACH), Error},
		{context.Canceled, Error},
		{errors.New("no such host"), Error},
	}
	for _, c := range cases {
		if got := classify(c.err); got != c.want {
			t.Errorf("classify(%v) = %s, want %s", c.err, got, c.want)
		}
	}
}

func TestConcurrentRequests(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	web := local(ProtoHTTP, freePort(t))
	if _, err := c.SetListeners(ctx, []Listener{web}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 50)
	for range 50 {
		wg.Go(func() {
			res, err := c.Probe(ctx, probeLocal(ProtoHTTP, web.Port))
			if err == nil && res.Outcome != Success {
				err = fmt.Errorf("outcome %s: %s", res.Outcome, res.Detail)
			}
			errs <- err
		})
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
}

func TestBadRequests(t *testing.T) {
	ctx := context.Background()
	a := newAgent(t, "n")
	toAgent, fromClient := io.Pipe()
	fromAgent, toClient := io.Pipe()
	go func() { _ = a.Serve(ctx, toAgent, toClient) }()
	defer func() { _ = fromClient.Close() }()

	// A line that isn't JSON gets an error, and the session goes on.
	if _, err := io.WriteString(fromClient, "not json\n"); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := fromAgent.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(buf[:n]), "bad request") {
		t.Errorf("reply to garbage = %q", buf[:n])
	}

	c := NewClient(fromAgent, fromClient)
	if _, err := c.call(ctx, Request{Op: "reboot"}); err == nil || !strings.Contains(err.Error(), "unknown op") {
		t.Errorf("unknown op: %v", err)
	}
	res, err := c.Probe(ctx, ProbeSpec{Proto: "gopher", Host: "127.0.0.1", Port: 70})
	if err != nil || res.Outcome != Error {
		t.Errorf("unknown probe protocol = %+v, %v; want an error outcome", res, err)
	}
	if _, err := c.Hello(ctx); err != nil {
		t.Errorf("the session didn't survive bad requests: %v", err)
	}
}

// socketPath is short: Unix socket paths are limited to about 100 bytes.
func socketPath(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ta-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return filepath.Join(dir, "run", "agent.sock")
}

// connect opens a session the way the host does, through Connect.
func connect(t *testing.T, socket string, wait time.Duration) (*Client, <-chan error) {
	t.Helper()
	toAgent, fromClient := io.Pipe()
	fromAgent, toClient := io.Pipe()
	done := make(chan error, 1)
	go func() {
		done <- Connect(context.Background(), socket, wait, toAgent, toClient)
		_ = toClient.Close()
		_ = toAgent.Close()
	}()
	c := NewClient(fromAgent, fromClient)
	t.Cleanup(func() { _ = c.Close() })
	return c, done
}

func TestRunServesSessionsUntilStopped(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	socket := socketPath(t)
	ran := make(chan error, 1)
	go func() { ran <- Run(ctx, New("laptop"), socket, nil, io.Discard) }()

	c, done := connect(t, socket, 5*time.Second)
	if h, err := c.Hello(ctx); err != nil || h.Node != "laptop" {
		t.Fatalf("hello = %+v, %v", h, err)
	}
	web := local(ProtoTCP, freePort(t))
	if _, err := c.SetListeners(ctx, []Listener{web}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Errorf("Connect = %v, want nil after the session closed", err)
	}

	stop()
	select {
	case err := <-ran:
		if err != nil {
			t.Errorf("Run = %v, want nil when stopped", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run didn't stop")
	}
	if _, err := os.Stat(socket); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the socket is still there: %v", err)
	}
	if res := Probe(context.Background(), probeLocal(web.Proto, web.Port)); res.Outcome != Refused {
		t.Errorf("after Run stopped, its listener answered: %+v", res)
	}
}

func TestRunSupervisesTheNodesCommand(t *testing.T) {
	t.Run("its exit code", func(t *testing.T) {
		err := Run(context.Background(), New("n"), socketPath(t), []string{"sh", "-c", "exit 3"}, io.Discard)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != 3 {
			t.Errorf("Run = %v, want exit 3", err)
		}
	})
	t.Run("stopped with the agent", func(t *testing.T) {
		ctx, stop := context.WithCancel(context.Background())
		ran := make(chan error, 1)
		go func() { ran <- Run(ctx, New("n"), socketPath(t), []string{"sleep", "60"}, io.Discard) }()
		time.Sleep(200 * time.Millisecond)
		stop()
		select {
		case err := <-ran:
			if err != nil {
				t.Errorf("Run = %v, want nil", err)
			}
		case <-time.After(stopGrace / 2):
			t.Fatal("the command didn't stop on SIGTERM")
		}
	})
	t.Run("a command that can't start", func(t *testing.T) {
		if err := Run(context.Background(), New("n"), socketPath(t), []string{"/nonexistent/tailscaled"}, io.Discard); err == nil {
			t.Error("Run succeeded without its command")
		}
	})
}

// The host may connect before the agent has started.
func TestConnectWaitsForTheAgent(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	socket := socketPath(t)
	c, _ := connect(t, socket, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	go func() { _ = Run(ctx, New("late"), socket, nil, io.Discard) }()
	if h, err := c.Hello(ctx); err != nil || h.Node != "late" {
		t.Errorf("hello = %+v, %v", h, err)
	}
}

func TestConnectGivesUp(t *testing.T) {
	_, done := connect(t, socketPath(t), 200*time.Millisecond)
	select {
	case err := <-done:
		if err == nil {
			t.Error("Connect succeeded with no agent")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Connect didn't give up")
	}
}

func TestMainCommand(t *testing.T) {
	for _, args := range [][]string{nil, {"serve"}, {"run"}, {"run", "--bogus"}, {"connect", "--bogus"}} {
		if code := Main(args, strings.NewReader(""), io.Discard, io.Discard); code != 2 {
			t.Errorf("Main(%q) = %d, want 2", args, code)
		}
	}
	if code := Main([]string{"connect", "--socket", socketPath(t), "--wait", "100ms"}, strings.NewReader(""), io.Discard, io.Discard); code != 1 {
		t.Errorf("connect with no agent = %d, want 1", code)
	}
	if code := Main([]string{"run", "--node", "n", "--socket", socketPath(t), "--", "sh", "-c", "exit 4"}, strings.NewReader(""), io.Discard, io.Discard); code != 4 {
		t.Errorf("run's code = %d, want the command's 4", code)
	}
}

func TestDurationJSON(t *testing.T) {
	d := Duration(1500 * time.Millisecond)
	b, err := d.MarshalJSON()
	if err != nil || string(b) != `"1.5s"` {
		t.Fatalf("marshal = %s, %v", b, err)
	}
	var back Duration
	if err := back.UnmarshalJSON(b); err != nil || back != d {
		t.Fatalf("unmarshal = %v, %v", back, err)
	}
	if err := back.UnmarshalJSON([]byte(`"soon"`)); err == nil {
		t.Error("a bad duration was accepted")
	}
}

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
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
	open, _, err := c.SetListeners(ctx, []Listener{tcp, web})
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
	if _, _, err := first.SetListeners(ctx, []Listener{want}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	<-first.Done()

	second := session(t, a)
	open, _, err := second.Listeners(ctx)
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
	if _, _, err := c.SetListeners(ctx, []Listener{local(ProtoTCP, p1), local(ProtoTCP, p2)}); err != nil {
		t.Fatal(err)
	}
	// p2 changes protocol; p1 goes; p3 arrives.
	open, _, err := c.SetListeners(ctx, []Listener{local(ProtoHTTP, p2), local(ProtoTCP, p3)})
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
	if open, _, err := c.SetListeners(ctx, nil); err != nil || len(open) != 0 {
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
			_, _, err := c.SetListeners(ctx, want)
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
	if got, _ := a.Listeners(); !sameSet(got, want) {
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
		if _, _, err := c.SetListeners(ctx, want); err == nil {
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
	open, _, err := c.SetListeners(ctx, []Listener{ok, local(ProtoTCP, port(t, busy.Addr().String()))})
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
	if _, _, err := c.SetListeners(ctx, []Listener{web}); err != nil {
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
	if res, err := c.Probe(ctx, ProbeSpec{Proto: "gopher", Host: "127.0.0.1", Port: 70}); err == nil {
		t.Errorf("unknown probe protocol = %+v, want a request error", res)
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
	if _, _, err := c.SetListeners(ctx, []Listener{web}); err != nil {
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
	t.Run("killed by a signal", func(t *testing.T) {
		err := Run(context.Background(), New("n"), socketPath(t), []string{"sh", "-c", "kill -9 $$"}, io.Discard)
		var exit *ExitError
		if !errors.As(err, &exit) || exit.Code != 137 {
			t.Errorf("Run = %v, want exit 137 (128+SIGKILL)", err)
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
			// sleep dies of the SIGTERM; a stopped node never reads as clean.
			var exit *ExitError
			if !errors.As(err, &exit) || exit.Code != 143 {
				t.Errorf("Run = %v, want exit 143 (128+SIGTERM)", err)
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

// A malformed probe spec is the host's mistake: refused as a request
// error, never reported as something the lab saw. The first two are the
// review's reproductions: before, they reached another port and another
// host, and reported success.
func TestBadProbeSpecs(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	web := local(ProtoHTTP, freePort(t))
	if _, _, err := c.SetListeners(ctx, []Listener{web}); err != nil {
		t.Fatal(err)
	}
	p := strconv.Itoa(web.Port)
	bad := map[string]ProbeSpec{
		"a path that changes the port": {Proto: ProtoHTTP, Host: "127.0.0.1", Port: web.Port / 10, Path: p[len(p)-1:] + "/"},
		"a path that changes the host": {Proto: ProtoHTTP, Host: "192.0.2.1", Port: 80, Path: "@127.0.0.1:" + p + "/"},
		"port 0":                       {Proto: ProtoTCP, Host: "127.0.0.1", Port: 0},
		"port 65536":                   {Proto: ProtoTCP, Host: "127.0.0.1", Port: 65536},
		"no host":                      {Proto: ProtoTCP, Host: "", Port: web.Port},
		"a name, not an address":       {Proto: ProtoTCP, Host: "localhost", Port: web.Port},
		"an address with a zone":       {Proto: ProtoTCP, Host: "fe80::1%eth0", Port: web.Port},
		"a negative timeout":           {Proto: ProtoTCP, Host: "127.0.0.1", Port: web.Port, Timeout: Duration(-time.Second)},
		"an unknown protocol":          {Proto: "gopher", Host: "127.0.0.1", Port: 70},
		"0.0.0.0, which is this node":  {Proto: ProtoTCP, Host: "0.0.0.0", Port: web.Port},
		":: , which is this node":      {Proto: ProtoTCP, Host: "::", Port: web.Port},
		"a mapped 0.0.0.0":             {Proto: ProtoTCP, Host: "::ffff:0.0.0.0", Port: web.Port},
		"a path with a query":          {Proto: ProtoHTTP, Host: "127.0.0.1", Port: web.Port, Path: "/health?ready=1"},
		"a path with a fragment":       {Proto: ProtoHTTP, Host: "127.0.0.1", Port: web.Port, Path: "/a#b"},
		"a path with an escape":        {Proto: ProtoHTTP, Host: "127.0.0.1", Port: web.Port, Path: "/a%20b"},
	}
	for name, spec := range bad {
		if res, err := c.Probe(ctx, spec); err == nil {
			t.Errorf("%s: probe ran: %+v", name, res)
		}
		if res := Probe(ctx, spec); res.Outcome != Error || !strings.Contains(res.Detail, "bad probe spec") {
			t.Errorf("%s: Probe = %+v, want an Error outcome naming the spec", name, res)
		}
	}
	// A path that only looks like an address stays a path.
	res, err := c.Probe(ctx, ProbeSpec{Proto: ProtoHTTP, Host: "127.0.0.1", Port: freePort(t), Path: "/@127.0.0.1:" + p + "/"})
	if err != nil || res.Outcome != Refused {
		t.Errorf("a closed port with an address in its path = %+v, %v; want refused", res, err)
	}
}

func TestClassifyDNSTimeoutIsNotATimeout(t *testing.T) {
	err := &net.OpError{Op: "dial", Net: "tcp", Err: &net.DNSError{Err: "i/o timeout", Name: "db.example", IsTimeout: true}}
	if got := classify(err); got != Error {
		t.Errorf("a lookup that timed out = %s, want error: only an unanswered connection is a timeout", got)
	}
}

// Changing a port's protocol straight after opening it reopens the port
// at once. Before the fix this failed in about 4 of 5 rounds.
func TestProtocolChangeRightAfterOpening(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	port := freePort(t)
	for i := range 300 {
		for _, proto := range []string{ProtoHTTP, ProtoTCP} {
			if _, _, err := c.SetListeners(ctx, []Listener{local(proto, port)}); err != nil {
				t.Fatalf("round %d, %s: %v", i, proto, err)
			}
		}
	}
}

func TestGeneration(t *testing.T) {
	ctx := context.Background()
	c := session(t, newAgent(t, "server"))
	want := []Listener{local(ProtoTCP, freePort(t))}
	_, g1, err := c.SetListeners(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	if _, g2, _ := c.SetListeners(ctx, want); g2 != g1 {
		t.Errorf("declaring the same set moved the generation: %d to %d", g1, g2)
	}
	if _, g3, _ := c.Listeners(ctx); g3 != g1 {
		t.Errorf("listing moved the generation: %d to %d", g1, g3)
	}
	if _, g4, _ := c.SetListeners(ctx, nil); g4 == g1 {
		t.Errorf("closing a listener didn't move the generation (%d)", g1)
	}
}

// A request with a bad field gets its error under its own ID, so its
// caller isn't left waiting.
func TestBadFieldKeepsItsID(t *testing.T) {
	a := newAgent(t, "n")
	toAgent, fromClient := io.Pipe()
	fromAgent, toClient := io.Pipe()
	go func() { _ = a.Serve(context.Background(), toAgent, toClient) }()
	defer func() { _ = fromClient.Close() }()
	if _, err := io.WriteString(fromClient, `{"id":7,"op":"probe","probe":{"proto":"tcp","host":"127.0.0.1","port":1,"timeout":"soon"}}`+"\n"); err != nil {
		t.Fatal(err)
	}
	var resp Response
	if err := json.NewDecoder(fromAgent).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != 7 || !strings.Contains(resp.Error, "bad request") {
		t.Errorf("response = %+v, want id 7 with a bad-request error", resp)
	}
}

// A second agent on the same socket would orphan the first one's
// listeners, so it's refused.
func TestRunRefusesALiveSocket(t *testing.T) {
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	socket := socketPath(t)
	go func() { _ = Run(ctx, New("first"), socket, nil, io.Discard) }()
	c, _ := connect(t, socket, 5*time.Second)
	if _, err := c.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	second := make(chan error, 1)
	sctx, sstop := context.WithCancel(ctx)
	defer sstop()
	go func() { second <- Run(sctx, New("second"), socket, nil, io.Discard) }()
	select {
	case err := <-second:
		if err == nil || !strings.Contains(err.Error(), "another agent") {
			t.Errorf("a second agent on a live socket: %v", err)
		}
	case <-time.After(5 * time.Second):
		sstop()
		t.Fatal("a second agent started on a live socket")
	}
	if h, err := c.Hello(ctx); err != nil || h.Node != "first" {
		t.Errorf("the first agent after the attempt: %+v, %v", h, err)
	}
}

// The agent makes the socket's directory private when it creates it, and
// never changes one it didn't: it refuses one others can enter, such as
// /tmp.
func TestSocketDirectory(t *testing.T) {
	t.Run("created private", func(t *testing.T) {
		ctx, stop := context.WithCancel(context.Background())
		socket := socketPath(t)
		ran := make(chan error, 1)
		go func() { ran <- Run(ctx, New("n"), socket, nil, io.Discard) }()
		c, _ := connect(t, socket, 5*time.Second)
		if _, err := c.Hello(ctx); err != nil {
			t.Fatal(err)
		}
		for _, p := range []struct {
			path string
			want os.FileMode
		}{{filepath.Dir(socket), 0o700}, {socket, 0o600}} {
			fi, err := os.Stat(p.path)
			if err != nil {
				t.Fatal(err)
			}
			if got := fi.Mode().Perm(); got != p.want {
				t.Errorf("%s: mode %o, want %o", p.path, got, p.want)
			}
		}
		stop()
		<-ran
	})
	t.Run("an open one refused, and left alone", func(t *testing.T) {
		socket := socketPath(t)
		dir := filepath.Dir(socket)
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: open on purpose; the agent must refuse it
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o777|os.ModeSticky); err != nil {
			t.Fatal(err)
		}
		err := Run(context.Background(), New("n"), socket, nil, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "can be entered by others") {
			t.Errorf("Run in an open directory: %v", err)
		}
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode()&(os.ModePerm|os.ModeSticky) != 0o777|os.ModeSticky {
			t.Errorf("the directory was changed: %v", fi.Mode())
		}
	})
}

// The SIGTERM path end to end: the agent runs as its own process, as in a
// node, and gets a real SIGTERM, as docker-init passes on. Its command dies
// of it, and the agent exits 128+15.
func TestMainStopsOnSIGTERM(t *testing.T) {
	if os.Getenv("TAILSUITE_AGENT_CHILD") != "" {
		var args []string
		if err := json.Unmarshal([]byte(os.Getenv("TAILSUITE_AGENT_CHILD")), &args); err != nil {
			os.Exit(99)
		}
		os.Exit(Main(args, os.Stdin, os.Stdout, os.Stderr))
	}
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "pid")
	socket := socketPath(t)
	args := []string{"run", "--node", "n", "--socket", socket, "--", "sh", "-c", "echo $$ > " + pidFile + "; exec sleep 60"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMainStopsOnSIGTERM$") //nolint:gosec // G204: it runs this test binary itself
	encoded, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Env = append(os.Environ(), "TAILSUITE_AGENT_CHILD="+string(encoded))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	var pid int
	deadline := time.Now().Add(10 * time.Second)
	for pid == 0 && time.Now().Before(deadline) {
		if b, err := os.ReadFile(pidFile); err == nil {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if pid == 0 {
		_ = cmd.Process.Kill()
		t.Fatal("the command never started")
	}
	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	err = cmd.Wait()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 143 {
		t.Errorf("the agent exited with %v, want 143", err)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("the command (pid %d) outlived the agent: %v", pid, err)
	}
}

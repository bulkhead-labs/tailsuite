package agent

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// bannerPrefix starts the line a tailsuite TCP listener sends on accept,
// and the body an HTTP listener returns. The node's name follows it.
const bannerPrefix = "tailsuite-agent "

// nodeHeader carries the node's name in an HTTP listener's response.
const nodeHeader = "X-Tailsuite-Node"

// bannerWait bounds how long a TCP probe waits for a banner after
// connecting. A listener that isn't tailsuite's sends none, and the probe
// is still a success.
const bannerWait = 500 * time.Millisecond

// CheckProbeSpec checks a probe spec. A malformed spec is the host's
// mistake, not something the lab saw, so the agent refuses it as an error
// and never reports it as an outcome.
func CheckProbeSpec(spec ProbeSpec) error {
	switch spec.Proto {
	case ProtoTCP, ProtoHTTP:
	default:
		return fmt.Errorf("probe: unknown protocol %q", spec.Proto)
	}
	// An IP literal: a name would add a lookup, and a resolver that doesn't
	// answer would look like a drop.
	addr, err := netip.ParseAddr(spec.Host)
	if err != nil || addr.Zone() != "" {
		return fmt.Errorf("probe: host %q isn't an IP address", spec.Host)
	}
	// 0.0.0.0 and :: connect to the prober's own node, as an empty host
	// would.
	if addr.Unmap().IsUnspecified() {
		return fmt.Errorf("probe: host %q is the unspecified address", spec.Host)
	}
	if spec.Port < 1 || spec.Port > 65535 {
		return fmt.Errorf("probe: port %d out of range", spec.Port)
	}
	if spec.Path != "" && !strings.HasPrefix(spec.Path, "/") {
		return fmt.Errorf("probe: path %q doesn't start with /", spec.Path)
	}
	// The path is sent as written, a path and nothing else: a query, a
	// fragment or an escape would be sent escaped, which isn't the request
	// the spec describes.
	if strings.ContainsAny(spec.Path, "?#%") {
		return fmt.Errorf("probe: path %q has a query, fragment or escape; want a plain path", spec.Path)
	}
	if spec.Timeout < 0 {
		return errors.New("probe: negative timeout")
	}
	return nil
}

// Probe runs one probe and reports what it saw. It never returns an
// error: everything the lab can do is an outcome, and a spec that fails
// CheckProbeSpec is an Error outcome that runs nothing.
func Probe(ctx context.Context, spec ProbeSpec) ProbeResult {
	if err := CheckProbeSpec(spec); err != nil {
		return ProbeResult{Outcome: Error, Detail: "bad probe spec: " + err.Error()}
	}
	timeout := time.Duration(spec.Timeout)
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	start := time.Now()
	var res ProbeResult
	if spec.Proto == ProtoHTTP {
		res = probeHTTP(ctx, spec)
	} else {
		res = probeTCP(ctx, spec)
	}
	res.Total = Duration(time.Since(start))
	return res
}

func target(spec ProbeSpec) string {
	return net.JoinHostPort(spec.Host, strconv.Itoa(spec.Port))
}

func probeTCP(ctx context.Context, spec ProbeSpec) ProbeResult {
	start := time.Now()
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", target(spec))
	if err != nil {
		return failed(err)
	}
	defer func() { _ = conn.Close() }()
	res := ProbeResult{Outcome: Success, Connected: true, Connect: Duration(time.Since(start))}

	// Read the banner, if the listener is tailsuite's.
	deadline := time.Now().Add(bannerWait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	line, err := bufio.NewReader(io.LimitReader(conn, 256)).ReadString('\n')
	if err == nil {
		if name, ok := strings.CutPrefix(strings.TrimSpace(line), bannerPrefix); ok {
			res.Peer = name
		}
	}
	return res
}

func probeHTTP(ctx context.Context, spec ProbeSpec) ProbeResult {
	path := spec.Path
	if path == "" {
		path = "/"
	}
	// The transport dials on a goroutine of its own, which can outlive
	// Do when the context ends.
	var (
		mu        sync.Mutex
		connected bool
		connect   time.Duration
	)
	dialed := func() (bool, Duration) {
		mu.Lock()
		defer mu.Unlock()
		return connected, Duration(connect)
	}
	start := time.Now()
	dialer := &net.Dialer{}
	transport := &http.Transport{
		Proxy: nil, // never through a proxy: the probe is the traffic under test
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err == nil {
				mu.Lock()
				connected, connect = true, time.Since(start)
				mu.Unlock()
			}
			return conn, err
		},
		DisableKeepAlives: true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		// A redirect is an answer; following it would probe somewhere else.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	// Built from parts, so nothing in the path can change the host or port.
	u := &url.URL{Scheme: "http", Host: target(spec), Path: path}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return ProbeResult{Outcome: Error, Detail: err.Error()}
	}
	if req.URL.Host != target(spec) || req.URL.User != nil {
		return ProbeResult{Outcome: Error, Detail: fmt.Sprintf("probe: the request would go to %q, not %q", req.URL.Host, target(spec))}
	}
	resp, err := client.Do(req)
	if err != nil {
		f := failed(err)
		f.Connected, f.Connect = dialed()
		if f.Connected && f.Outcome == Timeout {
			f.Detail = "connected, but no response: " + f.Detail
		}
		return f
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	res := ProbeResult{Outcome: Success, HTTPStatus: resp.StatusCode, Peer: resp.Header.Get(nodeHeader)}
	res.Connected, res.Connect = dialed()
	return res
}

// failed turns a dial or request error into its outcome.
func failed(err error) ProbeResult {
	return ProbeResult{Outcome: classify(err), Detail: err.Error()}
}

// classify tells a refusal from a timeout from anything else. A failed
// lookup is never a timeout, even one that timed out: only a connection
// that got no answer is.
func classify(err error) Outcome {
	var netErr net.Error
	var dnsErr *net.DNSError
	switch {
	case err == nil:
		return Success
	case errors.As(err, &dnsErr):
		return Error
	case errors.Is(err, syscall.ECONNREFUSED):
		return Refused
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return Timeout
	default:
		return Error
	}
}

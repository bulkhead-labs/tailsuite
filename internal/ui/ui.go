// Package ui is the local UI (decision 0017): a map of the lab, drawn from
// the policy at once and filled in by real results, served from the
// binary on localhost only, behind a one-time link.
package ui

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed assets
var assets embed.FS

// Headers the page sends.
const (
	headerLink    = "X-Tailsuite-Link"
	headerSession = "X-Tailsuite-Session"
)

// csp is the Content-Security-Policy on every response (0017).
const csp = "default-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// pollEvery is how often the results file is checked for a new run.
const pollEvery = 500 * time.Millisecond

// Options configure the server.
type Options struct {
	// View is the map drawn before any traffic.
	View View
	// Results is the file `tailsuite test` writes the last run's JSON to
	// (the same JSON as --json). The page shows each new run.
	Results string
	// now is the clock, for tests.
	now func() time.Time
}

// Server is a running UI.
type Server struct {
	opts  Options
	auth  *auth
	port  int
	hosts map[string]bool // Host headers accepted
	orig  map[string]bool // Origin headers accepted
	srv   *http.Server
	lns   []net.Listener
	wg    sync.WaitGroup
	files http.Handler
	// started is when the UI started; results older than it aren't shown.
	started time.Time
	// stop ends open streams, so Close doesn't wait on them.
	stop context.CancelFunc
}

// Start serves the UI on 127.0.0.1 and ::1, on one random free port. ::1
// is skipped where the host has no IPv6.
func Start(opts Options) (*Server, error) {
	if opts.now == nil {
		opts.now = time.Now
	}
	root, err := fs.Sub(assets, "assets")
	if err != nil {
		return nil, err
	}
	s := &Server{opts: opts, auth: newAuth(opts.now), files: http.FileServerFS(root), started: opts.now()}
	if err := s.listen(); err != nil {
		return nil, err
	}
	s.hosts, s.orig = map[string]bool{}, map[string]bool{}
	for _, h := range []string{"127.0.0.1", "[::1]", "localhost"} {
		hp := h + ":" + strconv.Itoa(s.port)
		s.hosts[hp] = true
		s.orig["http://"+hp] = true
	}
	base, stop := context.WithCancel(context.Background())
	s.stop = stop
	s.srv = &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second, BaseContext: func(net.Listener) context.Context { return base }}
	for _, ln := range s.lns {
		s.wg.Go(func() { _ = s.srv.Serve(ln) })
	}
	return s, nil
}

func (s *Server) listen() error {
	var lastErr error
	var lc net.ListenConfig
	ctx := context.Background()
	for range 5 {
		v4, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
		if err != nil {
			return fmt.Errorf("ui: %w", err)
		}
		port := v4.Addr().(*net.TCPAddr).Port
		v6, err := lc.Listen(ctx, "tcp", net.JoinHostPort("::1", strconv.Itoa(port)))
		if err == nil {
			s.port, s.lns = port, []net.Listener{v4, v6}
			return nil
		}
		if noIPv6(err) {
			s.port, s.lns = port, []net.Listener{v4}
			return nil
		}
		_ = v4.Close()
		lastErr = err // the port is taken on ::1: try another
	}
	return fmt.Errorf("ui: no free port on both 127.0.0.1 and ::1: %w", lastErr)
}

// noIPv6 says a listen failed because the host can't do IPv6 loopback.
func noIPv6(err error) bool {
	var se *os.SyscallError
	if errors.As(err, &se) {
		return se.Syscall == "socket" || errors.Is(se.Err, errAddrNotAvail)
	}
	return false
}

// Close stops serving. Open streams end.
func (s *Server) Close() error {
	s.stop()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := s.srv.Shutdown(ctx)
	if errors.Is(err, context.DeadlineExceeded) {
		err = s.srv.Close()
	}
	s.wg.Wait()
	return err
}

// Port is the port the UI serves on.
func (s *Server) Port() int { return s.port }

// NewLink makes a fresh one-time link, and returns it with a channel
// that's closed once a page trades it.
func (s *Server) NewLink() (string, <-chan struct{}, error) {
	secret, traded, err := s.auth.newLink()
	if err != nil {
		return "", nil, err
	}
	return "http://127.0.0.1:" + strconv.Itoa(s.port) + "/#" + secret, traded, nil
}

// ServeHTTP checks every request's Host, then routes it.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Against DNS rebinding: only the localhost names, on our port.
	if !s.hosts[r.Host] {
		http.Error(w, "tailsuite serves only 127.0.0.1, [::1] and localhost", http.StatusMisdirectedRequest)
		return
	}
	switch r.URL.Path {
	case "/api/session":
		s.session(w, r)
	case "/api/events":
		s.events(w, r)
	default:
		if len(r.URL.Path) >= 5 && r.URL.Path[:5] == "/api/" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		// No directory listings.
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		// The shell, scripts and styles hold no tailnet data (0017).
		h.Set("Cache-Control", "no-cache")
		s.files.ServeHTTP(w, r)
	}
}

// sameOrigin is 0017's Origin check, for every API request.
func (s *Server) sameOrigin(r *http.Request) bool {
	if o := r.Header.Get("Origin"); o != "" {
		return s.orig[o]
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

func apiError(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

// session trades a link's secret for a session secret: the one request
// without a session, and it still needs the Origin check.
func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		apiError(w, http.StatusMethodNotAllowed, errors.New("POST only"))
		return
	}
	if !s.sameOrigin(r) {
		apiError(w, http.StatusForbidden, errors.New("cross-origin request refused"))
		return
	}
	sess, err := s.auth.trade(r.Header.Get(headerLink))
	if err != nil {
		apiError(w, http.StatusUnauthorized, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]string{"session": sess})
}

// authorized checks an API request's Origin and session.
func (s *Server) authorized(w http.ResponseWriter, r *http.Request) bool {
	if !s.sameOrigin(r) {
		apiError(w, http.StatusForbidden, errors.New("cross-origin request refused"))
		return false
	}
	if !s.auth.valid(r.Header.Get(headerSession)) {
		apiError(w, http.StatusUnauthorized, errNoSession)
		return false
	}
	return true
}

// events streams Server-Sent Events: the view once, then each run's
// results as `tailsuite test` writes them. The session is checked again
// before each event, so a session ended by a reused link stops streaming.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		apiError(w, http.StatusMethodNotAllowed, errors.New("GET only"))
		return
	}
	if !s.authorized(w, r) {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		apiError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	session := r.Header.Get(headerSession)
	send := func(event string, v any) bool {
		if !s.auth.valid(session) {
			_, _ = fmt.Fprintf(w, "event: ended\ndata: %q\n\n", errReused.Error())
			flusher.Flush()
			return false
		}
		b, err := json.Marshal(v)
		if err != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if !send("view", s.opts.View) {
		return
	}
	var last []byte
	started := s.started
	tick := time.NewTicker(pollEvery)
	defer tick.Stop()
	for {
		// Only runs since the UI started: an older file is another lab's.
		if b, ok := s.results(started); ok && !bytes.Equal(b, last) {
			last = b
			if !send("results", json.RawMessage(b)) {
				return
			}
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if !s.auth.valid(session) {
				send("ended", nil)
				return
			}
		}
	}
}

// results reads the results file, if it's whole and was written at or
// after since.
func (s *Server) results(since time.Time) ([]byte, bool) {
	fi, err := os.Stat(s.opts.Results)
	if err != nil || fi.ModTime().Before(since.Truncate(time.Second)) {
		return nil, false
	}
	b, err := os.ReadFile(s.opts.Results)
	if err != nil || !json.Valid(b) {
		return nil, false
	}
	return bytes.TrimSpace(b), true
}

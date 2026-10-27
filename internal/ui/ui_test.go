package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func start(t *testing.T) (*Server, *clock, string) {
	t.Helper()
	c := &clock{t: time.Now()}
	results := filepath.Join(t.TempDir(), "results.json")
	s, err := Start(Options{View: View{Project: "synthetic"}, Results: results, now: c.now})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, c, results
}

func (s *Server) origin() string { return "http://127.0.0.1:" + strconv.Itoa(s.port) }

// reply is a response whose body do closes when the test ends.
type reply struct {
	StatusCode int
	Header     http.Header
	Body       io.Reader
}

func do(t *testing.T, s *Server, method, path string, hdr map[string]string) reply {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, s.origin()+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })
	return reply{StatusCode: res.StatusCode, Header: res.Header, Body: res.Body}
}

func secretOf(t *testing.T, link string) string {
	t.Helper()
	_, secret, ok := strings.Cut(link, "#")
	if !ok || len(secret) < 40 {
		t.Fatalf("link %q has no secret in its fragment", link)
	}
	return secret
}

func trade(t *testing.T, s *Server, secret string) (int, string) {
	t.Helper()
	res := do(t, s, "POST", "/api/session", map[string]string{"Origin": s.origin(), headerLink: secret})
	var body map[string]string
	_ = json.NewDecoder(res.Body).Decode(&body)
	return res.StatusCode, body["session"]
}

func TestTheShellNeedsNoSecretAndCarriesTheCSP(t *testing.T) {
	s, _, _ := start(t)
	for _, p := range []string{"/", "/app.mjs", "/vendor/preact.mjs", "/style.css", "/fonts/fonts.css"} {
		res := do(t, s, "GET", p, nil)
		if res.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d", p, res.StatusCode)
		}
		if got := res.Header.Get("Content-Security-Policy"); got != csp {
			t.Errorf("GET %s: CSP %q", p, got)
		}
	}
	b, _ := io.ReadAll(do(t, s, "GET", "/", nil).Body)
	if strings.Contains(string(b), "<script>") || strings.Contains(string(b), "style=") {
		t.Error("the shell has an inline script or style, which the CSP forbids")
	}
	if ct := do(t, s, "GET", "/app.mjs", nil).Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("app.mjs served as %q: a module script needs a JavaScript type", ct)
	}
}

func TestOnlyLocalhostHosts(t *testing.T) {
	s, _, _ := start(t)
	port := strconv.Itoa(s.port)
	for host, want := range map[string]int{
		"127.0.0.1:" + port: 200, "[::1]:" + port: 200, "localhost:" + port: 200,
		"evil.example:" + port: http.StatusMisdirectedRequest, "127.0.0.1:1": http.StatusMisdirectedRequest, "localhost": http.StatusMisdirectedRequest,
	} {
		if got := do(t, s, "GET", "/", map[string]string{"Host": host}).StatusCode; got != want {
			t.Errorf("Host %s: %d, want %d", host, got, want)
		}
	}
}

func TestTheLinkTradesOnce(t *testing.T) {
	s, _, _ := start(t)
	link, traded, err := s.NewLink()
	if err != nil {
		t.Fatal(err)
	}
	secret := secretOf(t, link)
	if code, _ := trade(t, s, "not-a-link"); code != 401 {
		t.Errorf("an unknown link: %d", code)
	}
	code, sess := trade(t, s, secret)
	if code != 200 || sess == "" {
		t.Fatalf("first trade: %d %q", code, sess)
	}
	select {
	case <-traded:
	default:
		t.Error("the traded channel didn't close")
	}
	if !s.auth.valid(sess) {
		t.Fatal("the session isn't valid")
	}
	// Traded again within its lifetime: the first session ends.
	if code, _ := trade(t, s, secret); code != 401 {
		t.Errorf("second trade: %d", code)
	}
	if s.auth.valid(sess) {
		t.Error("a reused link left its first session alive")
	}
	if code, _ := trade(t, s, secret); code != 401 {
		t.Errorf("third trade: %d", code)
	}
}

func TestAnUnusedLinkExpires(t *testing.T) {
	s, c, _ := start(t)
	link, _, _ := s.NewLink()
	c.add(LinkTTL + time.Second)
	if code, _ := trade(t, s, secretOf(t, link)); code != 401 {
		t.Errorf("an expired link: %d", code)
	}
	fresh, _, _ := s.NewLink()
	if code, _ := trade(t, s, secretOf(t, fresh)); code != 200 {
		t.Errorf("a fresh link: %d", code)
	}
}

func TestAPIChecksOriginAndSession(t *testing.T) {
	s, _, _ := start(t)
	link, _, _ := s.NewLink()
	_, sess := trade(t, s, secretOf(t, link))
	other := "http://127.0.0.1:" + strconv.Itoa(s.port+1)
	for _, tc := range []struct {
		name string
		hdr  map[string]string
		want int
	}{
		{"no session", map[string]string{"Origin": s.origin()}, 401},
		{"a wrong session", map[string]string{"Origin": s.origin(), headerSession: "x"}, 401},
		{"another port's origin", map[string]string{"Origin": other, headerSession: sess}, 403},
		{"cross-site, no origin", map[string]string{"Sec-Fetch-Site": "cross-site", headerSession: sess}, 403},
		{"neither origin nor fetch metadata", map[string]string{headerSession: sess}, 403},
		{"the link's secret as a session", map[string]string{"Origin": s.origin(), headerSession: secretOf(t, link)}, 401},
	} {
		if got := do(t, s, "GET", "/api/events", tc.hdr).StatusCode; got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
	link2, _, _ := s.NewLink()
	res := do(t, s, "POST", "/api/session", map[string]string{"Origin": other, headerLink: secretOf(t, link2)})
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("a trade from another origin: %d", res.StatusCode)
	}
	if code, _ := trade(t, s, secretOf(t, link2)); code != 200 {
		t.Errorf("a refused cross-origin trade used the link up: %d", code)
	}
}

// readEvent reads one Server-Sent Event.
func readEvent(t *testing.T, r io.Reader) (string, string) {
	t.Helper()
	var buf []byte
	one := make([]byte, 1)
	for !strings.HasSuffix(string(buf), "\n\n") {
		if _, err := r.Read(one); err != nil {
			t.Fatalf("reading an event: %v (so far %q)", err, buf)
		}
		buf = append(buf, one[0])
	}
	var event, data string
	for _, line := range strings.Split(strings.TrimSpace(string(buf)), "\n") {
		if v, ok := strings.CutPrefix(line, "event: "); ok {
			event = v
		}
		if v, ok := strings.CutPrefix(line, "data: "); ok {
			data = v
		}
	}
	return event, data
}

func TestEventsStreamTheViewThenNewResults(t *testing.T) {
	s, _, results := start(t)
	// Results from before the UI started are another lab's.
	if err := os.WriteFile(results, []byte(`{"version":"old"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(results, old, old); err != nil {
		t.Fatal(err)
	}
	link, _, _ := s.NewLink()
	_, sess := trade(t, s, secretOf(t, link))
	res := do(t, s, "GET", "/api/events", map[string]string{"Sec-Fetch-Site": "same-origin", headerSession: sess})
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("events: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	if ev, data := readEvent(t, res.Body); ev != "view" || !strings.Contains(data, `"project":"synthetic"`) {
		t.Fatalf("first event %s %s", ev, data)
	}
	if err := os.WriteFile(results, []byte("{\"version\":\"new\",\n\"exit\":0}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ev, data := readEvent(t, res.Body)
	if ev != "results" || !strings.Contains(data, `"new"`) || strings.Contains(data, "\n") {
		t.Fatalf("second event %s %q", ev, data)
	}
	// A reused link ends the session, and its stream.
	trade(t, s, secretOf(t, link))
	if ev, _ := readEvent(t, res.Body); ev != "ended" {
		t.Errorf("after the session ended: %s", ev)
	}
}

func TestNoDirectoryListings(t *testing.T) {
	s, _, _ := start(t)
	for _, p := range []string{"/vendor/", "/fonts/"} {
		if got := do(t, s, "GET", p, nil).StatusCode; got != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", p, got)
		}
	}
}

func TestCloseEndsOpenStreams(t *testing.T) {
	s, _, _ := start(t)
	link, _, _ := s.NewLink()
	_, sess := trade(t, s, secretOf(t, link))
	res := do(t, s, "GET", "/api/events", map[string]string{"Sec-Fetch-Site": "same-origin", headerSession: sess})
	readEvent(t, res.Body)
	began := time.Now()
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if d := time.Since(began); d > time.Second {
		t.Errorf("Close took %v with a stream open", d)
	}
}

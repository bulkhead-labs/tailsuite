//go:build integration && unix

package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/runner"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
)

// The CLI on a real headscale lab, from init to down, with the node agent
// embedded:
//
//	go generate ./internal/agentbin && go test -tags integration ./cmd/tailsuite/

func TestInitUpTestDown(t *testing.T) {
	if len(agentbin.Embedded()) == 0 {
		t.Fatal(agentbin.ErrNotEmbedded)
	}
	dir := initProject(t)
	t.Cleanup(func() {
		if st, ok, _ := readState(dir); ok {
			runCmd(t, "down", "--run", st.Run)
		}
	})

	// With no lab up, test starts one of its own and tears it down.
	code, out, errOut := runCmd(t, "test", "--dir", dir, "--json")
	if code != 0 {
		t.Fatalf("test with no lab up: exit %d\n%s\n%s", code, out, errOut)
	}
	own := results(t, out)
	if own.Summary.Pass == 0 || own.Summary.Fail != 0 {
		t.Errorf("the template's suite: %+v", own.Summary)
	}
	if _, ok, _ := readState(dir); ok {
		t.Error("test's own lab left a lab state")
	}

	stop, upOut := startUp(t, dir)
	st, ok, err := readState(dir)
	if !ok || err != nil {
		t.Fatalf("up left no lab state: %v", err)
	}
	if code, _, errOut := runCmd(t, "up", "--dir", dir); code != exitCouldNotRun || !strings.Contains(errOut, "already up") {
		t.Errorf("a second up: exit %d, %q", code, errOut)
	}

	// On the lab up left, the same results.
	code, out, errOut = runCmd(t, "test", "--dir", dir, "--json")
	if code != 0 {
		t.Fatalf("test on the lab: exit %d\n%s", code, errOut)
	}
	if strings.Contains(errOut, "starting one") {
		t.Fatalf("test started its own lab with up's running: %s", errOut)
	}
	// up serves the local UI: the map, then that run's results (#11).
	checkUI(t, upOut.String())
	if got := results(t, out); got.Summary != own.Summary {
		t.Errorf("on the lab that's up: %+v; on its own lab: %+v", got.Summary, own.Summary)
	}
	if code, _, _ := runCmd(t, "test", "--dir", dir, "--strict"); code != 2 {
		t.Errorf("test --strict with unproven results: exit %d, want 2", code)
	}

	// A device whose tags change needs a new lab: its node keeps the tags
	// it joined with.
	tfvars := filepath.Join(dir, "tofu", "tailnet.auto.tfvars")
	tv, err := os.ReadFile(tfvars)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tfvars, []byte(strings.Replace(string(tv), `["tag:prod"]`, `["tag:prod", "tag:db"]`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, errOut := runCmd(t, "test", "--dir", dir); code != exitCouldNotRun || !strings.Contains(errOut, "changed a device") || out != "" {
		t.Errorf("test after a device's tags changed: exit %d, %q, %q", code, errOut, out)
	}
	if err := os.WriteFile(tfvars, tv, 0o600); err != nil {
		t.Fatal(err)
	}

	// A lab started by another build: its agent and images may differ.
	other := st
	other.Build.Agent = strings.Repeat("0", 64)
	if err := writeState(dir, other); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "test", "--dir", dir); code != exitCouldNotRun || !strings.Contains(errOut, "another tailsuite build") {
		t.Errorf("test on another build's lab: exit %d, %q", code, errOut)
	}
	if err := writeState(dir, st); err != nil {
		t.Fatal(err)
	}

	// A policy applied by another process during a run unproves it.
	stillHolds(t, st)

	// A policy changed after up reaches the lab before any probe: with
	// engineers' grant gone, their test fails.
	pol := filepath.Join(dir, "policy.hujson")
	b, err := os.ReadFile(pol)
	if err != nil {
		t.Fatal(err)
	}
	grant := `{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:80"]},`
	if !strings.Contains(string(b), grant) {
		t.Fatalf("the template's policy has no %s", grant)
	}
	if err := os.WriteFile(pol, []byte(strings.Replace(string(b), grant, "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = runCmd(t, "test", "--dir", dir, "--json", "--run", "engineers reach the web tier")
	if code != 1 {
		t.Errorf("test after the grant was removed: exit %d, want 1\n%s", code, out)
	}
	if got := results(t, out); got.Summary.Fail == 0 {
		t.Errorf("no FAIL after the grant was removed: %+v", got.Summary)
	}

	// Ctrl-C tears up's lab down.
	if code := stop(); code != 0 {
		t.Fatalf("up after Ctrl-C: exit %d", code)
	}
	if _, ok, _ := readState(dir); ok {
		t.Error("up left the lab state")
	}
	if alive, err := runAlive(context.Background(), st.Run); alive || err != nil {
		t.Errorf("run %s after up stopped: alive %v, %v", st.Run, alive, err)
	}
	if code, out, _ := runCmd(t, "down", "--dir", dir); code != 0 || !strings.Contains(out, "No lab is up") {
		t.Errorf("a second down: exit %d, %q", code, out)
	}

	// A state left by a lab that's gone: test says so, and starts its own.
	if err := writeState(dir, st); err != nil {
		t.Fatal(err)
	}
	code, _, errOut = runCmd(t, "test", "--dir", dir, "--run", "the web tier reaches the database")
	if code != 0 || !strings.Contains(errOut, "is gone") || !strings.Contains(errOut, "starting one") {
		t.Errorf("test with a gone lab's state: exit %d, %q", code, errOut)
	}
	if _, ok, _ := readState(dir); ok {
		t.Error("the gone lab's state is still there")
	}
}

// stillHolds attaches to the lab twice, as two test processes would, and
// checks the first sees the second's apply.
func stillHolds(t *testing.T, st labState) {
	t.Helper()
	ctx := context.Background()
	open := func() *headscale.Lab {
		sb, err := docker.Attach(ctx, st.Run)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = sb.Close() })
		lab, err := headscale.New(headscale.Config{Sandbox: sb}).Attach(ctx, sb.Nodes(), planNames(st.Devices))
		if err != nil {
			t.Fatal(err)
		}
		return lab
	}
	first, second := open(), open()
	pol := []byte(`{"grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`)
	if _, err := first.Apply(ctx, pol); err != nil {
		t.Fatal(err)
	}
	if err := first.StillHolds(ctx); err != nil {
		t.Fatalf("right after its own apply: %v", err)
	}
	if _, err := second.Apply(ctx, pol); err != nil {
		t.Fatal(err)
	}
	if err := first.StillHolds(ctx); err == nil {
		t.Error("StillHolds didn't see another process's apply")
	}
}

func results(t *testing.T, out string) runner.Run {
	t.Helper()
	var r runner.Run
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("results: %v\n%s", err, out)
	}
	return r
}

// syncBuf is a buffer up writes to while the test reads it.
type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// startUp runs up in the background until its lab is up, and returns what
// stops it, as Ctrl-C does, with up's exit code.
func startUp(t *testing.T, dir string) (stop func() int, out *syncBuf) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	out = &syncBuf{}
	var errOut syncBuf
	done := make(chan int, 1)
	go func() { done <- run(ctx, []string{"up", "--dir", dir, "--no-browser"}, out, &errOut) }()
	code, stopped := 0, false
	stop = func() int {
		if !stopped {
			cancel()
			code, stopped = <-done, true
		}
		return code
	}
	t.Cleanup(func() { stop() })
	deadline := time.After(3 * time.Minute)
	for !strings.Contains(out.String(), "The lab is up") {
		select {
		case c := <-done:
			stopped, code = true, c
			t.Fatalf("up: exit %d: %s", c, errOut.String())
		case <-deadline:
			t.Fatalf("up didn't start in 3 minutes: %s", errOut.String())
		case <-time.After(200 * time.Millisecond):
		}
	}
	return stop, out
}

// up --detach and --clean, and down ending a detached lab (#81).
func TestDetachAndClean(t *testing.T) {
	if len(agentbin.Embedded()) == 0 {
		t.Fatal(agentbin.ErrNotEmbedded)
	}
	t.Setenv(asMain, "1") // the background process is this binary
	t.Setenv("CI", "")    // a browser is allowed, and faked: this up runs in-process
	var opened []string
	old := openBrowser
	openBrowser = func(_ context.Context, target string) error { opened = append(opened, target); return nil }
	t.Cleanup(func() { openBrowser = old })
	ctx := context.Background()
	dir := initProject(t)
	abs, _ := projectDir(dir)
	t.Cleanup(func() {
		if st, ok, _ := readState(dir); ok {
			runCmd(t, "down", "--run", st.Run)
		}
	})

	// A leftover of this project, as a crashed up would leave, with no
	// state file pointing at it.
	left, err := docker.NewFor(ctx, projectID(abs))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = left.Teardown(context.Background()); _ = left.Close() })
	if _, err := left.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "leftover"}); err != nil {
		t.Fatal(err)
	}
	// And another project's lab, which --clean must not touch.
	other, err := docker.NewFor(ctx, projectID("/elsewhere"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Teardown(context.Background()); _ = other.Close() })
	if _, err := other.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "theirs"}); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runCmd(t, "up", "--dir", dir, "--detach", "--clean")
	if code != 0 || !strings.Contains(out, "The lab is up") || !strings.Contains(out, "Held by pid") {
		t.Fatalf("up --detach --clean: exit %d\n%s\n%s", code, out, errOut)
	}
	if alive, _ := runAlive(ctx, left.RunID()); alive {
		t.Error("--clean left this project's leftover")
	}
	if alive, _ := runAlive(ctx, other.RunID()); !alive {
		t.Error("--clean tore down another project's lab")
	}
	st, ok, _ := readState(dir)
	if !ok || !st.Ready || st.Holder == 0 || st.Holder == os.Getpid() {
		t.Fatalf("state after a detached up: %+v", st)
	}
	if err := syscall.Kill(st.Holder, 0); err != nil {
		t.Fatalf("the holder (pid %d) isn't running: %v", st.Holder, err)
	}
	if fi, err := os.Stat(st.Redirect); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("the holder's redirect file to the UI: %v %v", fi, err)
	}
	if len(opened) != 1 || opened[0] != st.Redirect {
		t.Errorf("up --detach opened %q, want the holder's redirect file %s", opened, st.Redirect)
	}
	if !uiLink.MatchString(out) {
		t.Errorf("up --detach printed no link to the local UI:\n%s", out)
	}

	// test uses the detached lab.
	code, _, errOut = runCmd(t, "test", "--dir", dir, "--run", "the web tier reaches the database")
	if code != 0 || strings.Contains(errOut, "starting one") {
		t.Errorf("test on the detached lab: exit %d, %q", code, errOut)
	}

	// --clean over a detached lab replaces it, and the new up's output
	// is its own: the old holder, exiting, writes into its old log only.
	code, out, errOut = runCmd(t, "up", "--dir", dir, "--detach", "--clean")
	if code != 0 || strings.Contains(out, st.Run) || strings.ContainsRune(out, 0) {
		t.Fatalf("a second up --detach --clean: exit %d\n%q\n%s", code, out, errOut)
	}
	waitExit(t, st.Holder)
	if alive, _ := runAlive(ctx, st.Run); alive {
		t.Error("--clean left the earlier detached lab")
	}
	st, _, _ = readState(dir)

	// down ends it, and the holder notices and exits.
	if code, _, errOut := runCmd(t, "down", "--dir", dir); code != 0 {
		t.Fatalf("down: exit %d: %s", code, errOut)
	}
	waitExit(t, st.Holder)
	if alive, _ := runAlive(ctx, st.Run); alive {
		t.Error("the detached lab is still up after down")
	}
	if log, _ := os.ReadFile(logPath(dir, st.Token)); !strings.Contains(string(log), "torn down from elsewhere") {
		t.Errorf("the holder's log:\n%s", log)
	}

	// lab.yaml's up: section sets the default.
	lab := filepath.Join(dir, "lab.yaml")
	b, _ := os.ReadFile(lab)
	if err := os.WriteFile(lab, append(b, []byte("up: { detach: true }\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	// ...and --no-redirect-file opens the link itself, with no redirect file.
	if code, out, errOut := runCmd(t, "up", "--dir", dir, "--no-redirect-file"); code != 0 || !strings.Contains(out, "Held by pid") {
		t.Fatalf("up with up.detach in lab.yaml: exit %d\n%s\n%s", code, out, errOut)
	}
	st2, _, _ := readState(dir)
	if last := opened[len(opened)-1]; !uiLink.MatchString(last) || st2.Redirect != "" {
		t.Errorf("detached --no-redirect-file: opened %q, redirect %q", last, st2.Redirect)
	}
	if code, _, errOut := runCmd(t, "down", "--dir", dir); code != 0 {
		t.Fatalf("down: exit %d: %s", code, errOut)
	}
	waitExit(t, st2.Holder)
}

// waitExit waits for a holder to exit, for at most twice its poll and a
// teardown's bound.
func waitExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2*holdPoll + time.Minute)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("the holder (pid %d) didn't exit after down", pid)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// checkUI trades up's one-time link for a session, as the page does, and
// reads the stream: the map of the lab, then the last run's results.
func checkUI(t *testing.T, upOut string) {
	t.Helper()
	link := uiLink.FindString(upOut)
	if link == "" {
		t.Fatalf("up printed no link to the local UI:\n%s", upOut)
	}
	base, secret, _ := strings.Cut(link, "/#")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/session", nil)
	req.Header.Set("Origin", base)
	req.Header.Set("X-Tailsuite-Link", secret)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var sess struct{ Session string }
	_ = json.NewDecoder(res.Body).Decode(&sess)
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK || sess.Session == "" {
		t.Fatalf("trading up's link: %d", res.StatusCode)
	}
	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/events", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("X-Tailsuite-Session", sess.Session)
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	var event string
	seen := map[string]string{}
	for sc.Scan() && len(seen) < 2 {
		line := sc.Text()
		if e, ok := strings.CutPrefix(line, "event: "); ok {
			event = e
		}
		if d, ok := strings.CutPrefix(line, "data: "); ok {
			seen[event] = d
		}
	}
	if !strings.Contains(seen["view"], `"laptop"`) || !strings.Contains(seen["results"], `"summary"`) {
		t.Errorf("the UI's stream: view %.80q, results %.80q", seen["view"], seen["results"])
	}
}

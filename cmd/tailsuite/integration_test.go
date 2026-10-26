//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/runner"
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

	if code, _, errOut := runCmd(t, "up", "--dir", dir); code != 0 {
		t.Fatalf("up: exit %d: %s", code, errOut)
	}
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

	if code, _, errOut := runCmd(t, "down", "--dir", dir); code != 0 {
		t.Fatalf("down: exit %d: %s", code, errOut)
	}
	if _, ok, _ := readState(dir); ok {
		t.Error("down left the lab state")
	}
	if alive, err := runAlive(context.Background(), st.Run); alive || err != nil {
		t.Errorf("run %s after down: alive %v, %v", st.Run, alive, err)
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

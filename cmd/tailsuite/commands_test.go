package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/runner"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

func runCmd(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// initProject writes the startup template into a new folder.
func initProject(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my-tailnet")
	if code, _, errOut := runCmd(t, "init", "--template", "startup", dir); code != 0 {
		t.Fatalf("init: exit %d: %s", code, errOut)
	}
	return dir
}

func TestInitWritesAProjectThatLoadsAndPlans(t *testing.T) {
	dir := initProject(t)
	for _, f := range []string{"policy.hujson", "lab.yaml", "tests/startup.yaml", "tofu/tailnet.auto.tfvars", ".gitignore"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("init didn't write %s: %v", f, err)
		}
	}
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if !strings.Contains(string(gi), "/.tailsuite/") {
		t.Errorf(".gitignore = %q, want it to ignore .tailsuite/", gi)
	}
	p, o, err := load(dir, suite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	e := headscale.New(headscale.Config{})
	low, err := e.Lower(p.Tailnet)
	if err != nil || low.Err() != nil {
		t.Fatalf("lowering the template: %v %v", err, low.Err())
	}
	planned, _, err := plan(p, e.Capabilities(), low, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(planned) == 0 {
		t.Error("the template plans no tests")
	}
}

func TestInitNeverOverwrites(t *testing.T) {
	dir := initProject(t)
	pol := filepath.Join(dir, "policy.hujson")
	if err := os.WriteFile(pol, []byte("// mine\n{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "lab.yaml")); err != nil {
		t.Fatal(err)
	}
	code, _, errOut := runCmd(t, "init", dir)
	if code != exitCouldNotRun || !strings.Contains(errOut, "already exists") {
		t.Errorf("init over a project: exit %d, %q", code, errOut)
	}
	if b, _ := os.ReadFile(pol); string(b) != "// mine\n{}\n" {
		t.Error("init overwrote policy.hujson")
	}
	if _, err := os.Stat(filepath.Join(dir, "lab.yaml")); err == nil {
		t.Error("init wrote lab.yaml into a folder it refused")
	}
}

func TestInitRefusals(t *testing.T) {
	dir := t.TempDir()
	for _, args := range [][]string{
		{"init", "--template", "nope", dir},
		{"init", "a", "b"},
		{"init", "--nix", filepath.Join(dir, "x")}, // a test binary isn't a release
	} {
		if code, _, _ := runCmd(t, args...); code != exitCouldNotRun {
			t.Errorf("%q: exit %d, want %d", args, code, exitCouldNotRun)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "x")); err == nil {
		t.Error("a refused --nix init still wrote the project")
	}
}

func TestFlakePinsTheRelease(t *testing.T) {
	b, err := flakeFor("v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"github:bulkhead-labs/tailsuite/v1.2.3"`) || !strings.Contains(string(b), "pkgs.opentofu") {
		t.Errorf("flake = %s", b)
	}
	for _, v := range []string{"devel", "v0.0.0-20261026120000-abcdef123456", "v1.2", "1.2.3"} {
		if _, err := flakeFor(v); err == nil {
			t.Errorf("flakeFor(%q) pinned a version that isn't a release", v)
		}
	}
}

func TestCapabilities(t *testing.T) {
	code, out, _ := runCmd(t, "capabilities")
	if code != 0 || !strings.Contains(out, "headscale (headscale ") || !strings.Contains(out, "grants-acls") {
		t.Errorf("capabilities: exit %d, %q", code, out)
	}
	code, out, _ = runCmd(t, "capabilities", "--json")
	var ms []matrix
	if code != 0 || json.Unmarshal([]byte(out), &ms) != nil || len(ms) != 1 || len(ms[0].Capabilities) == 0 {
		t.Errorf("capabilities --json: exit %d, %q", code, out)
	}
}

// Everything wrong with the project, the flags or the selection is exit 3,
// before any lab is touched (0015).
func TestTestCouldNotRun(t *testing.T) {
	dir := initProject(t)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"test", "--dir", t.TempDir()}, "policy.hujson"},
		{[]string{"test", "--dir", dir, "--only", "no-such-label"}, `only "no-such-label" matches no test`},
		{[]string{"test", "--dir", dir, "--run", "no such test"}, "matches no test name"},
		{[]string{"test", "--dir", dir, "--run", "("}, "--run"},
		{[]string{"test", "--dir", dir, "--profile", "nope"}, `no profile "nope"`},
		{[]string{"test", "--dir", dir, "--profile", "nightly"}, "Tailscale engine isn't built yet"},
		{[]string{"test", "--dir", dir, "--engine", "netbird"}, `no engine "netbird"`},
		{[]string{"test", "--dir", dir, "extra"}, "unexpected argument"},
		{[]string{"test", "--no-such-flag"}, "no-such-flag"},
	} {
		code, out, errOut := runCmd(t, tc.args...)
		if code != exitCouldNotRun || !strings.Contains(errOut, tc.want) {
			t.Errorf("%q: exit %d, stderr %q, want %d and %q", tc.args, code, errOut, exitCouldNotRun, tc.want)
		}
		if out != "" {
			t.Errorf("%q printed results: %q", tc.args, out)
		}
	}
}

func TestUpRefusesWithoutTheAgent(t *testing.T) {
	if len(agentbin.Embedded()) > 0 {
		t.Skip("this build carries the node agent")
	}
	dir := initProject(t)
	code, _, errOut := runCmd(t, "up", "--dir", dir)
	if code != exitCouldNotRun || !strings.Contains(errOut, agentbin.ErrNotEmbedded.Error()) {
		t.Errorf("up without the agent: exit %d, %q", code, errOut)
	}
	if _, ok, _ := readState(dir); ok {
		t.Error("up left a lab state without starting a lab")
	}
}

func TestLabState(t *testing.T) {
	dir := t.TempDir()
	if _, ok, err := readState(dir); ok || err != nil {
		t.Fatalf("no state: %v %v", ok, err)
	}
	want := labState{Run: "0123abcd", Dir: dir, Engine: "headscale", Build: buildOf([]byte("agent")), Devices: []engine.PlanDevice{{Name: "laptop", User: "alice@example.com"}, {Name: "db", Tags: []string{"tag:db"}}}}
	if err := writeState(dir, want); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(statePath(dir))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode = %v, %v; want 0600", fi.Mode().Perm(), err)
	}
	got, ok, err := readState(dir)
	if !ok || err != nil || !reflect.DeepEqual(got, labState{Version: labStateVersion, Run: want.Run, Dir: want.Dir, Engine: want.Engine, Build: want.Build, Devices: want.Devices}) {
		t.Errorf("readState = %+v %v %v", got, ok, err)
	}
	for _, bad := range []string{"{", `{"version": 9, "run": "0123abcd"}`, `{"version": 1, "run": "../x"}`} {
		if err := os.WriteFile(statePath(dir), []byte(bad), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, _, err := readState(dir); err == nil {
			t.Errorf("readState(%q) accepted it", bad)
		}
	}
	if err := removeState(dir); err != nil {
		t.Fatal(err)
	}
	if err := removeState(dir); err != nil {
		t.Errorf("removing no state: %v", err)
	}
}

// report's exit code is the run's (0015), unless nobody could see the
// results.
func TestReportExitCodes(t *testing.T) {
	for _, exit := range []int{0, 1, 2} {
		r := &runner.Run{Engine: "headscale", Exit: exit}
		var out, errOut bytes.Buffer
		if got := report(r, t.TempDir(), false, "", &out, &errOut); got != exit {
			t.Errorf("report of a run with exit %d = %d", exit, got)
		}
	}
	dir := t.TempDir()
	r := &runner.Run{Engine: "headscale", Exit: 0, Summary: runner.Summary{Pass: 1}}
	var errOut bytes.Buffer
	if got := report(r, dir, false, "", brokenWriter{}, &errOut); got != exitCouldNotRun {
		t.Errorf("report to a broken stdout = %d, want %d", got, exitCouldNotRun)
	}
	var out bytes.Buffer
	if got := report(r, dir, true, filepath.Join(dir, "no", "such", "dir", "junit.xml"), &out, &errOut); got != exitCouldNotRun {
		t.Errorf("report with an unwritable --junit = %d, want %d", got, exitCouldNotRun)
	}
	var back runner.Run
	if err := json.Unmarshal(out.Bytes(), &back); err != nil || back.Summary.Pass != 1 || back.Exit != exitCouldNotRun {
		t.Errorf("--json printed %q (%v): its exit must match the process's", out.String(), err)
	}
	r.Exit, r.Warnings = 0, nil
	junit := filepath.Join(dir, "junit.xml")
	if got := report(r, dir, false, junit, &out, &errOut); got != 0 {
		t.Errorf("report with --junit = %d", got)
	}
	if b, err := os.ReadFile(junit); err != nil || !bytes.Contains(b, []byte("<testsuites")) {
		t.Errorf("junit: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(dir, stateDir, "results.json")); err != nil || !json.Valid(b) {
		t.Errorf("results.json: %v", err)
	}
}

func TestLabelsFlag(t *testing.T) {
	var l labels
	for _, v := range []string{"a", "b, c", ""} {
		if err := l.Set(v); err != nil {
			t.Fatal(err)
		}
	}
	if l.String() != "a,b,c" {
		t.Errorf("labels = %q", l.String())
	}
}

func TestEngines(t *testing.T) {
	if err := checkEngine("headscale"); err != nil {
		t.Error(err)
	}
	for _, e := range []string{"tailscale", "", "netbird"} {
		if err := checkEngine(e); err == nil {
			t.Errorf("checkEngine(%q) accepted it", e)
		}
	}
}

func TestSamePlan(t *testing.T) {
	a := []engine.PlanDevice{{Name: "laptop", User: "alice@example.com"}, {Name: "server", Tags: []string{"tag:prod"}}}
	if !samePlan(a, []engine.PlanDevice{{Name: "laptop", User: "alice@example.com"}, {Name: "server", Tags: []string{"tag:prod"}}}) {
		t.Error("the same plan differs")
	}
	for name, b := range map[string][]engine.PlanDevice{
		"a tag changed":    {{Name: "laptop", User: "alice@example.com"}, {Name: "server", Tags: []string{"tag:admin"}}},
		"a tag added":      {{Name: "laptop", User: "alice@example.com"}, {Name: "server", Tags: []string{"tag:prod", "tag:admin"}}},
		"tagged to a user": {{Name: "laptop", User: "alice@example.com"}, {Name: "server", User: "bob@example.com"}},
		"a user changed":   {{Name: "laptop", User: "bob@example.com"}, {Name: "server", Tags: []string{"tag:prod"}}},
		"a device less":    {{Name: "laptop", User: "alice@example.com"}},
		"a pin added":      {{Name: "laptop", User: "alice@example.com", IPv4: "100.64.0.9"}, {Name: "server", Tags: []string{"tag:prod"}}},
	} {
		if samePlan(a, b) {
			t.Errorf("%s: samePlan says the lab still matches", name)
		}
	}
}

func TestInitKeepsAnExistingGitignore(t *testing.T) {
	dir := t.TempDir()
	gi := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(gi, []byte("node_modules/"), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := runCmd(t, "init", dir); code != 0 {
		t.Fatalf("init into a repo with a .gitignore: exit %d: %s", code, errOut)
	}
	if b, _ := os.ReadFile(gi); string(b) != "node_modules/\n/.tailsuite/\n" {
		t.Errorf(".gitignore = %q", b)
	}
}

// onUpsLab refuses a lab that isn't this project's, or doesn't match it,
// before it touches Docker.
func TestUpsLabRefusals(t *testing.T) {
	dir := initProject(t)
	p, o, err := load(dir, suite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	plan := engine.PlanEveryDevice(p.Tailnet)
	abs, _ := filepath.Abs(dir)
	good := labState{Run: "0123abcd", Dir: abs, Engine: "headscale", Devices: plan.Devices}
	changed := slices.Clone(plan.Devices)
	changed[0].Tags = append(slices.Clone(changed[0].Tags), "tag:extra")
	for name, tc := range map[string]struct {
		st   labState
		want string
	}{
		"another folder's": {labState{Run: good.Run, Dir: "/elsewhere", Engine: good.Engine, Devices: good.Devices}, "belongs to /elsewhere"},
		"another engine":   {labState{Run: good.Run, Dir: abs, Engine: "tailscale", Devices: good.Devices}, "runs tailscale"},
		"a changed device": {labState{Run: good.Run, Dir: abs, Engine: good.Engine, Devices: changed}, "changed a device"},
	} {
		_, err := onUpsLab(context.Background(), dir, tc.st, plan, engine.Lowering{}, nil, runner.Config{Options: o})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want %q", name, err, tc.want)
		}
	}
}

//go:build ui

package ui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/project"
)

// playwrightImage runs the smoke test: Playwright 1.55.0's browsers,
// pinned by digest (published 2025-08-28). The Python package itself is
// installed from testdata/requirements.txt, pinned by hash.
const playwrightImage = "mcr.microsoft.com/playwright/python:v1.55.0-noble@sha256:640d578aae63cfb632461d1b0aecb01414e4e020864ac3dd45a868dc0eff3078"

// The Playwright smoke test (#11), on the host network so the browser
// reaches the UI on 127.0.0.1. It needs Docker on Linux:
//
//	go test -tags ui ./internal/ui/
func TestSmoke(t *testing.T) {
	p, err := project.Load("../suite/testdata/startup")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil { //nolint:gosec // G302: the browser container writes the results file as another user
		t.Fatal(err)
	}
	s, err := Start(Options{View: NewView(p), Results: filepath.Join(dir, "results.json")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	link, _, err := s.NewLink()
	if err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("testdata/smoke.py")
	if err != nil {
		t.Fatal(err)
	}
	// TAILSUITE_SMOKE_SHOTS names a folder for screenshots, to look at
	// the page by hand.
	shots := ""
	if d := os.Getenv("TAILSUITE_SMOKE_SHOTS"); d != "" {
		shots = "/shots"
		if err := os.MkdirAll(d, 0o777); err != nil { //nolint:gosec // G301: the browser container writes into it as another user
			t.Fatal(err)
		}
		if err := os.Chmod(d, 0o777); err != nil { //nolint:gosec // G302: as above
			t.Fatal(err)
		}
	}
	args := []string{"run", "--rm", "--network", "host", "--memory", "1g", "--memory-swap", "1g",
		"--name", "tailsuite-ui-smoke-" + filepath.Base(dir),
		"-v", filepath.Dir(script) + ":/smoke:ro", "-v", dir + ":/out"}
	if shots != "" {
		args = append(args, "-v", os.Getenv("TAILSUITE_SMOKE_SHOTS")+":/shots")
	}
	args = append(args, playwrightImage, "sh", "-c",
		`pip install -q --break-system-packages --require-hashes --no-deps -r /smoke/requirements.txt && exec python3 /smoke/smoke.py "$0" /out/results.json "$1"`,
		link, shots)
	cmd := exec.Command("docker", args...) //nolint:gosec,noctx // G204: fixed arguments and this test's own paths
	out, err := cmd.CombinedOutput()
	t.Logf("%s", out)
	if err != nil {
		t.Fatal(err)
	}
}

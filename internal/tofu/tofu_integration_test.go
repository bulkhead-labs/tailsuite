//go:build tofu

package tofu

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// The run directory is what OpenTofu expects: init installs the pinned
// provider from the shipped lock file, read-only, and the configuration
// validates, all with the scrubbed environment. It needs OpenTofu on PATH
// (the dev shell has it) and the network for the provider:
//
//	nix develop --command go test -tags tofu ./internal/tofu/
func TestTheRunDirectoryValidates(t *testing.T) {
	tofu, err := exec.LookPath("tofu")
	if err != nil {
		t.Fatal("OpenTofu isn't on PATH: run this in nix develop")
	}
	r, err := PrepareRun(project(t, nil), "0123abcd", []byte(`{"grants": []}`), Lab{Tailnet: "lab.example", BaseURL: "https://api.example"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Remove() }()
	env := r.Env(os.Environ(), Credentials{ClientID: "synthetic", ClientSecret: "synthetic"})
	for _, args := range [][]string{{"init", "-input=false", "-lockfile=readonly"}, {"validate"}} {
		cmd := exec.CommandContext(context.Background(), tofu, args...) //nolint:gosec // G204: OpenTofu from PATH, fixed arguments
		cmd.Dir = r.Dir + "/tofu"
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("tofu %s: %v\n%s", args[0], err, out)
		}
	}
}

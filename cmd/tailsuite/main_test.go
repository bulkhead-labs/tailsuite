package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"testing"
)

// asMain makes this test binary run as tailsuite itself, as up --detach
// re-runs its own binary for the background process.
const asMain = "TAILSUITE_TEST_AS_MAIN"

func TestMain(m *testing.M) {
	if os.Getenv(asMain) == "1" {
		main()
	}
	os.Exit(m.Run())
}

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no arguments prints the usage", nil, 0, "tailsuite init --template startup", ""},
		{"version", []string{"version"}, 0, "tailsuite ", ""},
		{"--version", []string{"--version"}, 0, runtime.Version(), ""},
		{"help", []string{"help"}, 0, "tailsuite version", ""},
		{"--help", []string{"--help"}, 0, "tailsuite version", ""},
		{"an unknown command couldn't run", []string{"deploy"}, exitCouldNotRun, "", `unknown command "deploy"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), tt.args, &stdout, &stderr)
			if code != tt.wantCode {
				t.Errorf("exit code = %d, want %d", code, tt.wantCode)
			}
			check(t, "stdout", stdout.String(), tt.wantStdout)
			check(t, "stderr", stderr.String(), tt.wantStderr)
		})
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestRunReportsAFailedWrite(t *testing.T) {
	for _, args := range [][]string{{"version"}, {"help"}, nil} {
		if code := run(context.Background(), args, brokenWriter{}, brokenWriter{}); code != exitCouldNotRun {
			t.Errorf("run(%q) with a broken stdout = %d, want %d", args, code, exitCouldNotRun)
		}
	}
}

// check wants got to contain want, or to be empty when want is.
func check(t *testing.T, stream, got, want string) {
	t.Helper()
	if want == "" {
		if got != "" {
			t.Errorf("%s = %q, want nothing", stream, got)
		}
		return
	}
	if !strings.Contains(got, want) {
		t.Errorf("%s = %q, want it to contain %q", stream, got, want)
	}
}

// Nix builds, and the release build will (#2), set TAILSUITE_REQUIRE_AGENT=1: their binary must
// carry the node agent for both architectures (#52).
func TestVersionNamesTheEmbeddedAgent(t *testing.T) {
	var out, errOut strings.Builder
	if code := run(context.Background(), []string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	want := "node agent: not embedded"
	if os.Getenv("TAILSUITE_REQUIRE_AGENT") == "1" {
		want = "node agent: linux/amd64, linux/arm64"
	} else if !strings.Contains(out.String(), want) {
		want = "node agent: linux/"
	}
	if !strings.Contains(out.String(), want) {
		t.Errorf("got %q, want %q", out.String(), want)
	}
}

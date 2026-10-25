package main

import (
	"bytes"
	"errors"
	"runtime"
	"strings"
	"testing"
)

func TestRun(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantCode   int
		wantStdout string
		wantStderr string
	}{
		{"no arguments prints the usage", nil, 0, "one command works so far", ""},
		{"version", []string{"version"}, 0, "tailsuite ", ""},
		{"--version", []string{"--version"}, 0, runtime.Version(), ""},
		{"help", []string{"help"}, 0, "tailsuite version", ""},
		{"--help", []string{"--help"}, 0, "tailsuite version", ""},
		{"an unknown command couldn't run", []string{"up"}, exitCouldNotRun, "", `unknown command "up"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(tt.args, &stdout, &stderr)
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
		if code := run(args, brokenWriter{}, brokenWriter{}); code != exitCouldNotRun {
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

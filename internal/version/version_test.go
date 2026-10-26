package version

import (
	"runtime/debug"
	"testing"
)

func TestResolve(t *testing.T) {
	info := func(v string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) {
			return &debug.BuildInfo{Main: debug.Module{Path: "github.com/bulkhead-labs/tailsuite", Version: v}}, true
		}
	}
	none := func() (*debug.BuildInfo, bool) { return nil, false }

	tests := []struct {
		name   string
		linked string
		read   func() (*debug.BuildInfo, bool)
		want   string
	}{
		{"link-time version wins", "v0.1.0", info("v0.0.0-20261025000000-0123456789ab"), "v0.1.0"},
		{"go install records the module version", "", info("v0.1.0"), "v0.1.0"},
		{"a git checkout records a pseudo-version", "", info("v0.0.0-20261025000000-0123456789ab+dirty"), "v0.0.0-20261025000000-0123456789ab+dirty"},
		{"no VCS information", "", info("(devel)"), "devel"},
		{"empty module version", "", info(""), "devel"},
		{"no build information", "", none, "devel"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolve(tt.linked, tt.read); got != tt.want {
				t.Errorf("resolve(%q) = %q, want %q", tt.linked, got, tt.want)
			}
		})
	}
}

func TestStringIsNeverEmpty(t *testing.T) {
	if String() == "" {
		t.Fatal("String() returned an empty version")
	}
}

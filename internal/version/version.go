// Package version reports which build of tailsuite is running.
package version

import "runtime/debug"

// version is set at link time by release and Nix builds:
//
//	-ldflags "-X github.com/bulkhead-labs/tailsuite/internal/version.version=v0.1.0"
var version string

// String returns the version set at link time. Without one, it returns
// what the Go toolchain recorded: the module version for `go install`, or a
// pseudo-version from the commit for a build in a git checkout. Failing
// both, it returns "devel".
func String() string {
	return resolve(version, debug.ReadBuildInfo)
}

func resolve(linked string, readBuildInfo func() (*debug.BuildInfo, bool)) string {
	if linked != "" {
		return linked
	}
	if info, ok := readBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "devel"
}

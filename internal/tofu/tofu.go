// Package tofu is tailsuite's side of OpenTofu (decisions 0004, 0014):
// the module it ships, the check that a project's tofu/ applies nothing
// else, and the run directory where a Tailscale-engine lab applies it.
// tailsuite never runs tofu in the user's tofu/.
package tofu

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"path"
	"slices"
)

// The module's file names in a project's tofu/.
const (
	ModuleFile = "tailsuite.tf"
	ValuesFile = "tailnet.auto.tfvars"
	lockFile   = ".terraform.lock.hcl"
)

// OpenTofuVersion is the OpenTofu whose file rules CheckProject follows,
// and which a lab runs (1.13.1, released 2026-10-01).
const OpenTofuVersion = "1.13.1"

// ProviderVersion is the Tailscale provider tailsuite.tf pins.
const ProviderVersion = "0.29.2"

// Variables are the variables tailsuite.tf declares: the only ones
// tailnet.auto.tfvars may set (0014).
var Variables = []string{"devices"}

// shipped holds every release's tailsuite.tf with its lock file: current/
// is this build's, and a release adds shipped/<version>/ (#2).
//
//go:embed all:shipped
var shipped embed.FS

// Release is one shipped tailsuite.tf.
type Release struct {
	// Name is the release that shipped it, or "current" for this build.
	Name   string
	Module []byte
	Lock   []byte
}

// Current is this build's module.
func Current() Release {
	r, _ := release("current")
	return r
}

func release(name string) (Release, bool) {
	mod, err := shipped.ReadFile(path.Join("shipped", name, ModuleFile))
	if err != nil {
		return Release{}, false
	}
	lock, err := shipped.ReadFile(path.Join("shipped", name, lockFile))
	if err != nil {
		return Release{}, false
	}
	return Release{Name: name, Module: mod, Lock: lock}, true
}

// Shipped finds the release that shipped a tailsuite.tf, by its SHA-256.
// A file no release shipped, an edited one included, isn't found.
func Shipped(module []byte) (Release, bool) {
	want := sha256.Sum256(module)
	entries, _ := fs.ReadDir(shipped, "shipped")
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	slices.Sort(names)
	for _, n := range names {
		r, ok := release(n)
		if ok && sha256.Sum256(r.Module) == want {
			return r, true
		}
	}
	return Release{}, false
}

// Hash is a file's SHA-256, as results name the module that ran.
func Hash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

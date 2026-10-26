// Package agentbin carries the node agent (cmd/tailsuite-agent) inside
// the tailsuite binary, built for both Linux architectures a Docker host
// runs, since a Mac's lab nodes are Linux too (decision 0027).
//
// The binaries are made by go generate, which release and Nix builds run,
// and land in bin/, which git ignores. A plain go build without them still
// compiles; For then says how to get a build that has them.
package agentbin

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
)

//go:generate go run ./gen

//go:embed bin
var bin embed.FS

// Arches are the architectures a build carries the agent for.
var Arches = []string{"amd64", "arm64"}

// ErrNotEmbedded means this build doesn't carry the agent.
var ErrNotEmbedded = errors.New("this tailsuite was built without the node agent: install a release, or build with `go generate ./... && go build ./cmd/tailsuite`")

// Name is the agent's file name in bin/ for an architecture.
func Name(arch string) string { return "tailsuite-agent-linux-" + arch }

// For returns the agent for a Docker host's architecture, as Go names it
// (amd64, arm64): the host's own, not tailsuite's, so a Mac running an
// arm64 Docker VM gets the arm64 agent whatever it runs on.
func For(arch string) ([]byte, error) {
	b, err := fs.ReadFile(bin, "bin/"+Name(arch))
	switch {
	case errors.Is(err, fs.ErrNotExist) && Embedded() == nil:
		return nil, ErrNotEmbedded
	case errors.Is(err, fs.ErrNotExist):
		return nil, fmt.Errorf("the node agent isn't built for linux/%s; this tailsuite carries it for linux/%v", arch, Embedded())
	case err != nil:
		return nil, err
	}
	return b, nil
}

// Embedded lists the architectures this build carries the agent for.
func Embedded() []string {
	var out []string
	for _, a := range Arches {
		if _, err := fs.Stat(bin, "bin/"+Name(a)); err == nil {
			out = append(out, a)
		}
	}
	return out
}

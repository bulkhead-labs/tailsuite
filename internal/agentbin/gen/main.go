// Command gen builds the node agent for every architecture agentbin
// embeds, into ../bin. go generate runs it from internal/agentbin.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
)

func main() {
	for _, arch := range agentbin.Arches {
		out := filepath.Join("bin", agentbin.Name(arch))
		cmd := exec.CommandContext(context.Background(), "go", "build", "-trimpath", "-ldflags=-s -w", "-o", out, "github.com/bulkhead-labs/tailsuite/cmd/tailsuite-agent") //nolint:gosec // G204: out is built from agentbin's own names
		// The baseline CPU level, whatever the developer's environment says,
		// so the agent runs on any node of that architecture.
		cmd.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0", "GOAMD64=v1", "GOARM64=v8.0")
		cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "building the agent for linux/%s: %v\n", arch, err)
			os.Exit(1)
		}
	}
}

// Command tailsuite rebuilds a tailnet in a local lab, on real Tailscale
// clients, and proves what it allows with real traffic.
//
// Only `tailsuite version` works so far. The planned commands are in
// docs/architecture.md, and #12 builds the CLI.
package main

import (
	"fmt"
	"io"
	"os"
	"runtime"

	"github.com/bulkhead-labs/tailsuite/internal/version"
)

// exitCouldNotRun is the exit code for "tailsuite couldn't run", such as an
// unknown command. `tailsuite test` uses the same code for the same case.
const exitCouldNotRun = 3

const usage = `tailsuite proves what your tailnet allows, with real traffic.

It's in development, and one command works so far:

  tailsuite version   print the version

The plan: https://github.com/bulkhead-labs/tailsuite/blob/main/docs/plan.md
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	var err error
	switch cmd {
	case "", "help", "-h", "-help", "--help":
		_, err = fmt.Fprint(stdout, usage)
	case "version", "--version":
		_, err = fmt.Fprintf(stdout, "tailsuite %s %s %s/%s\n", version.String(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	default:
		// If stderr fails too, the exit code is all that's left.
		_, _ = fmt.Fprintf(stderr, "tailsuite: unknown command %q\n\n%s", cmd, usage)
		return exitCouldNotRun
	}
	if err != nil {
		return exitCouldNotRun
	}
	return 0
}

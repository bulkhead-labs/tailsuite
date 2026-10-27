// Command tailsuite rebuilds a tailnet in a local lab, on real Tailscale
// clients, and proves what it allows with real traffic.
//
// The commands are in docs/architecture.md. `tailsuite up` starts the lab
// without the local UI until #11.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"syscall"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/version"
)

// Exit codes (decision 0015). Every command but test exits 0 or
// exitCouldNotRun; test also uses 1 and 2, from the run's results.
const (
	exitOK          = 0
	exitCouldNotRun = 3
)

const usage = `tailsuite proves what your tailnet allows, with real traffic.

Usage:

  tailsuite init --template startup [--nix] [dir]   write a project folder
  tailsuite up [--profile <name>] [--engine <name>] [--detach] [--clean]
                                                    start the lab; Ctrl-C (or down, if
                                                    detached) tears it down
  tailsuite test [--profile <name>] [--engine <name>] [--only <label>]
                 [--skip <label>] [--run <regex>] [--strict] [--json] [--junit <file>]
                                                    run the suite
  tailsuite down [--run <id> | --all]                tear down the lab, a run, or every run
  tailsuite capabilities [--json]                    what each engine supports
  tailsuite version                                  print the version

up, test and down work on the project in the current folder, or the one
--dir names. test uses the lab up holds, or starts one for the run.

Exit codes: 0 fine, 1 a test failed, 2 nothing proven (or unproven results
under --strict), 3 tailsuite couldn't run.

The plan: https://github.com/bulkhead-labs/tailsuite/blob/main/docs/plan.md
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	code := run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	stop()
	os.Exit(code)
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	cmd, rest := "", args
	if len(args) > 0 {
		cmd, rest = args[0], args[1:]
	}
	switch cmd {
	case "", "help", "-h", "-help", "--help":
		return wrote(fmt.Fprint(stdout, usage))
	case "version", "--version":
		agents := "not embedded (go generate ./... builds it)"
		if a := agentbin.Embedded(); len(a) > 0 {
			agents = "linux/" + strings.Join(a, ", linux/")
		}
		return wrote(fmt.Fprintf(stdout, "tailsuite %s %s %s/%s\nnode agent: %s\n", version.String(), runtime.Version(), runtime.GOOS, runtime.GOARCH, agents))
	case "init":
		return cmdInit(rest, stdout, stderr)
	case "up":
		return cmdUp(ctx, rest, stdout, stderr)
	case "test":
		return cmdTest(ctx, rest, stdout, stderr)
	case "down":
		return cmdDown(ctx, rest, stdout, stderr)
	case "capabilities":
		return cmdCapabilities(rest, stdout, stderr)
	default:
		// If stderr fails too, the exit code is all that's left.
		_, _ = fmt.Fprintf(stderr, "tailsuite: unknown command %q\n\n%s", cmd, usage)
		return exitCouldNotRun
	}
}

// wrote turns a write's error into an exit code.
func wrote(_ int, err error) int {
	if err != nil {
		return exitCouldNotRun
	}
	return exitOK
}

// fail reports an error and returns exitCouldNotRun.
func fail(stderr io.Writer, err error) int {
	_, _ = fmt.Fprintf(stderr, "tailsuite: %v\n", err)
	return exitCouldNotRun
}

// flags returns a flag set that reports to stderr and never exits.
func flags(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet("tailsuite "+name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	return fs
}

// parse parses args, and refuses arguments a command doesn't take. -h
// prints the command's flags and exits 0.
func parse(fs *flag.FlagSet, args []string, maxArgs int) (ok bool, code int) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return false, exitOK
		}
		return false, exitCouldNotRun
	}
	if fs.NArg() > maxArgs {
		_, _ = fmt.Fprintf(fs.Output(), "%s: unexpected argument %q\n", fs.Name(), fs.Arg(maxArgs))
		return false, exitCouldNotRun
	}
	return true, exitOK
}

// labels is a repeatable flag; a comma also separates labels.
type labels []string

func (l *labels) String() string { return strings.Join(*l, ",") }

func (l *labels) Set(v string) error {
	for _, s := range strings.Split(v, ",") {
		if s = strings.TrimSpace(s); s != "" {
			*l = append(*l, s)
		}
	}
	return nil
}

package agent

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"
)

const usage = `usage:
  tailsuite-agent run --node <name> [--socket <path>] [-- <command> [<arg>...]]
  tailsuite-agent connect [--socket <path>] [--wait <duration>]
`

// Main runs the agent's command line and returns its exit code.
//
// run is the node's entrypoint: it serves sessions on the socket, and runs
// the node's own command, if there is one, exiting with its code.
//
// connect is one session: the host runs it through the sandbox's exec
// channel, and talks to the agent on its stdin and stdout.
func Main(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	socket := fs.String("socket", DefaultSocket, "the agent's socket in the node")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "run":
		node := fs.String("node", "", "this node's name in the lab")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if *node == "" {
			_, _ = fmt.Fprintln(stderr, "tailsuite-agent: --node is required")
			return 2
		}
		err := Run(ctx, New(*node), *socket, fs.Args(), stderr)
		var exit *ExitError
		switch {
		case err == nil:
			return 0
		case errors.As(err, &exit):
			return exit.Code
		default:
			_, _ = fmt.Fprintln(stderr, "tailsuite-agent:", err)
			return 1
		}
	case "connect":
		wait := fs.Duration("wait", 10*time.Second, "how long to wait for the agent to start")
		if err := fs.Parse(args[1:]); err != nil {
			return 2
		}
		if err := Connect(ctx, *socket, *wait, stdin, stdout); err != nil {
			_, _ = fmt.Fprintln(stderr, "tailsuite-agent:", err)
			return 1
		}
		return 0
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
}

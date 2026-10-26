// Command tailsuite-agent is the node agent: it runs in every lab node,
// and the host drives it over the sandbox's exec channel. See
// internal/agent.
package main

import (
	"os"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
)

func main() {
	os.Exit(agent.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

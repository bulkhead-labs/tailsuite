package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
)

// matrix is one engine's rows of 0016's capability matrix.
type matrix struct {
	Engine       string              `json:"engine"`
	Version      string              `json:"version"`
	Capabilities engine.Capabilities `json:"capabilities"`
}

func cmdCapabilities(args []string, stdout, stderr io.Writer) int {
	fs := flags("capabilities", stderr)
	asJSON := fs.Bool("json", false, "print JSON")
	if ok, code := parse(fs, args, 0); !ok {
		return code
	}
	e := headscale.New(headscale.Config{})
	ms := []matrix{{Engine: e.Name(), Version: e.Version(), Capabilities: e.Capabilities()}}
	if *asJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(ms); err != nil {
			return exitCouldNotRun
		}
		return exitOK
	}
	var b strings.Builder
	for _, m := range ms {
		fmt.Fprintf(&b, "%s (%s)\n\n", m.Engine, m.Version)
		tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "  CAPABILITY\tSUPPORT\tLATER\tNOTE")
		for _, c := range m.Capabilities {
			later := ""
			if c.Target != "" {
				later = string(c.Target)
				if c.Milestone != "" {
					later += " in " + c.Milestone
				}
			}
			_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", c.Capability, c.Support, later, c.Note)
		}
		_ = tw.Flush()
		b.WriteString("\n")
	}
	b.WriteString("A capability an engine doesn't list is refused. The Tailscale engine is #6.\n")
	return wrote(io.WriteString(stdout, b.String()))
}

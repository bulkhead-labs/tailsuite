package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/tofu"
)

// refusedTofu is why a project's lab is refused when its tofu/ applies
// more than tailsuite's module (0014).
func refusedTofu(problems []string) string {
	msg := "a lab can't stand in for production: tofu/ must apply only tailsuite's module (decision 0032), and "
	for i, p := range problems {
		if i > 0 {
			msg += "; "
		}
		msg += p
	}
	return msg
}

// cmdUpgrade is `tailsuite upgrade tofu` (0014, 0032): it puts this
// release's tailsuite.tf in tofu/, and leaves the values alone. A copy
// no release shipped, an edited one, is replaced only with --force.
func cmdUpgrade(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "tofu" {
		return fail(stderr, errors.New("upgrade: say what to upgrade: tailsuite upgrade tofu"))
	}
	set := flags("upgrade tofu", stderr)
	dir := set.String("dir", ".", "the project")
	force := set.Bool("force", false, "replace a tailsuite.tf no release shipped, such as an edited one")
	if ok, code := parse(set, args[1:], 0); !ok {
		return code
	}
	// A mistyped --dir would otherwise leave a stray tofu/ behind.
	if _, err := os.Stat(filepath.Join(*dir, project.LabFile)); err != nil {
		return fail(stderr, fmt.Errorf("%s isn't a tailsuite project (no %s): tailsuite init makes one", *dir, project.LabFile))
	}
	path := filepath.Join(*dir, "tofu", tofu.ModuleFile)
	cur := tofu.Current()
	old, err := os.ReadFile(path) //nolint:gosec // G304: the user's own project
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: a project folder the user commits
			return fail(stderr, err)
		}
	case err != nil:
		return fail(stderr, err)
	case bytes.Equal(old, cur.Module):
		return wrote(fmt.Fprintf(stdout, "%s is already this release's.\n", path))
	default:
		if r, ok := tofu.Shipped(old); !ok && !*force {
			return fail(stderr, fmt.Errorf("%s isn't one a tailsuite release shipped (edited?); --force replaces it, and your edits go", path))
		} else if ok {
			_, _ = fmt.Fprintf(stdout, "Replacing the module from release %s.\n", r.Name)
		}
	}
	if err := os.WriteFile(path, cur.Module, 0o644); err != nil { //nolint:gosec // G306: a project file the user commits
		return fail(stderr, err)
	}
	return wrote(fmt.Fprintf(stdout, "Wrote this release's %s (Tailscale provider %s); tailnet.auto.tfvars is unchanged.\n", path, tofu.ProviderVersion))
}

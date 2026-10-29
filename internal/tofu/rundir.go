package tofu

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// Lab is where a run directory's module applies: a throwaway tailnet.
type Lab struct {
	// Tailnet is the throwaway tailnet's name, and BaseURL its API.
	Tailnet string
	BaseURL string
}

// labTF is tailsuite's own file in a run directory: the provider pointed
// at the lab, and local state. Credentials come only from the
// environment.
func labTF(l Lab) string {
	return `# Written by tailsuite for one lab run; deleted at teardown.
terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}

provider "tailscale" {
  tailnet  = ` + hclString(l.Tailnet) + `
  base_url = ` + hclString(l.BaseURL) + `
}
`
}

// hclString is s as an HCL string literal, with HCL's own escapes.
func hclString(s string) string {
	return string(hclwrite.TokensForValue(cty.StringVal(s)).Bytes())
}

// Run is a lab run's directory under the project's .tailsuite/:
//
//	.tailsuite/run-<id>/
//	  policy.hujson   the engine's copy, where tailsuite.tf looks
//	  tofu.rc         tailsuite's CLI config: the user's isn't read
//	  tofu/
//	    tailsuite.tf, tailnet.auto.tfvars   the project's, checked
//	    lab.tf                              tailsuite's
//	    .terraform.lock.hcl                 the shipped release's
type Run struct {
	Dir string
	// Release is the tailsuite.tf that runs, for the results.
	Release Release
}

// PrepareRun builds a run directory for a project, after CheckProject
// passes. policy is the engine's copy of the policy. The directory is
// readable only by this user; Remove deletes it.
func PrepareRun(projectDir, runID string, policy []byte, l Lab) (*Run, error) {
	tofuDir := filepath.Join(projectDir, "tofu")
	if problems := CheckProject(tofuDir); len(problems) > 0 {
		return nil, fmt.Errorf("tofu/ applies more than tailsuite.tf: %s", strings.Join(problems, "; "))
	}
	module, err := os.ReadFile(filepath.Join(tofuDir, ModuleFile)) //nolint:gosec // G304: the user's own project
	if err != nil {
		return nil, err
	}
	rel, ok := Shipped(module)
	if !ok {
		return nil, errors.New("tofu/tailsuite.tf changed while the run was being prepared")
	}
	values, err := os.ReadFile(filepath.Join(tofuDir, ValuesFile)) //nolint:gosec // G304: the user's own project
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	dir := filepath.Join(projectDir, ".tailsuite", "run-"+runID)
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return nil, err
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return nil, err
	}
	r := &Run{Dir: dir, Release: rel}
	files := map[string][]byte{
		"policy.hujson":      policy,
		"tofu.rc":            []byte("# tailsuite's OpenTofu CLI config: empty, so no user's is read.\n"),
		"tofu/" + ModuleFile: rel.Module,
		"tofu/" + ValuesFile: values,
		"tofu/lab.tf":        []byte(labTF(l)),
		"tofu/" + lockFile:   rel.Lock,
	}
	if err := os.Mkdir(filepath.Join(dir, "tofu"), 0o700); err != nil {
		return nil, errors.Join(err, r.Remove())
	}
	for name, b := range files {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			return nil, errors.Join(err, r.Remove())
		}
	}
	return r, nil
}

// Remove deletes the run directory: state, plans and all.
func (r *Run) Remove() error { return os.RemoveAll(r.Dir) }

// passEnv are the variables of tailsuite's own environment that reach
// tofu: what it needs to find programs, a home, a proxy and CAs. Nothing
// else does: no TF_VAR_*, TF_CLI_ARGS*, TF_*, or other credentials.
var passEnv = []string{
	"PATH", "HOME", "TMPDIR",
	"HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy",
	"SSL_CERT_FILE", "SSL_CERT_DIR",
}

// Credentials are the throwaway tailnet's OAuth client. They reach tofu
// only through its environment, never a file.
type Credentials struct {
	ClientID     string
	ClientSecret string
}

// Env is tofu's whole environment for a run: the allow-list from environ
// (name=value pairs, as os.Environ gives), tailsuite's CLI config, and
// the credentials.
func (r *Run) Env(environ []string, c Credentials) []string {
	var env []string
	for _, kv := range environ {
		k, _, ok := strings.Cut(kv, "=")
		if ok && slices.Contains(passEnv, k) {
			env = append(env, kv)
		}
	}
	return append(env,
		"TF_CLI_CONFIG_FILE="+filepath.Join(r.Dir, "tofu.rc"),
		"TF_IN_AUTOMATION=1",
		"TF_INPUT=0",
		"TAILSCALE_OAUTH_CLIENT_ID="+c.ClientID,
		"TAILSCALE_OAUTH_CLIENT_SECRET="+c.ClientSecret,
	)
}

// Apply runs tofu init (with the shipped lock file, read-only) and tofu
// apply in the run directory's tofu/, with env as tofu's whole
// environment. tofu names the OpenTofu binary.
func (r *Run) Apply(ctx context.Context, tofu string, env []string) error {
	for _, args := range [][]string{
		{"init", "-input=false", "-no-color", "-lockfile=readonly"},
		{"apply", "-input=false", "-no-color", "-auto-approve"},
	} {
		cmd := exec.CommandContext(ctx, tofu, args...) //nolint:gosec // G204: the pinned OpenTofu, fixed arguments
		cmd.Dir = filepath.Join(r.Dir, "tofu")
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("tofu %s: %w\n%s", args[0], err, out)
		}
	}
	return nil
}

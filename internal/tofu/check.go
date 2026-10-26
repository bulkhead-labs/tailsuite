package tofu

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

// Config file extensions OpenTofu loads from a module's folder, longest
// first, so .tf.json isn't read as .json.
var configExts = []string{".tofu.json", ".tf.json", ".tofu", ".tf"}

// Variables file extensions.
var varsExts = []string{".tfvars.json", ".tfvars"}

// Blocks a configuration file beside tailsuite.tf may hold: ones that
// apply nothing (0014).
var allowedBlocks = []string{"terraform", "provider", "variable", "locals", "output", "import", "data", "ephemeral"}

// Data sources and ephemeral resources that can run commands or send
// requests, so they're refused even though they only read: hashicorp's
// external, http and local_command (hashicorp/local 2.6.0 and later).
var refusedSources = []string{"external", "http", "local_command"}

// The one resource a file beside tailsuite.tf may import into: the
// policy, which the provider doesn't take over without an import or
// overwrite_existing_content. Importing a device's tags would make a
// replace in production that the lab never does.
var importable = []string{"tailscale_acl.policy"}

// CheckProject checks that a project's tofu/ folder applies only
// tailsuite's module, by the file rules of the OpenTofu tailsuite pins
// (0014). Each problem is a reason the lab run is REFUSED. Files OpenTofu
// doesn't load (a README, the lock file, .terraform/, state) are
// ignored.
func CheckProject(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return []string{fmt.Sprintf("tofu/: %v", err)}
	}
	// OpenTofu skips hidden configuration files, but loads hidden
	// variables files (*.auto.tfvars by suffix alone), so only the first
	// are skipped here.
	names := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ext := split(e.Name()); ignored(e.Name()) && !slices.Contains(varsExts, ext) {
			continue
		}
		names[e.Name()] = true
	}
	var problems []string
	add := func(format string, a ...any) { problems = append(problems, fmt.Sprintf(format, a...)) }

	if !names[ModuleFile] {
		add("tofu/%s is missing: tailsuite init or tailsuite export tofu writes it", ModuleFile)
	} else if b, err := os.ReadFile(filepath.Join(dir, ModuleFile)); err != nil { //nolint:gosec // G304: the user's own project
		add("tofu/%s: %v", ModuleFile, err)
	} else if _, ok := Shipped(b); !ok {
		add("tofu/%s isn't one a tailsuite release shipped (edited?): tailsuite upgrade tofu replaces it, and keeps tailnet.auto.tfvars", ModuleFile)
	}
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	slices.Sort(sorted)
	for _, n := range sorted {
		base, ext := split(n)
		switch {
		case n == ModuleFile:
		case slices.Contains(configExts, ext):
			if base == "tailsuite" {
				add("tofu/%s: OpenTofu would load it beside or instead of tailsuite.tf", n)
				continue
			}
			if base == "override" || strings.HasSuffix(base, "_override") {
				add("tofu/%s is an override file, which can change what tailsuite.tf applies", n)
				continue
			}
			// A .tf beside its .tofu twin is checked too: OpenTofu skips
			// it, but Terraform, which reads no .tofu file, applies it.
			problems = append(problems, checkConfig(dir, n)...)
		case slices.Contains(varsExts, ext):
			problems = append(problems, checkVars(dir, n)...)
		}
	}
	return problems
}

// ignored are the files OpenTofu skips: hidden ones and editor leftovers.
func ignored(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, "~") ||
		strings.HasPrefix(name, "#") && strings.HasSuffix(name, "#")
}

// split cuts a file name into its base and its OpenTofu extension.
func split(name string) (base, ext string) {
	for _, e := range append(slices.Clone(configExts), varsExts...) {
		if b, ok := strings.CutSuffix(name, e); ok {
			return b, e
		}
	}
	return name, ""
}

func parse(dir, name string) (hcl.Body, error) {
	src, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: the user's own project
	if err != nil {
		return nil, err
	}
	p := hclparse.NewParser()
	var f *hcl.File
	var diags hcl.Diagnostics
	if strings.HasSuffix(name, ".json") {
		f, diags = p.ParseJSON(src, name)
	} else {
		f, diags = p.ParseHCL(src, name)
	}
	if diags.HasErrors() {
		return nil, diags
	}
	return f.Body, nil
}

// configSchema lists every top-level block OpenTofu knows, so a JSON file
// reads as blocks too; what isn't listed stays in the remainder.
var configSchema = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
	{Type: "terraform"}, {Type: "provider", LabelNames: []string{"name"}},
	{Type: "variable", LabelNames: []string{"name"}}, {Type: "locals"},
	{Type: "output", LabelNames: []string{"name"}}, {Type: "import"},
	{Type: "data", LabelNames: []string{"type", "name"}},
	{Type: "ephemeral", LabelNames: []string{"type", "name"}},
	{Type: "resource", LabelNames: []string{"type", "name"}},
	{Type: "module", LabelNames: []string{"name"}},
	{Type: "moved"}, {Type: "removed"}, {Type: "check", LabelNames: []string{"name"}},
}}

// checkConfig checks a configuration file beside tailsuite.tf: it may
// hold only blocks that apply nothing.
func checkConfig(dir, name string) []string {
	body, err := parse(dir, name)
	if err != nil {
		return []string{fmt.Sprintf("tofu/%s: %v", name, err)}
	}
	content, remain, diags := body.PartialContent(configSchema)
	if diags.HasErrors() {
		return []string{fmt.Sprintf("tofu/%s: %v", name, diags)}
	}
	var problems []string
	if attrs, _ := remain.JustAttributes(); len(attrs) > 0 {
		for _, a := range attrs {
			problems = append(problems, fmt.Sprintf("tofu/%s: %q isn't a block OpenTofu applies nothing with", name, a.Name))
		}
	}
	if sb, ok := remain.(*hclsyntax.Body); ok {
		for _, b := range sb.Blocks {
			if !slices.ContainsFunc(configSchema.Blocks, func(s hcl.BlockHeaderSchema) bool { return s.Type == b.Type }) {
				problems = append(problems, fmt.Sprintf("tofu/%s: a %s block, which tailsuite doesn't know", name, b.Type))
			}
		}
	}
	for _, b := range content.Blocks {
		at := fmt.Sprintf("tofu/%s:%d", name, b.DefRange.Start.Line)
		switch {
		case !slices.Contains(allowedBlocks, b.Type):
			problems = append(problems, fmt.Sprintf("%s: a %s block applies something beside tailsuite.tf, so the lab would differ from production", at, b.Type))
		case (b.Type == "data" || b.Type == "ephemeral") && slices.Contains(refusedSources, b.Labels[0]):
			problems = append(problems, fmt.Sprintf("%s: an %q %s source can run commands or send requests", at, b.Labels[0], b.Type))
		case b.Type == "import":
			problems = append(problems, checkImport(at, b)...)
		case b.Type == "terraform":
			problems = append(problems, checkEncryption(at, b.Body)...)
		}
	}
	return problems
}

// checkImport allows an import block only into tailsuite.tf's resources.
func checkImport(at string, b *hcl.Block) []string {
	attrs, diags := b.Body.JustAttributes()
	if diags.HasErrors() {
		return []string{fmt.Sprintf("%s: an import block: %v", at, diags)}
	}
	to, ok := attrs["to"]
	if !ok {
		return []string{at + ": an import block without to"}
	}
	tr, diags := hcl.AbsTraversalForExpr(to.Expr)
	if diags.HasErrors() || len(tr) < 2 {
		return []string{at + ": an import block's to isn't a resource address"}
	}
	addr := tr.RootName()
	if a, ok := tr[1].(hcl.TraverseAttr); ok {
		addr += "." + a.Name
	}
	if !slices.Contains(importable, addr) {
		return []string{fmt.Sprintf("%s: imports into %s; a file beside tailsuite.tf may import only into %s", at, addr, strings.Join(importable, ", "))}
	}
	return nil
}

var (
	terraformSchema  = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{{Type: "encryption"}}}
	encryptionSchema = &hcl.BodySchema{Blocks: []hcl.BlockHeaderSchema{
		{Type: "key_provider", LabelNames: []string{"type", "name"}},
		{Type: "method", LabelNames: []string{"type", "name"}},
	}}
)

// checkEncryption refuses OpenTofu's external key provider and method in
// a terraform block's state encryption: both run a command.
func checkEncryption(at string, body hcl.Body) []string {
	tf, _, _ := body.PartialContent(terraformSchema)
	var problems []string
	for _, enc := range tf.Blocks {
		content, _, _ := enc.Body.PartialContent(encryptionSchema)
		for _, b := range content.Blocks {
			if b.Labels[0] == "external" {
				problems = append(problems, fmt.Sprintf("%s: an external %s in state encryption runs a command", at, strings.ReplaceAll(b.Type, "_", " ")))
			}
		}
	}
	return problems
}

// checkVars checks a variables file: tailnet.auto.tfvars sets only what
// tailsuite.tf declares, and any other sets none of it, since it would
// override those values (an *.auto.tfvars, in lexical order) or reach
// production through -var-file.
func checkVars(dir, name string) []string {
	body, err := parse(dir, name)
	if err != nil {
		return []string{fmt.Sprintf("tofu/%s: %v", name, err)}
	}
	attrs, diags := body.JustAttributes()
	if diags.HasErrors() {
		return []string{fmt.Sprintf("tofu/%s: %v", name, diags)}
	}
	var problems []string
	for n := range attrs {
		switch {
		case name == ValuesFile && !slices.Contains(Variables, n):
			problems = append(problems, fmt.Sprintf("tofu/%s sets %q, which tailsuite.tf doesn't declare", name, n))
		case name != ValuesFile && slices.Contains(Variables, n):
			problems = append(problems, fmt.Sprintf("tofu/%s sets %q, tailsuite.tf's value, which lives only in %s", name, n, ValuesFile))
		}
	}
	slices.Sort(problems)
	return problems
}

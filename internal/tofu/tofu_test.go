package tofu

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestShipped(t *testing.T) {
	cur := Current()
	if len(cur.Module) == 0 || !strings.Contains(string(cur.Lock), `version     = "`+ProviderVersion+`"`) {
		t.Fatalf("the current release's module or lock file is missing, or the lock doesn't pin %s", ProviderVersion)
	}
	if !strings.Contains(string(cur.Module), `version = "`+ProviderVersion+`"`) {
		t.Errorf("tailsuite.tf doesn't pin the provider at %s", ProviderVersion)
	}
	if r, ok := Shipped(cur.Module); !ok || r.Name != "current" {
		t.Errorf("Shipped(current) = %q, %v", r.Name, ok)
	}
	edited := append(slices.Clone(cur.Module), []byte("\n# a comment\n")...)
	if _, ok := Shipped(edited); ok {
		t.Error("an edited tailsuite.tf counts as shipped")
	}
}

// project writes a tofu/ folder: the shipped module, values, and extra
// files.
func project(t *testing.T, extra map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	tofu := filepath.Join(dir, "tofu")
	files := map[string]string{
		ModuleFile: string(Current().Module),
		ValuesFile: "devices = {\n  server = { tags = [\"tag:prod\"] }\n}\n",
	}
	for k, v := range extra {
		files[k] = v
	}
	if err := os.MkdirAll(tofu, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, body := range files {
		if body == "-" {
			continue // left out
		}
		if err := os.WriteFile(filepath.Join(tofu, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "policy.hujson"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCheckProject(t *testing.T) {
	backend := `terraform {
  backend "s3" { bucket = "synthetic-state" }
}
provider "tailscale" { tailnet = "example.com" }
variable "region" {}
locals { x = 1 }
output "o" { value = 1 }
data "aws_secretsmanager_secret_version" "oauth" { secret_id = "synthetic" }
import {
  to = tailscale_acl.policy
  id = "acl"
}
`
	for name, tc := range map[string]struct {
		extra map[string]string
		want  string // "" means it passes
	}{
		"the shipped module and values":    {nil, ""},
		"production's backend and secrets": {map[string]string{"main.tf": backend, "README.md": "# not loaded", ".terraform.lock.hcl": "x", "notes.txt~": "x"}, ""},
		"no module":                        {map[string]string{ModuleFile: "-"}, "is missing"},
		"an edited module":                 {map[string]string{ModuleFile: string(Current().Module) + "\n# mine\n"}, "isn't one a tailsuite release shipped"},
		"a .tofu twin":                     {map[string]string{"tailsuite.tofu": "# replaces tailsuite.tf"}, "instead of tailsuite.tf"},
		"a resource":                       {map[string]string{"extra.tf": `resource "tailscale_dns_nameservers" "x" { nameservers = ["192.0.2.1"] }`}, "a resource block"},
		"a resource, in JSON":              {map[string]string{"extra.tf.json": `{"resource": {"tailscale_dns_nameservers": {"x": {"nameservers": ["192.0.2.1"]}}}}`}, "a resource block"},
		"a module":                         {map[string]string{"extra.tofu": `module "m" { source = "./m" }`}, "a module block"},
		"an override":                      {map[string]string{"main_override.tf": `locals {}`}, "an override file"},
		"an external data source":          {map[string]string{"x.tf": `data "external" "x" { program = ["sh"] }`}, `"external" data`},
		"an http data source":              {map[string]string{"x.tf": `data "http" "x" { url = "https://example.com" }`}, `"http" data`},
		"a moved block":                    {map[string]string{"x.tf": "moved {\n  from = a.b\n  to = c.d\n}\n"}, "a moved block"},
		"an import elsewhere":              {map[string]string{"x.tf": "import {\n  to = tailscale_dns_nameservers.x\n  id = \"x\"\n}\n"}, "doesn't manage"},
		"values for something else":        {map[string]string{ValuesFile: "devices = {}\nregion = \"x\"\n"}, `sets "region"`},
		"another file overriding values":   {map[string]string{"zz.auto.tfvars": "devices = {}\n"}, "lives only in"},
		"a -var-file overriding values":    {map[string]string{"prod.tfvars.json": `{"devices": {}}`}, "lives only in"},
		"another file's own variables":     {map[string]string{"prod.tfvars": "region = \"x\"\n"}, ""},
		"a .tf beside its .tofu twin":      {map[string]string{"x.tf": `resource "a" "b" {}`, "x.tofu": "locals {}\n"}, ""},
		"a file that doesn't parse":        {map[string]string{"x.tf": "resource {"}, "x.tf"},
	} {
		t.Run(name, func(t *testing.T) {
			problems := CheckProject(filepath.Join(project(t, tc.extra), "tofu"))
			switch {
			case tc.want == "" && len(problems) > 0:
				t.Errorf("refused: %q", problems)
			case tc.want != "" && !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, tc.want) }):
				t.Errorf("problems %q, want one with %q", problems, tc.want)
			}
		})
	}
}

func TestPrepareRun(t *testing.T) {
	dir := project(t, map[string]string{"main.tf": `terraform {
  backend "s3" { bucket = "synthetic-state" }
}
`})
	r, err := PrepareRun(dir, "0123abcd", []byte(`{"grants": []} // the engine's copy`), Lab{Tailnet: "lab-1.example", BaseURL: "https://api.example"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Release.Name != "current" {
		t.Errorf("release %q", r.Release.Name)
	}
	if fi, err := os.Stat(r.Dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("run directory: %v %v", fi, err)
	}
	var got []string
	_ = filepath.WalkDir(r.Dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(r.Dir, p)
			got = append(got, rel)
			if fi, _ := d.Info(); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s: mode %v", rel, fi.Mode().Perm())
			}
		}
		return nil
	})
	slices.Sort(got)
	want := []string{"policy.hujson", "tofu.rc", "tofu/.terraform.lock.hcl", "tofu/lab.tf", "tofu/tailnet.auto.tfvars", "tofu/tailsuite.tf"}
	if !slices.Equal(got, want) {
		t.Errorf("run directory holds %q, want %q (never the project's main.tf, with production's backend)", got, want)
	}
	lab, _ := os.ReadFile(filepath.Join(r.Dir, "tofu", "lab.tf"))
	if !strings.Contains(string(lab), `tailnet  = "lab-1.example"`) || !strings.Contains(string(lab), `backend "local"`) {
		t.Errorf("lab.tf:\n%s", lab)
	}
	if err := r.Remove(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(r.Dir); err == nil {
		t.Error("Remove left the run directory")
	}
}

func TestPrepareRunRefusesAProjectThatFailsTheCheck(t *testing.T) {
	dir := project(t, map[string]string{"x.tf": `resource "a" "b" {}`})
	if _, err := PrepareRun(dir, "0123abcd", nil, Lab{}); err == nil || !strings.Contains(err.Error(), "resource block") {
		t.Errorf("PrepareRun = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".tailsuite")); err == nil {
		t.Error("a refused run left a directory")
	}
}

func TestEnvIsAnAllowList(t *testing.T) {
	r := &Run{Dir: "/p/.tailsuite/run-0123abcd"}
	env := r.Env([]string{
		"PATH=/bin", "HOME=/home/u", "HTTPS_PROXY=http://proxy.example:3128",
		"TF_VAR_devices={}", "TF_CLI_ARGS_apply=-target=x", "TF_CLI_CONFIG_FILE=/home/u/.tofurc",
		"AWS_SECRET_ACCESS_KEY=synthetic", "TAILSCALE_API_KEY=synthetic", "TAILSCALE_TAILNET=prod.example",
		"NOEQUALS",
	}, Credentials{ClientID: "id", ClientSecret: "secret"})
	want := []string{
		"PATH=/bin", "HOME=/home/u", "HTTPS_PROXY=http://proxy.example:3128",
		"TF_CLI_CONFIG_FILE=/p/.tailsuite/run-0123abcd/tofu.rc", "TF_IN_AUTOMATION=1", "TF_INPUT=0",
		"TAILSCALE_OAUTH_CLIENT_ID=id", "TAILSCALE_OAUTH_CLIENT_SECRET=secret",
	}
	if !slices.Equal(env, want) {
		t.Errorf("env = %q\nwant %q", env, want)
	}
}

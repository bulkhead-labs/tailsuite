package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/model"
)

func TestLoadStartup(t *testing.T) {
	p, err := Load("testdata/startup")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range p.Tailnet.Devices {
		names = append(names, d.Name)
		if d.Origin != model.Declared {
			t.Errorf("%s: origin %s", d.Name, d.Origin)
		}
	}
	if !slices.Equal(names, []string{"db", "laptop", "server"}) {
		t.Errorf("devices %v, want sorted db, laptop, server", names)
	}
	db, _ := p.Tailnet.Device("db")
	if !slices.Equal(db.Tags, []string{"tag:db", "tag:prod"}) || db.IPv4 != "100.64.0.10" || db.OS != "linux" {
		t.Errorf("db = %+v", db)
	}
	laptop, _ := p.Tailnet.Device("laptop")
	if laptop.User != "alice@example.com" || len(laptop.Tags) != 0 {
		t.Errorf("laptop = %+v", laptop)
	}
	if got := p.Lab.Devices["server"].Listen; len(got) != 1 || got[0] != (Listen{Proto: "http", Port: 80}) {
		t.Errorf("server listens on %+v", got)
	}
	if p.Lab.Profiles["nightly"].Unproven != "fail" {
		t.Errorf("profiles = %+v", p.Lab.Profiles)
	}
	if p.Tailnet.Policy == nil || len(p.Tailnet.Policy.Policy.Grants) != 2 || len(p.Tailnet.Policy.Unknown) != 0 {
		t.Errorf("policy didn't load as written")
	}
}

func TestModelJSON(t *testing.T) {
	p, err := Load("testdata/startup")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := p.Tailnet.WriteFile(dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".tailsuite", "model.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["schemaVersion"] != float64(model.SchemaVersion) {
		t.Errorf("schemaVersion = %v", doc["schemaVersion"])
	}
	for _, k := range []string{"devices", "policy", "unknownPolicyKeys"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("model.json has no %s", k)
		}
	}
	// Keys sorted: devices' fields come out in alphabetical order.
	i, j := strings.Index(string(b), `"name"`), strings.Index(string(b), `"origin"`)
	if i < 0 || j < 0 || i > j {
		t.Error("model.json's keys aren't sorted")
	}
	// The same project writes the same bytes.
	again := t.TempDir()
	if err := p.Tailnet.WriteFile(again); err != nil {
		t.Fatal(err)
	}
	b2, _ := os.ReadFile(filepath.Join(again, ".tailsuite", "model.json"))
	if string(b) != string(b2) {
		t.Error("model.json isn't deterministic")
	}
}

// project copies the startup project to a temp folder, with files
// replaced or removed (an empty value removes one).
func project(t *testing.T, replace map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for _, f := range []string{PolicyFile, LabFile, TFVarsFile} {
		b, err := os.ReadFile(filepath.Join("testdata/startup", f))
		if err != nil {
			t.Fatal(err)
		}
		if r, ok := replace[f]; ok {
			if r == "" {
				continue
			}
			b = []byte(r)
		}
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, f)), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestLoadChecks(t *testing.T) {
	cases := map[string]struct {
		replace map[string]string
		want    string
	}{
		"a device in tofu/ that lab.yaml lacks": {
			map[string]string{TFVarsFile: `devices = { ghost = { tags = ["tag:prod"] } }`}, "device ghost isn't in lab.yaml"},
		"a device with neither user nor tags": {
			map[string]string{TFVarsFile: ""}, "device server has neither a user nor tags"},
		"a tag that isn't one": {
			map[string]string{TFVarsFile: `devices = { server = { tags = ["prod"] }, db = { tags = ["tag:db"] } }`}, `"prod" isn't a tag`},
		"a device name that isn't a MagicDNS label": {
			map[string]string{LabFile: "version: 1\ndevices:\n  Web_1: { user: alice@example.com }\n"}, `device "Web_1"`},
		"a repeated device": {
			map[string]string{LabFile: "version: 1\ndevices:\n  web: { user: a@example.com }\n  web: { user: b@example.com }\n"}, "already defined"},
		"a key lab.yaml doesn't have": {
			map[string]string{LabFile: "version: 1\ndevicez: {}\n"}, "devicez"},
		"another lab.yaml version": {
			map[string]string{LabFile: "version: 2\n"}, "version 2"},
		"a listener the agent can't serve yet": {
			map[string]string{LabFile: "version: 1\ndevices:\n  db: { user: a@example.com, listen: [{ postgres: 5432 }] }\n", TFVarsFile: ""}, "listen postgres isn't supported yet"},
		"two listeners on one port": {
			map[string]string{LabFile: "version: 1\ndevices:\n  db: { user: a@example.com, listen: [{ tcp: 80 }, { http: 80 }] }\n", TFVarsFile: ""}, "two listeners on port 80"},
		"an address outside Tailscale's range": {
			map[string]string{LabFile: "version: 1\ndevices:\n  db: { user: a@example.com, ip: 192.0.2.1 }\n", TFVarsFile: ""}, "100.64.0.0/10"},
		"an unknown engine": {
			map[string]string{LabFile: "version: 1\ndevices: {}\nprofiles:\n  pr: { engine: netbird }\n", TFVarsFile: ""}, `engine "netbird"`},
		"no policy": {
			map[string]string{PolicyFile: ""}, "policy.hujson"},
		"a policy that isn't HuJSON": {
			map[string]string{PolicyFile: "{"}, "policy.hujson"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(project(t, c.replace))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Load = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

// A tfvars file holds literal values only, and sets only what
// tailsuite.tf declares (0014).
func TestTFVars(t *testing.T) {
	bad := map[string]string{
		"a function call":          `devices = { server = { tags = upper(["tag:prod"]) } }`,
		"a variable reference":     `devices = { server = { tags = var.tags } }`,
		"another variable":         `devices = {}` + "\n" + `policy_file = "../other.hujson"`,
		"a value the module lacks": `devices = { server = { tags = ["tag:prod"], routes = ["192.0.2.0/24"] } }`,
		"tags that aren't a list":  `devices = { server = { tags = "tag:prod" } }`,
		"not HCL":                  `devices = {`,
	}
	for name, src := range bad {
		if _, err := ReadTFVars([]byte(src), "tailnet.auto.tfvars"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	vars, err := ReadTFVars([]byte("# comment\ndevices = {\n  db = { tags = [\"tag:db\"] }\n}\n"), "tailnet.auto.tfvars")
	if err != nil || !slices.Equal(vars.Devices["db"].Tags, []string{"tag:db"}) {
		t.Errorf("a plain file: %+v, %v", vars, err)
	}
}

func TestPolicyUnknownKeysDontFailALoad(t *testing.T) {
	p, err := Load(project(t, map[string]string{PolicyFile: `{"grants": [], "futureSection": {}}`}))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tailnet.Policy.Unknown) != 1 {
		t.Errorf("unknown = %+v", p.Tailnet.Policy.Unknown)
	}
}

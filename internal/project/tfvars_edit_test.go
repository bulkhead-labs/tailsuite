package project

import (
	"strings"
	"testing"
)

func TestSetDevicesKeepsTheRest(t *testing.T) {
	src := []byte(`# Synthetic. Production's device tags.

# The web tier.
devices = {
  server = { tags = ["tag:prod"] }
}
`)
	out, err := SetDevices(src, "tailnet.auto.tfvars", map[string]TFDevice{
		"server": {Tags: []string{"tag:prod"}},
		"db":     {Tags: []string{"tag:prod", "tag:db"}},
		"laptop": {},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Synthetic. Production's device tags.", "# The web tier."} {
		if !strings.Contains(string(out), want) {
			t.Errorf("lost the comment %q:\n%s", want, out)
		}
	}
	back, err := ReadTFVars(out, "tailnet.auto.tfvars")
	if err != nil {
		t.Fatalf("the edit doesn't read back: %v\n%s", err, out)
	}
	if got := back.Devices["db"].Tags; len(got) != 2 || got[1] != "tag:db" {
		t.Errorf("db's tags = %q\n%s", got, out)
	}
	if _, ok := back.Devices["laptop"]; !ok || len(back.Devices) != 3 {
		t.Errorf("devices = %+v", back.Devices)
	}
	if strings.Index(string(out), "db") > strings.Index(string(out), "server") {
		t.Errorf("devices aren't in name order:\n%s", out)
	}
}

func TestSetDevicesRefusesWhatItCantRead(t *testing.T) {
	src := []byte("devices = { server = { tags = var.prod } }\n")
	if _, err := SetDevices(src, "tailnet.auto.tfvars", nil); err == nil {
		t.Error("SetDevices edited a file ReadTFVars refuses")
	}
}

func TestSetNoDevicesRemovesThem(t *testing.T) {
	out, err := SetDevices([]byte("# keep\n\ndevices = { server = { tags = [\"tag:prod\"] } }\n"), "x.tfvars", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(out), "devices") || !strings.Contains(string(out), "# keep") {
		t.Errorf("got %q", out)
	}
}

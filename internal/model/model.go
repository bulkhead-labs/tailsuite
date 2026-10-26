// Package model is the tailnet model of decision 0014: our own types, in
// Tailscale's terms, that every source converts into and every engine is
// configured from.
//
// It holds the policy, the devices and their users, and the tailnet's
// name. It leaves out what isn't the tailnet: lab-only config (listeners,
// profiles), tailsuite's own tests, credentials, device IDs and results.
// DNS and tailnet settings join it in the milestones that use them.
//
// Only inputs are stored. A device's identity (its tags if any, otherwise
// its user), its effective routes and its groups are computed, never
// stored beside their inputs.
package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// SchemaVersion is the version of model.json. It changes whenever the
// file's shape does.
const SchemaVersion = 1

// Origin says where a record came from.
type Origin string

// Origins.
const (
	// Observed: read from Tailscale's API or an OpenTofu plan (M5).
	Observed Origin = "observed"
	// Declared: written in the project, or in a template.
	Declared Origin = "declared"
	// Inferred: filled in by tailsuite, as when a pasted policy names
	// devices it has to guess (#35).
	Inferred Origin = "inferred"
)

// Tailnet is one tailnet.
type Tailnet struct {
	// Name is the tailnet's name, if known.
	Name string `json:"name,omitempty"`

	// Policy is the policy file: the user's bytes and the typed copy.
	Policy *policy.File `json:"-"`

	// Devices are keyed by their MagicDNS name, and sorted by it.
	Devices []Device `json:"devices"`

	// Users are the tailnet's users, by login, sorted.
	Users []User `json:"users"`
}

// User is one user of the tailnet.
type User struct {
	Login string `json:"login"`
	// Roles are the user's tailnet roles, such as admin, which role
	// autogroups name.
	Roles []string `json:"roles,omitempty"`

	Origin Origin `json:"origin"`
	// Inferred lists the fields tailsuite filled in.
	Inferred []string `json:"inferred,omitempty"`
}

// Device is one device of the tailnet.
type Device struct {
	// Name is the device's MagicDNS name: its key in the model.
	Name string `json:"name"`
	// User added the device. Empty for a device that's only tagged.
	User string `json:"user,omitempty"`
	// Tags are the device's tags, which make it a tagged device.
	Tags []string `json:"tags,omitempty"`
	// OS is the operating system the device reports.
	OS string `json:"os,omitempty"`
	// ClientVersion is its Tailscale client's version.
	ClientVersion string `json:"clientVersion,omitempty"`
	// IPv4 is its Tailscale IPv4 address, if pinned.
	IPv4 string `json:"ipv4,omitempty"`

	Origin Origin `json:"origin"`
	// Inferred lists the fields tailsuite filled in.
	Inferred []string `json:"inferred,omitempty"`
}

// Device returns the device with a name.
func (t *Tailnet) Device(name string) (Device, bool) {
	for _, d := range t.Devices {
		if d.Name == name {
			return d, true
		}
	}
	return Device{}, false
}

// document is model.json's shape.
type document struct {
	SchemaVersion int                 `json:"schemaVersion"`
	Name          string              `json:"name,omitempty"`
	Devices       []Device            `json:"devices"`
	Users         []User              `json:"users"`
	Policy        *policy.Policy      `json:"policy"`
	Unknown       []policy.UnknownKey `json:"unknownPolicyKeys"`
}

// MarshalJSON writes the model for the UI, golden tests and fuzzing:
// schema-versioned, with every object's keys sorted.
func (t *Tailnet) MarshalJSON() ([]byte, error) {
	doc := document{SchemaVersion: SchemaVersion, Name: t.Name, Devices: t.Devices, Users: t.Users, Unknown: []policy.UnknownKey{}}
	if doc.Devices == nil {
		doc.Devices = []Device{}
	}
	if doc.Users == nil {
		doc.Users = []User{}
	}
	if t.Policy != nil {
		doc.Policy = &t.Policy.Policy
		if t.Policy.Unknown != nil {
			doc.Unknown = t.Policy.Unknown
		}
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return nil, err
	}
	// Through a generic value and back: encoding/json sorts map keys, so
	// struct fields come out sorted too.
	// UseNumber keeps numbers exactly as written, past float64's precision.
	var generic any
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := dec.Decode(&generic); err != nil {
		return nil, err
	}
	return json.MarshalIndent(generic, "", "  ")
}

// WriteFile writes the model to dir/.tailsuite/model.json. The directory
// is gitignored: the model is never a project file (0014).
func (t *Tailnet) WriteFile(dir string) error {
	b, err := t.MarshalJSON()
	if err != nil {
		return fmt.Errorf("model: %w", err)
	}
	out := filepath.Join(dir, ".tailsuite")
	if err := os.MkdirAll(out, 0o750); err != nil {
		return fmt.Errorf("model: %w", err)
	}
	if err := os.WriteFile(filepath.Join(out, "model.json"), append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("model: %w", err)
	}
	return nil
}

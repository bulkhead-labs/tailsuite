// Package project loads a user's project folder, laid out as decision 0014
// says, into the tailnet model and the lab's own config:
//
//	policy.hujson          the policy, the only copy
//	tofu/tailnet.auto.tfvars  device tags, which OpenTofu applies
//	lab.yaml               the devices as the lab rebuilds them, listeners, profiles
//
// A fact lives in the file for whatever makes it true in production, so
// each fact has one home.
package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// The project's files, relative to its folder.
const (
	PolicyFile = "policy.hujson"
	LabFile    = "lab.yaml"
	TFVarsFile = "tofu/tailnet.auto.tfvars"
)

// Project is a loaded project.
type Project struct {
	Dir string
	// Tailnet is the model: the policy and the devices.
	Tailnet model.Tailnet
	// Lab is lab.yaml, which the planner reads beside the model.
	Lab Lab
}

// Load reads a project folder and checks it, as 0014 says a load does:
//
//   - every device named in tofu/ exists in lab.yaml;
//   - every device has tags or a user;
//   - names are unique.
//
// Every problem found is reported, each naming its file and device. A
// policy key the typed copy doesn't understand isn't a load error: it's
// listed in the model, and decides what an engine may do (0014).
func Load(dir string) (*Project, error) {
	raw, err := os.ReadFile(filepath.Join(dir, PolicyFile)) //nolint:gosec // G304: the user's own project
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	pol, err := policy.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("project: %s: %w", PolicyFile, err)
	}

	labRaw, err := os.ReadFile(filepath.Join(dir, LabFile)) //nolint:gosec // G304: the user's own project
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	lab, err := readLabBytes(labRaw)
	if err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}

	tfvars := TFVars{Devices: map[string]TFDevice{}}
	tfRaw, err := os.ReadFile(filepath.Join(dir, TFVarsFile)) //nolint:gosec // G304: the user's own project
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No tags: every device needs a user, which the checks below say.
	case err != nil:
		return nil, fmt.Errorf("project: %w", err)
	default:
		if tfvars, err = ReadTFVars(tfRaw, TFVarsFile); err != nil {
			return nil, fmt.Errorf("project: %w", err)
		}
	}

	if err := check(lab, tfvars); err != nil {
		return nil, fmt.Errorf("project: %w", err)
	}
	return &Project{Dir: dir, Lab: lab, Tailnet: build(pol, lab, tfvars)}, nil
}

func check(lab Lab, tfvars TFVars) error {
	var errs []error
	for _, name := range sortedKeys(tfvars.Devices) {
		if _, ok := lab.Devices[name]; !ok {
			errs = append(errs, fmt.Errorf("%s: device %s isn't in %s", TFVarsFile, name, LabFile))
		}
		for _, tag := range tfvars.Devices[name].Tags {
			if !strings.HasPrefix(tag, "tag:") || len(tag) == len("tag:") {
				errs = append(errs, fmt.Errorf("%s: device %s: %q isn't a tag, such as tag:prod", TFVarsFile, name, tag))
			}
		}
	}
	for _, name := range sortedKeys(lab.Devices) {
		if lab.Devices[name].User == "" && len(tfvars.Devices[name].Tags) == 0 {
			errs = append(errs, fmt.Errorf("%s: device %s has neither a user nor tags (in %s)", LabFile, name, TFVarsFile))
		}
	}
	// Names are unique: lab.yaml's decoder refuses a repeated key, and
	// MagicDNS names are lower-case, so two spellings of one name can't
	// both pass.
	return errors.Join(errs...)
}

func build(pol *policy.File, lab Lab, tfvars TFVars) model.Tailnet {
	t := model.Tailnet{Policy: pol}
	for _, name := range sortedKeys(lab.Devices) {
		d := lab.Devices[name]
		tags := slices.Clone(tfvars.Devices[name].Tags)
		slices.Sort(tags)
		t.Devices = append(t.Devices, model.Device{
			Name:          name,
			User:          d.User,
			Tags:          tags,
			OS:            d.OS,
			ClientVersion: d.Version,
			IPv4:          d.IP,
			Origin:        model.Declared,
		})
	}
	return t
}

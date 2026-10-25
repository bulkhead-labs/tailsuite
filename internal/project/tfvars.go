package project

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
)

// TFVars is what tailsuite reads from tofu/tailnet.auto.tfvars: the
// values tailsuite's module applies (0014). In M1, each device's tags.
type TFVars struct {
	Devices map[string]TFDevice
}

// TFDevice is one device's values.
type TFDevice struct {
	Tags []string
}

// tfvarsVariables are the variables tailsuite.tf declares in M1. DNS and
// settings join as their milestones land.
var tfvarsVariables = []string{"devices"}

// ReadTFVars reads a tfvars file without running OpenTofu. A tfvars file
// holds literal values only (0014): with no evaluation context, a
// variable or function call is an error, as OpenTofu makes it.
func ReadTFVars(src []byte, filename string) (TFVars, error) {
	vars := TFVars{Devices: map[string]TFDevice{}}
	file, diags := hclparse.NewParser().ParseHCL(src, filename)
	if diags.HasErrors() {
		return vars, fmt.Errorf("%s: %w", filename, diags)
	}
	attrs, diags := file.Body.JustAttributes()
	if diags.HasErrors() {
		return vars, fmt.Errorf("%s: %w", filename, diags)
	}
	var errs []error
	for _, name := range sortedKeys(attrs) {
		attr := attrs[name]
		if !slices.Contains(tfvarsVariables, name) {
			errs = append(errs, fmt.Errorf("%s: sets %q, which tailsuite.tf doesn't declare; it sets only %s", filename, name, strings.Join(tfvarsVariables, ", ")))
			continue
		}
		val, diags := attr.Expr.Value(nil)
		if diags.HasErrors() {
			errs = append(errs, fmt.Errorf("%s: %s: %w", filename, name, literalOnly(diags)))
			continue
		}
		devices, err := readDevices(val)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: devices: %w", filename, err))
			continue
		}
		vars.Devices = devices
	}
	return vars, errors.Join(errs...)
}

// literalOnly explains what OpenTofu would say about a non-literal value.
func literalOnly(diags hcl.Diagnostics) error {
	return fmt.Errorf("a tfvars file holds literal values only: %w", diags)
}

func readDevices(v cty.Value) (map[string]TFDevice, error) {
	if v.IsNull() || !v.IsKnown() || (!v.Type().IsObjectType() && !v.Type().IsMapType()) {
		return nil, errors.New("want a map from device name to { tags = [...] }")
	}
	out := map[string]TFDevice{}
	var errs []error
	for it := v.ElementIterator(); it.Next(); {
		k, dv := it.Element()
		name := k.AsString()
		if dv.IsNull() || (!dv.Type().IsObjectType() && !dv.Type().IsMapType()) {
			errs = append(errs, fmt.Errorf("%s: want { tags = [...] }", name))
			continue
		}
		var d TFDevice
		for fit := dv.ElementIterator(); fit.Next(); {
			fk, fv := fit.Element()
			switch fk.AsString() {
			case "tags":
				tags, err := stringList(fv)
				if err != nil {
					errs = append(errs, fmt.Errorf("%s: tags: %w", name, err))
				}
				d.Tags = tags
			default:
				errs = append(errs, fmt.Errorf("%s: %q isn't a value tailsuite.tf takes; it takes tags", name, fk.AsString()))
			}
		}
		out[name] = d
	}
	return out, errors.Join(errs...)
}

func stringList(v cty.Value) ([]string, error) {
	if v.IsNull() || (!v.Type().IsTupleType() && !v.Type().IsListType() && !v.Type().IsSetType()) {
		return nil, errors.New("want a list of strings")
	}
	var out []string
	for it := v.ElementIterator(); it.Next(); {
		_, s := it.Element()
		if s.IsNull() || s.Type() != cty.String {
			return nil, errors.New("want a list of strings")
		}
		out = append(out, s.AsString())
	}
	return out, nil
}

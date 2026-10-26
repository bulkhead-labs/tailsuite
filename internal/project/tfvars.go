package project

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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

// ReadTFVars reads a tfvars file without running OpenTofu. tailsuite takes
// literal values only (0014), stricter than OpenTofu, which also accepts
// constant expressions such as templates and for loops: #9 edits this
// file with hclwrite, which needs what's written to be what's meant. A
// variable, function call or any expression is an error, and so is a
// name or key written twice.
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
		if err := literal(attr.Expr); err != nil {
			errs = append(errs, fmt.Errorf("%s: %s: %w", filename, name, err))
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

// literal accepts only literal syntax: objects with literal keys, written
// once each; lists; strings with no interpolation; numbers and booleans.
func literal(expr hcl.Expression) error {
	switch e := expr.(type) {
	case *hclsyntax.LiteralValueExpr:
		return nil
	case *hclsyntax.TemplateExpr:
		if len(e.Parts) == 0 {
			return nil
		}
		if len(e.Parts) == 1 {
			if _, ok := e.Parts[0].(*hclsyntax.LiteralValueExpr); ok {
				return nil
			}
		}
		return fmt.Errorf("line %d: a string with interpolation; want a literal value", e.SrcRange.Start.Line)
	case *hclsyntax.TupleConsExpr:
		for _, item := range e.Exprs {
			if err := literal(item); err != nil {
				return err
			}
		}
		return nil
	case *hclsyntax.ObjectConsExpr:
		seen := map[string]bool{}
		for _, item := range e.Items {
			key, err := literalKey(item.KeyExpr)
			if err != nil {
				return err
			}
			if seen[key] {
				return fmt.Errorf("line %d: %q is written twice", item.KeyExpr.Range().Start.Line, key)
			}
			seen[key] = true
			if err := literal(item.ValueExpr); err != nil {
				return err
			}
		}
		return nil
	default:
		return fmt.Errorf("line %d: an expression; a tfvars file for tailsuite holds literal values only", expr.Range().Start.Line)
	}
}

func literalKey(expr hcl.Expression) (string, error) {
	if k, ok := expr.(*hclsyntax.ObjectConsKeyExpr); ok {
		if k.ForceNonLiteral {
			return "", fmt.Errorf("line %d: a computed key; want a literal name", expr.Range().Start.Line)
		}
		if name := hcl.ExprAsKeyword(k.Wrapped); name != "" {
			return name, nil
		}
		expr = k.Wrapped
	}
	if err := literal(expr); err != nil {
		return "", err
	}
	if _, ok := expr.(*hclsyntax.TemplateExpr); !ok {
		return "", fmt.Errorf("line %d: a key that isn't a name or a string", expr.Range().Start.Line)
	}
	v, diags := expr.Value(nil)
	if diags.HasErrors() || v.Type() != cty.String {
		return "", fmt.Errorf("line %d: a key that isn't a name or a string", expr.Range().Start.Line)
	}
	return v.AsString(), nil
}

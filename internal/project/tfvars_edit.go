package project

import (
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

// SetDevices writes the devices variable into a tfvars file with hclwrite,
// keeping everything else in it, comments included, and returns the new
// file. The devices map itself is rewritten whole, in name order:
// comments inside it aren't kept, and with no devices
// the variable is removed, with the comment lines directly above it. The file must read
// with ReadTFVars first, so an edit never hides a value tailsuite
// couldn't read.
func SetDevices(src []byte, filename string, devices map[string]TFDevice) ([]byte, error) {
	if _, err := ReadTFVars(src, filename); err != nil {
		return nil, err
	}
	f, diags := hclwrite.ParseConfig(src, filename, hcl.InitialPos)
	if diags.HasErrors() {
		return nil, fmt.Errorf("%s: %w", filename, diags)
	}
	if len(devices) == 0 {
		f.Body().RemoveAttribute("devices")
		return f.Bytes(), nil
	}
	f.Body().SetAttributeRaw("devices", devicesTokens(devices))
	return hclwrite.Format(f.Bytes()), nil
}

// devicesTokens is the devices map as literal HCL:
//
//	{
//	  server = {
//	    tags = ["tag:prod"]
//	  }
//	}
func devicesTokens(devices map[string]TFDevice) hclwrite.Tokens {
	obj := map[string]cty.Value{}
	for _, name := range slices.Sorted(maps.Keys(devices)) {
		list := cty.ListValEmpty(cty.String)
		if tags := devices[name].Tags; len(tags) > 0 {
			vals := make([]cty.Value, len(tags))
			for i, t := range tags {
				vals[i] = cty.StringVal(t)
			}
			list = cty.ListVal(vals)
		}
		obj[name] = cty.ObjectVal(map[string]cty.Value{"tags": list})
	}
	return hclwrite.TokensForValue(cty.ObjectVal(obj))
}

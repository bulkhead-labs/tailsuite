package policy

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// FuzzParse checks what 0014 relies on, for any input that parses:
// parsing again gives the same typed copy and the same unknown keys;
// every unknown key and every reference points at a place in the file;
// and a reference's span holds its value.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"default", "segmented", "everything", "unknown"} {
		raw, err := os.ReadFile("testdata/" + name + ".hujson")
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	f.Add([]byte(`{"ACLs":[{"Action":"accept","Users":["a@b"],"Ports":["tag:x:22"]}]}`))
	f.Add([]byte(`{"groups":{"group:a":["x@y"],"group:A":["z@y"]},"groups":{}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		first, err := Parse(raw)
		if err != nil {
			return
		}
		again, err := Parse(first.Raw)
		if err != nil {
			t.Fatalf("parsing again failed: %v", err)
		}
		if !reflect.DeepEqual(first.Policy, again.Policy) || !reflect.DeepEqual(first.Unknown, again.Unknown) {
			t.Fatal("parsing again gave a different result")
		}
		for _, u := range first.Unknown {
			if u.Start < 0 || u.End > len(raw) || u.Start >= u.End {
				t.Fatalf("unknown key %s has span %d-%d", u.Pointer, u.Start, u.End)
			}
		}
		for _, r := range first.References() {
			start, end, ok := first.Span(r.Pointer)
			if !ok {
				t.Fatalf("reference %s doesn't resolve", r.Pointer)
			}
			if r.Role == "defines" {
				continue // points at the key's value; the name is the key
			}
			var s string
			if err := json.Unmarshal(raw[start:end], &s); err != nil || (s != r.Value && stripPort(s) != r.Value) {
				t.Fatalf("reference %s's span %q doesn't hold %q (%v)", r.Pointer, raw[start:end], r.Value, err)
			}
		}
	})
}

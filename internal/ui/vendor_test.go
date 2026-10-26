package ui

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"testing"
)

// The vendored libraries, as recorded when they were vendored
// (2026-10-26, decision 0017): npm's tarballs, checked against the
// registry's integrity hashes. hooks.mjs has one change, its bare import
// of preact rewritten to ./preact.mjs.
var vendored = map[string]string{
	"preact.mjs":    "3a3201e9ccef7d30ef1273427f1b17ef8323e464b2c8d73b4fd5636af730433e", // preact 11.0.1, dist/preact.mjs
	"hooks.mjs":     "df45d3c5b39fc012b2397a64d4056ef06d3fbde676c9567a7c12d4d783b6bda3", // preact 11.0.1, hooks/dist/hooks.mjs
	"htm.mjs":       "ab33dd3f38059b9be4d5f5350128eefb2356639c4e0bbe9d9e8b3ba75847e9e4", // htm 3.1.1, dist/htm.mjs
	"cytoscape.mjs": "c299a4d5de7af08dfa7813f8eec4e2a2ef4f700f490f4505b408a0ce57e8e816", // cytoscape 3.34.3, dist/cytoscape.esm.min.mjs
}

func TestVendoredFiles(t *testing.T) {
	entries, err := fs.ReadDir(assets, "assets/vendor")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, e := range entries {
		want, ok := vendored[e.Name()]
		if !ok {
			if e.Name() != "LICENSES.txt" {
				t.Errorf("vendor/%s has no recorded hash", e.Name())
			}
			continue
		}
		b, err := fs.ReadFile(assets, "assets/vendor/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(b)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("vendor/%s: sha256 %s, want %s", e.Name(), got, want)
		}
		seen++
	}
	if seen != len(vendored) {
		t.Errorf("found %d of the %d vendored files", seen, len(vendored))
	}
}

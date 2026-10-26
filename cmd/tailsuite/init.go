package main

import (
	"fmt"
	"io"
	"regexp"

	"github.com/bulkhead-labs/tailsuite/internal/templates"
	"github.com/bulkhead-labs/tailsuite/internal/version"
)

func cmdInit(args []string, stdout, stderr io.Writer) int {
	fs := flags("init", stderr)
	tmpl := fs.String("template", "startup", "the template to start from")
	nix := fs.Bool("nix", false, "also write flake.nix, pinning this tailsuite and OpenTofu")
	if ok, code := parse(fs, args, 1); !ok {
		return code
	}
	dir := "."
	if fs.NArg() == 1 {
		dir = fs.Arg(0)
	}
	files, err := templates.Files(*tmpl)
	if err != nil {
		return fail(stderr, err)
	}
	if *nix {
		flake, err := flakeFor(version.String())
		if err != nil {
			return fail(stderr, err)
		}
		files["flake.nix"] = flake
	}
	if err := templates.Write(dir, files); err != nil {
		return fail(stderr, err)
	}
	next := "tailsuite up, then tailsuite test"
	if dir != "." {
		next = "cd " + dir + ", then " + next
	}
	return wrote(fmt.Fprintf(stdout, "Wrote the %s template to %s. Next: %s.\n", *tmpl, dir, next))
}

// release is a version a flake can pin by tag.
var release = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

// flakeFor is a dev shell with this release of tailsuite and OpenTofu,
// from the nixpkgs tailsuite's own flake locks. `nix develop` writes
// flake.lock, which pins both.
func flakeFor(v string) ([]byte, error) {
	if !release.MatchString(v) {
		return nil, fmt.Errorf("--nix pins this tailsuite by its release tag, and this build is %s, not a release; install a release, or leave out --nix", v)
	}
	return []byte(`{
  description = "A tailsuite project";

  # Written by tailsuite init. flake.lock pins both inputs.
  inputs = {
    tailsuite.url = "github:bulkhead-labs/tailsuite/` + v + `";
    nixpkgs.follows = "tailsuite/nixpkgs";
  };

  outputs =
    { tailsuite, nixpkgs, ... }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f system nixpkgs.legacyPackages.${system});
    in
    {
      devShells = forAllSystems (
        system: pkgs: {
          default = pkgs.mkShell {
            packages = [
              tailsuite.packages.${system}.tailsuite
              pkgs.opentofu
            ];
          };
        }
      );
    };
}
`), nil
}

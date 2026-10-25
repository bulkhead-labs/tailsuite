{
  description = "tailsuite: prove what your tailnet allows";

  # nixos-unstable, locked to a channel release at least two weeks old
  # (decision 0018). Its Go tools are built with Go 1.27, as the module is.
  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "x86_64-linux"
        "aarch64-linux"
        "x86_64-darwin"
        "aarch64-darwin"
      ];
      forAllSystems = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      version = "0.0.0-${self.shortRev or self.dirtyShortRev or "unknown"}";

      # What CI runs. The dev shell adds the rest of the toolchain.
      ciTools =
        pkgs: with pkgs; [
          go_1_27
          golangci-lint
          lychee
          actionlint
          zizmor
          shellcheck # actionlint checks run: scripts with it
          nixfmt
        ];
    in
    {
      packages = forAllSystems (pkgs: {
        default = self.packages.${pkgs.stdenv.hostPlatform.system}.tailsuite;
        tailsuite = pkgs.buildGo127Module {
          pname = "tailsuite";
          inherit version;
          src =
            with pkgs.lib.fileset;
            toSource {
              root = ./.;
              fileset = unions [
                ./go.mod
                (maybeMissing ./go.sum)
                ./cmd
                ./internal
              ];
            };
          # The hash of the Go dependencies. Every change to go.sum changes
          # it: set it to the hash `nix build` reports. CI fails until it
          # matches.
          vendorHash = "sha256-+7gmw90VV15r2l0I90yKv2INEIVT9/03OYCC8KFoKPc=";
          subPackages = [ "cmd/tailsuite" ];
          env.CGO_ENABLED = "0";
          ldflags = [
            "-s"
            "-w"
            "-X github.com/bulkhead-labs/tailsuite/internal/version.version=${version}"
          ];
          meta = {
            description = "Prove what your tailnet allows, with real traffic in a local lab";
            homepage = "https://github.com/bulkhead-labs/tailsuite";
            license = pkgs.lib.licenses.asl20;
            mainProgram = "tailsuite";
          };
        };
      });

      # Plain `go build` works without any of this.
      devShells = forAllSystems (pkgs: {
        default = pkgs.mkShell {
          packages =
            ciTools pkgs
            ++ (with pkgs; [
              gopls
              goreleaser
              opentofu
              docker-client
              gh
              jq
            ]);
          env.CGO_ENABLED = "0";
        };
        ci = pkgs.mkShell {
          packages = ciTools pkgs;
          env.CGO_ENABLED = "0";
        };
      });

      checks = forAllSystems (pkgs: {
        tailsuite = self.packages.${pkgs.stdenv.hostPlatform.system}.tailsuite;
      });

      formatter = forAllSystems (pkgs: pkgs.nixfmt);
    };
}

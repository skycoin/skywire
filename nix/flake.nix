{
  description = "Skywire packaged for Nix — source build (static musl) and prebuilt-binary variants";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        # Skywire ships without an explicit LICENSE file (the AUR
        # PKGBUILD tags it "license-free"), so Nix's policy treats
        # it as unfree by default and refuses to build it without an
        # opt-in. Whitelist exactly skywire + skywire-bin here so
        # `nix build` works on a stock Nix installation without
        # exporting NIXPKGS_ALLOW_UNFREE=1 / --impure.
        pkgs = import nixpkgs {
          inherit system;
          config.allowUnfreePredicate = pkg:
            builtins.elem (nixpkgs.lib.getName pkg) [
              "skywire"
              "skywire-bin"
            ];
        };

        # Source-build version comes from the flake's own git
        # metadata — analogous to the Makefile's
        # `git describe --always`. Clean tree → short commit SHA;
        # dirty tree → short SHA with a `-dirty` suffix; no git info
        # at all → "dev". For a tagged release build, override `rev`
        # in the call below (or pass `version` directly) and
        # skywire.nix will strip the leading "v" off the rev.
        sourceVersion =
          if self ? shortRev then self.shortRev
          else if self ? dirtyShortRev then self.dirtyShortRev
          else "dev";

        # The binary build downloads a release tarball whose URL
        # encodes the semver, so this one stays a hand-written
        # constant. Bump on each release. The source build does not
        # use this — it derives version from git above.
        binaryVersion = "1.3.50";

        skywire = pkgs.callPackage ./skywire.nix {
          version = sourceVersion;
          # Default: build from the local working tree (../..). To
          # build a tagged release from upstream instead, override:
          #
          #   nix build .#skywire \
          #     --override-input src \
          #     'github:skycoin/skywire/v1.3.50'
        };

        skywire-bin = pkgs.callPackage ./skywire-bin.nix {
          version = binaryVersion;
          # Fill in real hashes here once the release is cut. First
          # `nix build .#skywire-bin` will print the expected hash
          # for whichever arch you're on; copy it in.
          #
          # hashes = {
          #   "linux-amd64"   = "sha256-...";
          #   "linux-arm64"   = "sha256-...";
          #   "linux-386"     = "sha256-...";
          #   "linux-armhf"   = "sha256-...";
          #   "linux-arm"     = "sha256-...";
          #   "linux-riscv64" = "sha256-...";
          # };
        };
      in {
        packages = {
          inherit skywire skywire-bin;
          default = skywire;
        };

        # `nix run .#skywire-cli ...` etc. without typing the bin path.
        apps = {
          skywire = {
            type = "app";
            program = "${skywire}/bin/skywire";
          };
          skywire-cli = {
            type = "app";
            program = "${skywire}/bin/skywire-cli";
          };
          skywire-visor = {
            type = "app";
            program = "${skywire}/bin/skywire-visor";
          };
          skywire-bin = {
            type = "app";
            program = "${skywire-bin}/bin/skywire";
          };
          default = self.apps.${system}.skywire;
        };

        # `nix develop` drops you into a shell with the toolchain
        # the source build needs (Go, musl, the standard CLI deps),
        # mirroring `make build-static` requirements.
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            go
            musl
            pkg-config
            gnumake
            git
          ];
        };
      });
}

# Skywire — Nix packaging

Two derivations, mirroring the AUR `skywire` and `skywire-bin`
packages:

- **`skywire.nix`** — source build. Uses `pkgsStatic.buildGoModule`
  (musl-based stdenv), with the same `-linkmode external -extldflags
  "-static" -buildid=` ldflags as the upstream `make build-static`
  recipe. Produces a fully-static binary identical in shape to the
  release tarballs.

- **`skywire-bin.nix`** — install the prebuilt release tarball from
  GitHub releases. Faster build, trusts the upstream binary.

Both produce the same layout:

    $out/bin/skywire           # merged binary
    $out/bin/skywire-cli       # shim → skywire cli
    $out/bin/skywire-visor     # shim → skywire visor
    $out/share/skywire/apps/   # shims for skychat, skysocks, vpn-*, etc.

## Quick start

From a checkout of this repo:

    cd nix
    nix build .#skywire        # source build (static musl)
    nix build .#skywire-bin    # download prebuilt
    nix run   .#skywire -- --bv

Or as a flake input from somewhere else:

    {
      inputs.skywire.url = "github:0pcom/skywire/nix/packaging";
      # …
      outputs = { self, nixpkgs, skywire, ... }: {
        # nixosConfigurations / systemPackages / etc.
        environment.systemPackages = [
          skywire.packages.${system}.skywire
        ];
      };
    }

## First-time hashes

`skywire-bin.nix` ships with `lib.fakeHash` placeholders for every
arch's release tarball. The first `nix build .#skywire-bin` will
fail and print the expected hash for the arch you're on; copy it
into the `hashes` attrset in `flake.nix` and rebuild. Repeat for
each arch you want to support.

`skywire.nix` defaults to building from the **local working tree**
(handy for iterating on a branch). To build a tagged release from
upstream instead, override `rev` and `version` when calling it from
the flake (the parameter for an explicit src tree is `srcOverride`,
to dodge a `pkgs.src` callPackage auto-bind collision in nixpkgs):

    pkgs.callPackage ./skywire.nix {
      rev     = "v1.3.50";
      version = "1.3.50";
    }

The first such build will fail with the expected `hash` for
`fetchFromGitHub`; copy it into the `hash = lib.fakeHash` line in
`skywire.nix`.

## Visor app discovery

The visor launches `skychat`, `skysocks`, `vpn-server`, etc. as
separate processes. Both packages install dispatcher shims under
`$out/share/skywire/apps/`. Point the visor at that directory:

    skywire visor --apps-dir ${skywire}/share/skywire/apps …

or, in a config file, set `launcher.bin_path` to the same path.

## NixOS module — TODO

A `nixosModules.skywire` exposing `services.skywire.enable`,
service-tree toggles (sn / sd / tpd / dmsg / ar / rf), and the
autoconfig wiring is the natural follow-up. Not in this initial
drop. Once it lands, you'll be able to:

    services.skywire = {
      enable  = true;
      package = pkgs.skywire;       # or skywire-bin
      services.tpd.enable = true;
      # …
    };

## Validating the build

To sanity-check the source build is actually static:

    nix build .#skywire
    file ./result/bin/skywire
    # → ELF 64-bit LSB executable, x86-64, statically linked, …

If `file` reports `dynamically linked`, the build's `postInstall`
guard already would have failed.

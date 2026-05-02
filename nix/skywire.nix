{
  lib,
  pkgsStatic,
  buildGoModule,
  fetchFromGitHub,
  runtimeShell,
  rev ? null,
  version ? "1.3.50",
  src ? null,
}:

# skywire — static musl source build of the merged binary.
#
# Mirrors the AUR `skywire/PKGBUILD` and the upstream
# `make build-static` recipe:
#
#   CC=musl-gcc go install -trimpath \
#     --ldflags '-linkmode external -extldflags "-static" -buildid=' .
#
# pkgsStatic provides the musl-based stdenv + Go that buildGoModule
# uses, so the produced binary is fully statically linked. We add
# back the BUILDINFO ldflags the upstream Makefile injects so
# `skywire --bv` reports a real version (the static target in the
# Makefile drops them; we don't have to).
#
# Outputs:
#   $out/bin/skywire           — the merged binary
#   $out/bin/skywire-cli       — shim → skywire cli
#   $out/bin/skywire-visor     — shim → skywire visor
#   $out/share/skywire/apps/<name> — shims for each native app
#                                    (skywire app <name>) so the
#                                    visor can launch them with the
#                                    legacy "external apps directory"
#                                    layout.
#
# The visor's BinPath defaults to "./apps" relative to its working
# directory; point it at $out/share/skywire/apps with
#   visor.binPath = "${pkgs.skywire}/share/skywire/apps"
# in the NixOS module (or `--apps-dir` on the command line).

let
  selfSrc =
    if src != null
    then src
    else if rev != null
    then
      fetchFromGitHub {
        owner = "skycoin";
        repo = "skywire";
        inherit rev;
        # Replace with `nix-prefetch-github skycoin skywire --rev <rev>`.
        hash = lib.fakeHash;
      }
    else
      # Default: build from the local working tree so `nix build`
      # in this checkout exercises the current branch's code.
      ../.;

  # Apps that the visor launches as separate binaries; the merged
  # `skywire` exposes each as `skywire app <name>`. We install
  # shim scripts under $out/share/skywire/apps/<name> so the visor
  # can find them by the legacy filename.
  apps = [
    "skychat"
    "skysocks"
    "skysocks-client"
    "vpn-server"
    "vpn-client"
    "skynet-srv"
    "skynet-client"
  ];
in
pkgsStatic.buildGoModule {
  pname = "skywire";
  inherit version;
  src = selfSrc;

  # The repo vendors all dependencies (`vendor/`); skip the module
  # download phase and use them directly. If vendor/ is removed
  # upstream, replace with a real `vendorHash`.
  vendorHash = null;

  # `cmd/skywire/skywire.go` is the merged-binary entrypoint; the
  # repo root has no `package main`. Same target the AUR builds:
  #   go install github.com/skycoin/skywire/cmd/skywire@v…
  subPackages = [ "cmd/skywire" ];

  # Static-link via musl + the same -extldflags '-static' the
  # upstream Makefile uses. CGO_ENABLED is forced on because the
  # external linker only fires when cgo is in play.
  env.CGO_ENABLED = "1";

  ldflags = [
    "-s"
    "-w"
    "-linkmode" "external"
    "-extldflags" "-static"
    "-buildid="
    "-X" "github.com/skycoin/skywire/pkg/buildinfo.version=v${version}"
    "-X" "github.com/skycoin/skywire/pkg/visor.BuildTag=nix_static"
  ];

  # `go build .` produces a binary named after the repo dir
  # (`skywire`) — keep it; the AUR shims expect `skywire` too.
  postInstall = ''
    # Sanity-check the binary actually came out static. Static
    # ELFs lack a PT_INTERP segment; readelf reports "Requesting
    # program interpreter" only on dynamic executables. Anything
    # dynamic here would silently break portability of the closure.
    if ${pkgsStatic.buildPackages.binutils-unwrapped}/bin/readelf -l $out/bin/skywire \
        | grep -q 'Requesting program interpreter'; then
      echo "skywire binary is dynamically linked — static build failed" >&2
      exit 1
    fi

    # CLI / visor wrapper shims (mirrors AUR layout).
    cat > $out/bin/skywire-cli <<EOF
    #!${runtimeShell}
    exec $out/bin/skywire cli "\$@"
    EOF
    cat > $out/bin/skywire-visor <<EOF
    #!${runtimeShell}
    exec $out/bin/skywire visor "\$@"
    EOF
    chmod +x $out/bin/skywire-cli $out/bin/skywire-visor

    # App shims — one per launchable native app. The visor calls
    # these as ordinary executables, so they're plain dispatchers
    # to `skywire app <name>`.
    install -d $out/share/skywire/apps
  '' + lib.concatMapStringsSep "\n" (app: ''
    cat > $out/share/skywire/apps/${app} <<EOF
    #!${runtimeShell}
    exec $out/bin/skywire app ${app} "\$@"
    EOF
    chmod +x $out/share/skywire/apps/${app}
  '') apps;

  doCheck = false; # upstream test suite needs a network + redis

  meta = with lib; {
    description = "Skywire — software-defined networking with public keys (static musl build)";
    homepage = "https://github.com/skycoin/skywire";
    license = licenses.unfree; # upstream PKGBUILD says license-free
    mainProgram = "skywire";
    platforms = [
      "x86_64-linux"
      "aarch64-linux"
      "armv7l-linux"
      "i686-linux"
      "riscv64-linux"
    ];
  };
}

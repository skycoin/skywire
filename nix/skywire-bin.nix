{
  lib,
  stdenvNoCC,
  fetchurl,
  runtimeShell,
  version ? "1.3.50",
  hashes ? null,
}:

# skywire-bin — install the statically-linked release binary
# Skycoin publishes on GitHub. Mirrors the AUR `skywire-bin/PKGBUILD`:
# fetch tarball for the current arch, drop the `skywire` binary into
# $out/bin, and create the same wrapper shims.
#
# Release binaries are built static-musl upstream (same recipe as
# `skywire.nix`), so no autoPatchelfHook is needed — they run on
# any glibc/musl host.
#
# `hashes` is an attrset keyed by the `linux-<arch>` suffix used in
# the release filename:
#
#   {
#     "linux-amd64"   = "sha256-...";
#     "linux-arm64"   = "sha256-...";
#     "linux-386"     = "sha256-...";
#     "linux-armhf"   = "sha256-...";
#     "linux-arm"     = "sha256-...";
#     "linux-riscv64" = "sha256-...";
#   }
#
# When omitted, defaults to `lib.fakeHash` for every arch — first
# real build will fail with the correct expected hash, fill them
# in. The flake at nix/flake.nix exposes a single `skywire-bin`
# package per system; this file is the per-system derivation.

let
  # Map nixpkgs `system` strings to the suffix Skycoin uses in
  # release filenames. Returns null on systems with no published
  # binary so the caller can short-circuit (we evaluate to a
  # broken-package on those rather than failing eval).
  archSuffixFor = system:
    {
      "x86_64-linux"   = "linux-amd64";
      "aarch64-linux"  = "linux-arm64";
      "i686-linux"     = "linux-386";
      "armv7l-linux"   = "linux-armhf";
      "armv6l-linux"   = "linux-arm";
      "riscv64-linux"  = "linux-riscv64";
    }.${system} or null;

  archSuffix = archSuffixFor stdenvNoCC.hostPlatform.system;

  effectiveHashes =
    if hashes != null then hashes
    else {
      "linux-amd64"   = lib.fakeHash;
      "linux-arm64"   = lib.fakeHash;
      "linux-386"     = lib.fakeHash;
      "linux-armhf"   = lib.fakeHash;
      "linux-arm"     = lib.fakeHash;
      "linux-riscv64" = lib.fakeHash;
    };

  hash =
    if archSuffix == null
    then null
    else effectiveHashes.${archSuffix} or lib.fakeHash;

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

if archSuffix == null then
  throw "skywire-bin: no upstream release binary published for ${stdenvNoCC.hostPlatform.system}"
else

stdenvNoCC.mkDerivation {
  pname = "skywire-bin";
  inherit version;

  src = fetchurl {
    url = "https://github.com/skycoin/skywire/releases/download/v${version}/skywire-v${version}-${archSuffix}.tar.gz";
    inherit hash;
  };

  # Tarball is a single `skywire` binary at the top level — no
  # subdir, so unpack stripping zero leading path components.
  sourceRoot = ".";

  dontConfigure = true;
  dontBuild = true;

  # Static-musl binary — patchelf has nothing to patch, and trying
  # would fail. Skip stripping too in case Skycoin shipped any
  # symbol info we'd want for debugging.
  dontPatchELF = true;
  dontStrip = true;

  installPhase = ''
    runHook preInstall

    install -Dm755 skywire $out/bin/skywire

    cat > $out/bin/skywire-cli <<EOF
    #!${runtimeShell}
    exec $out/bin/skywire cli "\$@"
    EOF
    cat > $out/bin/skywire-visor <<EOF
    #!${runtimeShell}
    exec $out/bin/skywire visor "\$@"
    EOF
    chmod +x $out/bin/skywire-cli $out/bin/skywire-visor

    install -d $out/share/skywire/apps
    ${lib.concatMapStringsSep "\n" (app: ''
      cat > $out/share/skywire/apps/${app} <<EOF
      #!${runtimeShell}
      exec $out/bin/skywire app ${app} "\$@"
      EOF
      chmod +x $out/share/skywire/apps/${app}
    '') apps}

    runHook postInstall
  '';

  meta = with lib; {
    description = "Skywire — prebuilt static binary from upstream release tarballs";
    homepage = "https://github.com/skycoin/skywire";
    license = licenses.unfree;
    mainProgram = "skywire";
    platforms = [
      "x86_64-linux"
      "aarch64-linux"
      "i686-linux"
      "armv7l-linux"
      "armv6l-linux"
      "riscv64-linux"
    ];
    sourceProvenance = with sourceTypes; [ binaryNativeCode ];
  };
}

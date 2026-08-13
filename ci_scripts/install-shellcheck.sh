#!/usr/bin/env bash
#
# Put a shellcheck where `make lint-shell` will find it: ./shellcheck if this
# can fetch the release, otherwise whatever the machine already has.
#
# Both halves are deliberate. The download is preferred because it is the same
# version everywhere. The fallback exists because github.com/releases is not a
# dependency a lint job can rely on from CI — it answered 503, then died
# mid-transfer, on consecutive runs — and a linter that cannot be downloaded is
# not a reason to fail a build when the runner image already ships one.
#
# What is NOT acceptable is the third behaviour, which is what this file used to
# do: print curl's errors, exit 0 anyway, and leave lint-shell to quietly pick
# up a different binary. That turned an outage into a lint error against source
# annotated for another version, and cost a red build to read. Hence `set -e`,
# and hence the fallback announcing itself.
#
# (Mind the wrapping here: a comment line beginning with the linter's own name
# is read as a directive, not as prose, and fails the file it explains.)

set -euo pipefail

osname="$(uname -s | tr '[:upper:]' '[:lower:]')"
osarch="$(uname -m)"

# The release assets do not spell the architectures the way uname does, and an
# unbuildable name is indistinguishable from an outage at the other end: an
# Apple Silicon Mac asks for `darwin.arm64`, which has never existed, and gets
# back whatever the network says about a URL with no file behind it. Every
# asset for ARM is published as `aarch64`.
case "$osarch" in
    arm64) osarch="aarch64" ;;
    amd64) osarch="x86_64" ;;
esac

url="https://github.com/koalaman/shellcheck/releases/download/stable/shellcheck-stable.${osname}.${osarch}.tar.xz"

trap 'rm -rf ./scheck ./shellcheck-stable.tar.xz' EXIT

mkdir -p ./scheck

# --fail so an HTML error page is not mistaken for a tarball, and --retry
# because a release download dying mid-transfer often fixes itself.
# --fail so an HTML error page is not mistaken for a tarball, and --retry
# because a release download dying mid-transfer often fixes itself.
#
# Everything AFTER curl can still fail without curl saying so, which is the
# case this used to get wrong: a transfer that dies after the last retry
# leaves a truncated file that tar unpacks partially, and the path inside the
# archive is an assumption of its own. Both land here with curl having exited
# 0, so the download branch has to verify it actually produced a shellcheck
# rather than assume it. It did not, and the failure was `mv: cannot stat
# ./scheck/shellcheck-stable/shellcheck` — after which the fallback below,
# written precisely for a bad release download, was never reached.
if curl --fail --location --retry 3 --retry-delay 2 --retry-all-errors \
    -o shellcheck-stable.tar.xz "$url" \
    && tar -xf shellcheck-stable.tar.xz -C ./scheck \
    && [ -x ./scheck/shellcheck-stable/shellcheck ]
then
    mv ./scheck/shellcheck-stable/shellcheck ./shellcheck
    echo "installed ./shellcheck from $url"
    ./shellcheck --version
elif command -v shellcheck >/dev/null 2>&1; then
    # No ./shellcheck is written, which is exactly what lint-shell's
    # `command -v ./shellcheck || command -v shellcheck` falls back on.
    echo "WARNING: could not install shellcheck from $url"
    echo "WARNING: falling back to the shellcheck already on PATH:"
    shellcheck --version
    echo "WARNING: its findings may differ from the pinned release — see the"
    echo "WARNING: SC2317/SC2329 note in ci_scripts/mux-route-probe.sh."
else
    echo "FATAL: could not download $url, and no shellcheck is on PATH." >&2
    echo "FATAL: install one (apt-get install shellcheck, brew install" >&2
    echo "FATAL: shellcheck, pip install shellcheck-py) and try again." >&2
    exit 1
fi

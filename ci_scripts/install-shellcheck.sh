#!/usr/bin/env bash
#
# Fetch the shellcheck binary that `make lint-shell` runs.
#
# `set -e` and the cleanup trap are the point of this file, not decoration.
# Without them a failed download was a SUCCESSFUL step: curl gave up, tar and
# mv printed errors, and the final `rm -rf` returned 0 on the way out. Nothing
# was installed and nothing said so, so lint-shell fell through to whichever
# binary the runner image happens to ship — an older version, printing
# different codes, against source annotated for ours. That is a lint failure
# with a network outage's name filed off, and it cost a red build to read.
#
# (Mind the wrapping here: a comment line beginning with the linter's own name
# is read as a directive, not as prose, and fails the file it explains.)

set -euo pipefail

osname="$(uname -s | tr '[:upper:]' '[:lower:]')"
osarch="$(uname -m)"
url="https://github.com/koalaman/shellcheck/releases/download/stable/shellcheck-stable.${osname}.${osarch}.tar.xz"

trap 'rm -rf ./scheck ./shellcheck-stable.tar.xz' EXIT

mkdir -p ./scheck

# --fail so an HTML error page is not mistaken for a tarball, and --retry
# because a release download dying mid-flight is the one failure here that is
# not anybody's bug and fixes itself on the second try.
curl --fail --location --retry 3 --retry-delay 2 --retry-all-errors \
    -o shellcheck-stable.tar.xz "$url"

tar -xf shellcheck-stable.tar.xz -C ./scheck

mv ./scheck/shellcheck-stable/shellcheck ./shellcheck
./shellcheck --version

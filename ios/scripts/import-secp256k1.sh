#!/usr/bin/env bash
# bitcoin-core/secp256k1, the wallet's one third-party crypto library
# (playbook §6 D-2), vendored verbatim into
# ios/Packages/WalletCore/Sources/CSecp256k1.
#
# What is copied is exactly what the library build compiles: the three files
# upstream's src/CMakeLists.txt builds (secp256k1.c and the two precomputed
# tables) and every file they #include under the defines WalletCore's
# Package.swift sets, as the compiler itself reports it (clang -MM), plus the
# licence (COPYING). Nothing is edited. The closure is the 64-bit one (the
# compiler picks field_5x52 / scalar_4x64 / native int128 on every iOS and
# Mac target); a 32-bit target would fail to compile loudly, not silently.
#
# The tag is checked against the pinned commit before anything is copied, so
# a tag moved upstream stops the import instead of changing the bytes.
#
# Usage:
#   ios/scripts/import-secp256k1.sh            re-import the pinned tag
#   ios/scripts/import-secp256k1.sh --verify   prove the vendored copy is the
#                                              tag's bytes (exit 1 otherwise)
#
# Needs: git, Xcode's clang (xcrun), network to github.com.
# To move to a new release: change TAG and COMMIT, run without --verify,
# build and run WalletCore's tests, review the diff.
set -euo pipefail

TAG=v0.8.0
# The commit the (annotated) tag points at: `git ls-remote <repo> 'refs/tags/v0.8.0^{}'`.
COMMIT=6e2c8bc4ecdc6e71dbe7a368f360d8d453ce435d
# Keep in step with the CSecp256k1 target's cSettings in Package.swift: they
# decide which files the closure holds.
DEFINES=(
  -DENABLE_MODULE_RECOVERY=1
  -DECMULT_WINDOW_SIZE=15
  -DCOMB_BLOCKS=43
  -DCOMB_TEETH=6
  -DSECP256K1_NO_API_VISIBILITY_ATTRIBUTES
)
UNITS=(src/secp256k1.c src/precomputed_ecmult.c src/precomputed_ecmult_gen.c)

ROOT=$(cd "$(dirname "$0")/../.." && pwd)
DEST=$ROOT/ios/Packages/WalletCore/Sources/CSecp256k1

mode=import
case "${1:-}" in
  "") ;;
  --verify) mode=verify ;;
  *) echo "usage: $0 [--verify]" >&2; exit 2 ;;
esac

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

# Git warns that an annotated tag "is not a commit" on a shallow clone of it;
# its output is shown only when the clone fails.
if ! git -c advice.detachedHead=false clone --quiet --depth 1 --branch "$TAG" \
  https://github.com/bitcoin-core/secp256k1 "$tmp/up" 2>"$tmp/clone.log"; then
  cat "$tmp/clone.log" >&2
  exit 1
fi
head=$(git -C "$tmp/up" rev-parse HEAD)
if [ "$head" != "$COMMIT" ]; then
  echo "bitcoin-core/secp256k1 $TAG is $head upstream, pinned $COMMIT: stopping" >&2
  exit 1
fi

# The closure, as repository-relative paths: clang -MM lists the unit and its
# headers, some as src/modules/recovery/../../../include/x.h, which the sed
# loop folds; SDK headers are absolute and dropped.
(cd "$tmp/up" && for unit in "${UNITS[@]}"; do xcrun clang "${DEFINES[@]}" -MM "$unit"; done) |
  tr ' \\' '\n\n' | grep -v ':$' | grep -v '^$' | grep -v '^/' |
  sed -e ':a' -e 's#[^/.][^/]*/\.\./##' -e 'ta' | sort -u >"$tmp/closure"

mkdir -p "$tmp/stage"
while read -r path; do
  mkdir -p "$tmp/stage/$(dirname "$path")"
  cp "$tmp/up/$path" "$tmp/stage/$path"
done <"$tmp/closure"
cp "$tmp/up/COPYING" "$tmp/stage/COPYING"
count=$(($(wc -l <"$tmp/closure") + 1))

if [ "$mode" = verify ]; then
  # UPSTREAM.md is ours (where the copy came from); everything else must match.
  if diff -r -x UPSTREAM.md "$tmp/stage" "$DEST"; then
    echo "CSecp256k1: $count files identical to bitcoin-core/secp256k1 $TAG ($COMMIT)"
  else
    echo "CSecp256k1 differs from bitcoin-core/secp256k1 $TAG ($COMMIT)" >&2
    exit 1
  fi
  exit 0
fi

mkdir -p "$DEST"
rm -rf "$DEST/include" "$DEST/src" "$DEST/COPYING"
cp -R "$tmp/stage/." "$DEST/"
echo "CSecp256k1: imported $count files from bitcoin-core/secp256k1 $TAG ($COMMIT)"

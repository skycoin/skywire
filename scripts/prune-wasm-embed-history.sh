#!/usr/bin/env bash
#
# prune-wasm-embed-history.sh — drop OLD versions of the embedded wasm-visor blob
# from git history, keeping only the current (HEAD) one.
#
# The embedded wasm-visor (pkg/wasmhv/wasmbin/wasm-visor.wasm.gz, ~8MB) is
# committed and updated occasionally via `make embed-wasm-visor`. Each update
# leaves the prior ~8MB version in history forever. This reclaims that by
# stripping the file from ALL history, then re-committing the current version
# fresh — so history holds it exactly once.
#
# ⚠️  THIS REWRITES HISTORY. Read before running:
#   - All commit hashes after the first change to the blob change → you must
#     FORCE-PUSH, and every clone/fork must re-clone or `git reset --hard`.
#     Coordinate with anyone working on the repo. Never run on a shared branch
#     without agreement.
#   - The Go module proxy + sumdb retain already-PUBLISHED versions immutably:
#     `go install ...@<old-tag>` still fetches the blob that tag shipped. This
#     shrinks the git repo and fresh clones, NOT what the proxy serves for past
#     releases.
#   - Requires `git filter-repo` (https://github.com/newren/git-filter-repo).
#
# Usage (from a CLEAN, up-to-date checkout you can afford to rewrite):
#   scripts/prune-wasm-embed-history.sh
#   # review, then: git push --force-with-lease origin <branch>
#
set -euo pipefail

BLOB="pkg/wasmhv/wasmbin/wasm-visor.wasm.gz"

if ! command -v git-filter-repo >/dev/null 2>&1; then
  echo "error: git-filter-repo not found. Install it: https://github.com/newren/git-filter-repo" >&2
  exit 1
fi

if [ -n "$(git status --porcelain)" ]; then
  echo "error: working tree not clean. Commit or stash first." >&2
  exit 1
fi

if [ ! -f "$BLOB" ]; then
  echo "error: $BLOB not present at HEAD — run from repo root on a branch that has it." >&2
  exit 1
fi

echo "This will REWRITE HISTORY to keep only the current $BLOB."
echo "You will then need to force-push and everyone must re-clone."
read -r -p "Type 'rewrite' to proceed: " ans
[ "$ans" = "rewrite" ] || { echo "aborted."; exit 1; }

# 1) Stash the current blob outside the tree.
tmp="$(mktemp)"
cp "$BLOB" "$tmp"

# 2) Remove the blob from ALL history (every version, including HEAD).
git filter-repo --path "$BLOB" --invert-paths --force

# 3) Re-add the current blob as a single fresh commit.
mkdir -p "$(dirname "$BLOB")"
cp "$tmp" "$BLOB"
rm -f "$tmp"
git add "$BLOB"
git commit -m "chore(wasm-visor): re-add embedded blob after history prune"

echo
echo "Done. History now contains $BLOB exactly once."
echo "Next: review, then 'git push --force-with-lease'. Everyone else must re-clone."

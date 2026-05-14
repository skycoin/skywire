#!/usr/bin/env bash
# scripts/docs-prepare.sh — stage external doc trees under docs/ so
# MkDocs (which requires all sources under a single docs_dir) can
# render them as part of the unified site.
#
# Each tree is gitignored under its destination — the canonical
# location remains where it was. Idempotent; safe to re-run before
# every `mkdocs serve` / `mkdocs build`.
#
# Used by:
#   - .github/workflows/docs.yml (CI)
#   - `make docs-serve` (local preview)
#
# v1 scope: specs + rewards. Per-app (cmd/apps/), per-DMSG-binary
# (cmd/dmsg/), and per-service (cmd/svc/) READMEs are deferred —
# their relative-path links assume the cmd/ location and would all
# need rewriting (skip for now; revisit as a follow-up PR).

set -euo pipefail

cd "$(dirname "$0")/.."

# --- Specs ------------------------------------------------------------
# skywire-specs/ has its own subdir layout (specifications/, transports/,
# VPN/, plus README.md and Drafts.md at the root). We mirror it as-is
# under docs/specs/ so internal cross-links resolve.
mkdir -p docs/specs
rsync -a --delete --exclude '.git' skywire-specs/ docs/specs/
# Synthesize an index page if the spec root doesn't already have a
# README.md that fits the index slot.
if [ -f docs/specs/README.md ] && [ ! -f docs/specs/index.md ]; then
  mv docs/specs/README.md docs/specs/index.md
fi

# --- Rewards ----------------------------------------------------------
# Only the markdown lives in rewards/ — the rest of that directory is
# accounting scripts. Copy the .md files individually.
mkdir -p docs/rewards
shopt -s nullglob
for f in rewards/*.md; do
  cp "$f" "docs/rewards/$(basename "$f")"
done
cat > docs/rewards/index.md <<'EOF'
# Skywire Rewards

Eligibility rules and operational details for the Skywire reward
distribution. See [mainnet_rules](mainnet_rules.md) for the
authoritative ruleset.
EOF

echo "docs-prepare: staged specs and rewards under docs/"

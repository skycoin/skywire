#!/usr/bin/env bash
# check-descriptors.sh — every non-test Go file carries a well-formed
# c<layer>-<domain>-<system> descriptor, and one package agrees on one value.
#
# See docs/code-descriptors.md. The descriptor is a TAXONOMY LABEL, not a
# rename: it is read and grepped, never part of an identifier.
#
# This checks only well-formedness and per-package consistency. It deliberately
# does NOT decide which domain a package belongs to — domain and system are a
# documentation grouping with no Go analogue (an `app` package importing `net`
# is normal), and the source standard is explicit that they must not become a
# lint rule. The one part that IS enforced, the layer, is enforced by the Go
# compiler already: import cycles do not build.
#
# Generated files (the Go "Code generated ... DO NOT EDIT." header) and
# testdata are skipped: a tag there would be lost on the next generate.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

RE='c[0-4]-(com|net|app|vis)-[a-z0-9]+'
GEN='^// Code generated .* DO NOT EDIT\.$'
ROOTS=(pkg cmd internal)

for r in "${ROOTS[@]}"; do
	[ -d "$r" ] || { echo "check-descriptors: $r not found — run from the repo" >&2; exit 2; }
done

fail=0

while IFS= read -r f; do
	[ -n "$f" ] || continue
	case "$f" in *_test.go) continue ;; esac
	grep -qE "$RE" "$f" 2>/dev/null && continue
	grep -qE "$GEN" "$f" 2>/dev/null && continue
	echo "NO DESCRIPTOR: $f — add a c<layer>-<domain>-<system> tag (docs/code-descriptors.md)"
	fail=1
done < <(find "${ROOTS[@]}" -name '*.go' -not -path '*/testdata/*' 2>/dev/null | sort)

while IFS= read -r d; do
	[ -n "$d" ] || continue
	tags=$(grep -hoE "$RE" "$d"/*.go 2>/dev/null | sort -u)
	[ "$(printf '%s\n' "$tags" | grep -c .)" -gt 1 ] || continue
	echo "MIXED DESCRIPTORS: $d carries $(printf %s "$tags" | tr "\n" " ") — one package, one descriptor"
	fail=1
done < <(find "${ROOTS[@]}" -name '*.go' -not -name '*_test.go' -not -path '*/testdata/*' 2>/dev/null | sed 's|/[^/]*$||' | sort -u)

[ "$fail" = 0 ] && echo "descriptors: every file tagged, one descriptor per package"
exit "$fail"

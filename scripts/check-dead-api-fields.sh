#!/usr/bin/env bash
# check-dead-api-fields.sh — find json-tagged struct fields that nothing assigns.
#
# The failure mode this catches is silent. A field declared on an output struct
# is READ by encoding/json and so looks used, but if no code ever assigns it the
# API reports a zero value forever, and callers trust it:
#
#   #4849  Overview.Hypervisors / ConnectedHypervisor — always null on every
#          visor, including one with three hypervisors connected.
#   #4850  EmbeddedProxyInfo.WebAddr — two CLI commands printed "-" in a column
#          that could never hold a value.
#   #4851  Summary.SkybianBuildVersion — the method and the UI binding both
#          existed; only the assignment between them was missing.
#
# Heuristic on purpose: it reports candidates for a human to judge. A field
# assigned only by reflection, or from a module this does not scan, is a false
# positive — record it in ALLOW with the reason.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

# Files whose json-tagged fields are OUTPUT (the visor produces them). Input
# structs are legitimately assigned only by the json decoder.
FILES=(pkg/visor/api.go pkg/visor/api_state.go)

# Fields assigned somewhere the scan cannot see. "<field> # reason"
ALLOW=()

for f in "${FILES[@]}"; do
	if [ ! -f "$f" ]; then
		echo "check-dead-api-fields: $f not found (run from the repo, or update FILES)" >&2
		exit 2
	fi
done

fields=$(grep -hoE '^[[:space:]]+[A-Z][A-Za-z0-9_]*[[:space:]]+[^`]*`json:"[a-z_]+' "${FILES[@]}" \
	| awk '{print $1}' | sort -u)

if [ -z "$fields" ]; then
	echo "check-dead-api-fields: matched no json-tagged fields — the scan is broken, not the code" >&2
	exit 2
fi

fail=0
count=0
while IFS= read -r f; do
	[ -n "$f" ] || continue
	count=$((count + 1))
	skip=0
	for a in ${ALLOW[@]+"${ALLOW[@]}"}; do
		[ "$a" = "$f" ] && skip=1
	done
	[ "$skip" -eq 1 ] && continue
	# `Name:` in a composite literal, or `.Name =` / `.Name +=` anywhere in
	# non-test Go outside vendor. One hit is enough to call it assigned.
	if ! grep -rqE "(^|[^A-Za-z0-9_])${f}:[^=]|\.${f}[[:space:]]*(\+)?=" \
		--include='*.go' pkg cmd internal 2>/dev/null; then
		echo "DEAD: ${f} carries a json tag but nothing assigns it — it will always serialize as its zero value"
		fail=1
	fi
done <<< "$fields"

echo "checked $count json-tagged API fields"
exit "$fail"

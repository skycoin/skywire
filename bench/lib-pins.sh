#!/bin/sh
# lib-pins.sh — the one check every pin consumer runs before it trusts a pin.
#
#   PINS_HERE=$here; . "$here/lib-pins.sh"
#
# A LIBRARY: it defines one function and does no top-level work, so sourcing it
# can never disturb a measurement, and sourcing it twice is harmless.
#
# A pin file is what `skywire cli route calc … --json` writes and what
# `proxy start --route` / `proxy mux set --legs` read:
#
#   [{"forward":[{"From":"<pk>","To":"<pk>","TpID":"<uuid>"}, …], …}]
#
# The TpID is a real transport UUID. On 2026-09-18 three pins in the campaign's
# pins dir had been overwritten with a 59-byte placeholder,
# [{"forward":[{"From":"a","To":"b","TpID":"tp-0371ab4b"}]}], by a harness
# outside this repo. Nothing rejected them: paired_start started the reference,
# saw no leg matching "tp-0371ab4b", and returned 1 — so every set of that chain
# ran unpaired for a reason no log line named. A pin whose first hop is not a
# UUID is a broken FILE, not a route that failed to come up, and it is worth
# exactly one loud line saying so.
#
# pin_ok <file>: 0 when the file's first forward hop carries a transport UUID.
# Otherwise prints one line beginning "INVALID pin" naming the file, and fails.
# Cheap enough (one jq) to put in front of every consumer.

pin_ok() {
	_pnf=$1
	if [ ! -f "$_pnf" ]; then
		echo "INVALID pin $_pnf: no such file"
		return 1
	fi
	_pnid=$(jq -r '.[0].forward[0].TpID // empty' "$_pnf" 2>/dev/null)
	if [ -z "$_pnid" ]; then
		echo "INVALID pin $_pnf: no .[0].forward[0].TpID ($(wc -c < "$_pnf" | tr -d ' ') bytes)"
		return 1
	fi
	if ! printf '%s' "$_pnid" | grep -Eq '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'; then
		echo "INVALID pin $_pnf: first hop TpID '$_pnid' is not a transport UUID — the file is a stub, not a route"
		return 1
	fi
	return 0
}

# pins_ok <dir>: every via-*.json of a pins dir, checked in one pass. Echoes the
# number that failed; prints one "INVALID pin" line each. A dir with no pins is
# reported as such and counts as one failure, since a runner given it will not
# pin anything.
pins_ok() {
	_pnd=$1; _pnbad=0; _pnn=0
	for _pnp in "$_pnd"/via-*.json; do
		[ -f "$_pnp" ] || continue
		_pnn=$((_pnn + 1))
		pin_ok "$_pnp" || _pnbad=$((_pnbad + 1))
	done
	if [ "$_pnn" -eq 0 ]; then
		echo "INVALID pin dir $_pnd: no via-*.json in it"
		_pnbad=1
	fi
	echo "$_pnbad"
}

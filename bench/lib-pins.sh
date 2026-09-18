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

# --- the order pins are handed out in -----------------------------------------
# A runner given no explicit order hands its pins out in `ls` order, which is
# alphabetical by public key and says nothing about the route. On 2026-09-18
# that put via-0255117b — a route whose own reference in the SAME campaign
# measured 0.05 MB/s and failed all three 50 MB downloads — into the
# composition cell's second tunnel, so the cell measured that route rather than
# composition.
#
# pin_order <out dir> <pins dir>: the default order, best first, on STDOUT (so
# it drops straight into `order=${6:-$(pin_order "$out" "$pins")}`), with the
# reasoning on STDERR, one line per route.
#
# Rank sources, in order of authority:
#   1. <out>/paired-ref.tsv — pick-ref.sh's contemporaneous 50 MB download probe
#      of the suite's shortlist: `candidate median_MBps trials_ok
#      source_rank_MBps`. Best median first; this is the freshest measurement of
#      the campaign's own routes there is.
#   2. <out>/ref-via-<tok>.tsv — the route's own reference set, for the pins
#      pick-ref.sh never shortlisted. Those keep ls order among themselves and
#      follow the ranked ones.
# A route is DROPPED, not demoted, when either source shows a FAILED 50 MB
# download cell or a 50 MB download median below PIN_MIN_MBPS (1 MB/s): a route
# that cannot carry 50 MB is not a slower leg, it is a broken one, and a set
# that pins it measures the breakage.
# With no paired-ref.tsv there is nothing to rank by and the order is ls order,
# exactly as before.
PIN_MIN_MBPS=${PIN_MIN_MBPS:-1}

# pin_ref_stats <out dir> <tok>: "<median MBps> <failed cells> <cells>" from the
# route's own reference set. Fails when the out dir holds no such set.
pin_ref_stats() {
	_prf=$1/ref-via-$2.tsv
	[ -f "$_prf" ] || return 1
	_prrows=$(grep -v '^#' "$_prf" | awk -F'\t' '$2 == "down" && $3 + 0 == 50000000')
	[ -n "$_prrows" ] || return 1
	_prmed=$(printf '%s\n' "$_prrows" | awk -F'\t' '{printf "%.4f\n", $4 / 1e6}' | sort -n |
		awk '{a[NR] = $1} END {if (!NR) {print "-"; exit} printf "%.2f\n", (NR % 2) ? a[(NR + 1) / 2] : (a[NR / 2] + a[NR / 2 + 1]) / 2}')
	_prbad=$(printf '%s\n' "$_prrows" | awk -F'\t' '$8 + 0 != 1 || $5 !~ /^2/ {n++} END {print n + 0}')
	_prn=$(printf '%s\n' "$_prrows" | wc -l | tr -d ' ')
	echo "$_prmed $_prbad $_prn"
}

# pin_check <out dir> <tok> <probe median|-> <probe ok> <probe trials>: 0 to keep
# the route. The reason, kept or dropped, lands in $pin_why.
pin_check() {
	_pcout=$1; _pctok=$2; _pcmed=$3; _pcnum=$4; _pcden=$5
	pin_why=""; _pcbad=0
	case $_pcmed in
	- | '') ;;
	*)
		pin_why="probe ${_pcmed} MB/s ${_pcnum}/${_pcden}"
		awk -v m="$_pcmed" -v x="$PIN_MIN_MBPS" 'BEGIN{exit !(m + 0 < x + 0)}' &&
			{ pin_why="$pin_why — below $PIN_MIN_MBPS MB/s"; _pcbad=1; }
		;;
	esac
	case $_pcden in
	'' | 0 | *[!0-9]*) ;;
	*) [ "${_pcnum:-0}" -lt "$_pcden" ] && { pin_why="${pin_why:-probe} — ${_pcnum}/${_pcden} cells verified"; _pcbad=1; } ;;
	esac
	if _pcs=$(pin_ref_stats "$_pcout" "$_pctok"); then
		_pcrmed=${_pcs%% *}; _pcrest=${_pcs#* }; _pcrbad=${_pcrest%% *}; _pcrn=${_pcrest##* }
		pin_why="${pin_why:+$pin_why, }reference ${_pcrmed} MB/s over $_pcrn 50 MB down cell(s)"
		[ "$_pcrbad" -gt 0 ] && { pin_why="$pin_why, $_pcrbad failed"; _pcbad=1; }
		case $_pcrmed in
		- | '') ;;
		*) awk -v m="$_pcrmed" -v x="$PIN_MIN_MBPS" 'BEGIN{exit !(m + 0 < x + 0)}' &&
			{ pin_why="$pin_why, below $PIN_MIN_MBPS MB/s"; _pcbad=1; } ;;
		esac
	fi
	[ -n "$pin_why" ] || pin_why="no reference in this run"
	[ "$_pcbad" -eq 0 ]
}

pin_order() {
	_pood=$1; _poop=$2
	_pooall=$(ls "$_poop"/via-*.json 2>/dev/null | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')
	_poref=$_pood/paired-ref.tsv
	if [ ! -f "$_poref" ]; then
		echo "pin order: no $_poref — ls order: ${_pooall% }" >&2
		printf '%s\n' "${_pooall% }"
		return 0
	fi
	# median_MBps is column 2, trials_ok ("2/2") column 3; "-" sorts last.
	_poranked=$(grep -v '^#' "$_poref" |
		awk -F'\t' 'NF >= 3 && $1 != "" {split($3, a, "/"); printf "%.4f %s %s %s %s\n", ($2 == "-" ? 0 : $2), $1, $2, a[1] + 0, a[2] + 0}' |
		sort -k1,1gr)
	_pokeep=""; _podrop=""; _porank=0
	for _pot in $(printf '%s\n' "$_poranked" | awk '{print $2}'); do
		case " $_pooall " in *" $_pot "*) ;; *) continue ;; esac
		_porank=$((_porank + 1))
		_pom=$(printf '%s\n' "$_poranked" | awk -v t="$_pot" '$2 == t {print $3; exit}')
		_pon=$(printf '%s\n' "$_poranked" | awk -v t="$_pot" '$2 == t {print $4; exit}')
		_pod=$(printf '%s\n' "$_poranked" | awk -v t="$_pot" '$2 == t {print $5; exit}')
		if pin_check "$_pood" "$_pot" "$_pom" "$_pon" "$_pod"; then
			_pokeep="$_pokeep$_pot "
			echo "pin order: $_pot kept at rank $_porank — $pin_why" >&2
		else
			_podrop="$_podrop$_pot "
			echo "pin order: $_pot DROPPED — $pin_why" >&2
		fi
	done
	# the pins pick-ref.sh never shortlisted, in ls order, behind the ranked ones
	for _pot in $_pooall; do
		case " $_pokeep$_podrop " in *" $_pot "*) continue ;; esac
		if pin_check "$_pood" "$_pot" - 0 0; then
			_pokeep="$_pokeep$_pot "
			echo "pin order: $_pot kept (unranked, ls order) — $pin_why" >&2
		else
			_podrop="$_podrop$_pot "
			echo "pin order: $_pot DROPPED — $pin_why" >&2
		fi
	done
	[ -z "$_podrop" ] || echo "pin order: dropped ${_podrop% }" >&2
	echo "pin order: ${_pokeep% }" >&2
	printf '%s\n' "${_pokeep% }"
}

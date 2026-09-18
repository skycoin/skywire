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
# reasoning on STDERR, one line per pin — used, dropped or left out.
#
# A pin is RANKED when this out dir holds a number for it, in this order:
#   1. <out>/paired-ref.tsv, pick-ref.sh's contemporaneous 50 MB download probe
#      (`candidate median_MBps trials_ok source_rank_MBps`) — its probe median,
#      or the suite rank it carries when the candidate was recorded but not
#      re-probed;
#   2. <out>/ref-via-<tok>.tsv, the route's own reference set, median of its
#      50 MB download cells.
# It is DROPPED when a number exists and says the route is broken: a probe that
# was run and did not verify every trial, a failed 50 MB reference cell, or a
# median below PIN_MIN_MBPS (1 MB/s). A route that cannot carry 50 MB is not a
# slower leg.
# It is UNRANKED when NOTHING in this out dir measured it. An unranked pin is
# LEFT OUT unless PIN_ALLOW_UNRANKED=1: chain AL ran the smoke sets in a dir
# holding only a copied paired-ref.tsv and no ref-via-*.tsv, so via-0255117b —
# the route the ranking exists to keep out — came back in as "unranked, ls
# order" and was pinned into the composition cell again. A runner that is then
# short of pins must say so, not quietly take a stray one.
# With no ranking material at all in the dir the order is plain ls order,
# exactly as before pin_order existed.
PIN_MIN_MBPS=${PIN_MIN_MBPS:-1}
PIN_ALLOW_UNRANKED=${PIN_ALLOW_UNRANKED:-0}

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

# pin_eval <out dir> <tok>: judge one pin. Sets pin_key (the number it is
# ordered by), pin_src (what measured it) and pin_why (the line printed for it).
# Returns 0 to use it, 1 to drop it as broken, 2 when nothing ranked it.
pin_eval() {
	_ped=$1; _pet=$2
	pin_key=""; pin_src=""; pin_why=""; _pebad=0
	_perow=$(grep -v '^#' "$_ped/paired-ref.tsv" 2>/dev/null | awk -F'\t' -v t="$_pet" '$1 == t {print; exit}')
	if [ -n "$_perow" ]; then
		_pem=$(printf '%s\n' "$_perow" | cut -f2)
		_peok=$(printf '%s\n' "$_perow" | cut -f3)
		_pesr=$(printf '%s\n' "$_perow" | cut -f4)
		case ${_pem:--} in
		-) ;;
		*) pin_key=$_pem; pin_src="probe ${_pem} MB/s ${_peok}" ;;
		esac
		# a probe that RAN and did not verify every trial is a bad route, not an
		# unmeasured one — that is the whole point of recording failed probes
		_penum=${_peok%%/*}; _peden=${_peok##*/}
		case ${_peden:-0} in
		0 | *[!0-9]*) ;;
		*) case ${_penum:-x} in
			*[!0-9]*) ;;
			*) [ "$_penum" -lt "$_peden" ] && { pin_why="probe verified only ${_peok}"; _pebad=1; } ;;
			esac ;;
		esac
		if [ -z "$pin_key" ] && [ "$_pebad" -eq 0 ]; then
			case ${_pesr:--} in
			-) ;;
			*) pin_key=$_pesr; pin_src="suite rank ${_pesr} MB/s (recorded, not re-probed)" ;;
			esac
		fi
	fi
	if _pes=$(pin_ref_stats "$_ped" "$_pet"); then
		_perm=${_pes%% *}; _perest=${_pes#* }; _perbad=${_perest%% *}; _pern=${_perest##* }
		pin_src="${pin_src:+$pin_src, }reference ${_perm} MB/s over $_pern 50 MB down cell(s)"
		[ "$_perbad" -gt 0 ] && { pin_why="${pin_why:+$pin_why, }$_perbad of $_pern reference cell(s) failed"; _pebad=1; }
		case ${_perm:--} in - | '') ;; *) [ -n "$pin_key" ] || pin_key=$_perm ;; esac
	fi
	case ${pin_key:--} in
	-) ;;
	*) awk -v m="$pin_key" -v x="$PIN_MIN_MBPS" 'BEGIN{exit !(m + 0 < x + 0)}' &&
		{ pin_why="${pin_why:+$pin_why, }${pin_key} MB/s is below $PIN_MIN_MBPS MB/s"; _pebad=1; } ;;
	esac
	if [ -z "$pin_key" ] && [ "$_pebad" -eq 0 ]; then
		pin_why="no row in paired-ref.tsv and no ref-via-$_pet.tsv in this run"
		return 2
	fi
	[ -n "$pin_why" ] || pin_why=$pin_src
	[ "$_pebad" -eq 0 ] || return 1
	return 0
}

pin_order() {
	_pood=$1; _poop=$2
	_pooall=$(ls "$_poop"/via-*.json 2>/dev/null | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')
	_pooall=${_pooall% }
	if [ ! -f "$_pood/paired-ref.tsv" ] && ! ls "$_pood"/ref-via-*.tsv >/dev/null 2>&1; then
		echo "pin order: nothing in $_pood ranks these pins (no paired-ref.tsv, no ref-via-*.tsv) — ls order: $_pooall" >&2
		printf '%s\n' "$_pooall"
		return 0
	fi
	_poranked=""; _podrop=""; _pounr=""
	for _pot in $_pooall; do
		pin_eval "$_pood" "$_pot" && _pers=0 || _pers=$?
		case $_pers in
		0)
			_poranked="$_poranked$pin_key $_pot
"
			echo "pin order: $_pot ranked $pin_key — $pin_why" >&2
			;;
		1)
			_podrop="$_podrop$_pot "
			echo "pin order: $_pot LEFT OUT (broken route) — $pin_why" >&2
			;;
		*)
			_pounr="$_pounr$_pot "
			if [ "$PIN_ALLOW_UNRANKED" = 1 ]; then
				echo "pin order: $_pot used unranked, last, because PIN_ALLOW_UNRANKED=1 — $pin_why" >&2
			else
				echo "pin order: $_pot LEFT OUT (unranked) — $pin_why; set PIN_ALLOW_UNRANKED=1 to use it anyway" >&2
			fi
			;;
		esac
	done
	# stable sort: equal keys keep ls order
	_pokeep=$(printf '%s' "$_poranked" | sort -k1,1gr -s | awk '{print $2}' | tr '\n' ' ')
	_pokeep=${_pokeep% }
	if [ "$PIN_ALLOW_UNRANKED" = 1 ] && [ -n "$_pounr" ]; then
		_pokeep="${_pokeep:+$_pokeep }${_pounr% }"
	fi
	echo "pin order: $(printf '%s' "$_pokeep" | wc -w | tr -d ' ') pin(s) usable: ${_pokeep:-none}${_podrop:+; dropped as broken: ${_podrop% }}${_pounr:+; unranked: ${_pounr% }}" >&2
	printf '%s\n' "$_pokeep"
}

#!/bin/sh
# shellcheck disable=SC2154,SC2034 # a library: $out, $set_name, $sink, $pins, $name, $exit_pk and the
# cut_*/ct_*/paired_* results are the CALLER's variables, set and read across the source boundary.
# lib-paired.sh — contemporaneous paired reference rows for a mux set.
#
#   PAIRED_HERE=$here; . "$here/lib-paired.sh"
#
# A LIBRARY: it defines functions and does no top-level work beyond defaulting
# its own variables, so sourcing it can never disturb a measurement.
#
# The rule it implements, from the goal text (2026-09-17):
#
#   "references measured contemporaneously … reference rows are interleaved
#    with the mux rows of the same cell so both see the same network, and every
#    verdict is the paired ratio mux/best-reference"
#
# So for every row of a mux set, ONE reference row of the SAME cell runs on ONE
# single route immediately BEFORE the mux row. The reference runs in a SECOND
# proxy instance (skysocks-client-ref on 127.0.0.1:1081, started the way
# run-capcheck.sh starts its instance B) — never through :1080, which stays the
# default instance under test so status.skysocks keeps showing the session the
# campaign is measuring.
#
# The two instances NEVER transfer at the same time: paired_row returns before
# the mux row starts. One reference row per mux row, so a 20-row set is 40
# transfers and nothing else changes about the set's wall time.
#
# Which route is the reference (PAIRED_REF):
#   auto     (default) <outdir>/paired-ref.txt when bench/pick-ref.sh has
#            written one — the route that won a 2-trial probe of the last full
#            suite's top three, taken just before the campaign. Failing that,
#            the best route by the most recent reference medians in the out dir
#            — <outdir>/ref-*.tsv first, then <outdir>/drift.tsv
#            (drift-probe.sh's two probes) — falling back to `direct`. The cell
#            that ranks them is the PAIRED_RANK_BYTES (50 MB) download.
#   direct   a --direct proxy: the AppDirect shortcut, one hop to the exit and
#            no route group at all, so the mux engine's global width cannot
#            grow a second leg under it.
#   <short>  a pin short, e.g. 0371ab4b: `--route <pins>/via-<short>.json`.
#   <path>   a pin file directly.
# ref-direct-squicr is never a candidate: it is the same direct path on another
# carrier, not an independent reference route.
#
# `proxy mux width` is a GLOBAL engine setting, not a per-app one, so a pinned
# reference instance can be grown to the set's width by the adaptive engine.
# That is recorded rather than fought: every paired row carries the reference's
# leg count, and a reference holding more than one leg is called out — it is no
# longer a single-route reference and the ratio has to be read knowing that.
#
# Artefacts, per set:
#   <set>.paired.tsv       row, cell, ref MB/s, mux MB/s, ratio, ref_ok,
#                          mux_ok, ref_legs
#   <set>.paired-rows.tsv  every reference transfer as a plain bench.sh row,
#                          so a reference row can be re-read like any other
PAIRED=${PAIRED:-1}                 # 0 restores exactly the un-paired behaviour
PAIRED_REF=${PAIRED_REF:-auto}
PAIRED_REF2=${PAIRED_REF2:-auto}    # the second reference, only for the two-upload sum
PAIRED_RANK_BYTES=${PAIRED_RANK_BYTES:-50000000}
PAIRED_SLOT1_NAME=${PAIRED_SLOT1_NAME:-skysocks-client-ref}
PAIRED_SLOT1_ADDR=${PAIRED_SLOT1_ADDR:-127.0.0.1:1081}
PAIRED_SLOT2_NAME=${PAIRED_SLOT2_NAME:-skysocks-client-ref2}
PAIRED_SLOT2_ADDR=${PAIRED_SLOT2_ADDR:-127.0.0.1:1084}
PAIRED_RG_WAIT=${PAIRED_RG_WAIT:-30}
PAIRED_HERE=${PAIRED_HERE:-$(dirname "$0")}
CLI=${CLI:-/home/d0mo/go/bin/skywire}

# pin_ok: the shared stub-pin check, used before any --route pin is trusted.
# shellcheck source=bench/lib-pins.sh
. "$PAIRED_HERE/lib-pins.sh"

_paired_name() { case $1 in 2) echo "$PAIRED_SLOT2_NAME" ;; *) echo "$PAIRED_SLOT1_NAME" ;; esac; }
_paired_addr() { case $1 in 2) echo "$PAIRED_SLOT2_ADDR" ;; *) echo "$PAIRED_SLOT1_ADDR" ;; esac; }

# _paired_median <file> <label prefix> <dir> <bytes> -> median MB/s or "-"
# summarize.sh's formula, hash-verified rows only.
_paired_median() {
	grep -v '^#' "$1" 2>/dev/null |
		awk -F'\t' -v p="$2" -v d="$3" -v n="$4" '$1 ~ "^" p && $2==d && $3==n && $8==1 {print $4/1e6}' |
		sort -n |
		awk '{a[NR]=$1} END{if (!NR) {print "-"; exit} printf "%.2f\n", (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2}'
}

# paired_rank <outdir>: "<median MB/s> <token> <source>" per candidate route,
# best first. PAIRED_REF_DIR overrides where the medians are read from (the
# refs dir of the day, when the mux sets run into a dir of their own).
#
# A route measured by BOTH sources keeps its better reading: this is a
# SHORTLIST, not a bar — bench/pick-ref.sh re-probes the top of it live, and
# the bar itself is the paired row next to each mux row. A route that looked
# good in either source has earned a probe.
paired_rank() {
	_prd=${PAIRED_REF_DIR:-$1}
	{
		for _pf in "$_prd"/ref-*.tsv; do
			[ -f "$_pf" ] || continue
			_pb=$(basename "$_pf" .tsv)
			case $_pb in
			*.carrier | *.paired | *.paired-rows | *.cut | *.up2) continue ;;
			ref-direct-stcpr) _ptok=direct ;;
			ref-via-*) _ptok=${_pb#ref-via-} ;;
			*) continue ;;
			esac
			_pm=$(_paired_median "$_pf" "$_pb" down "$PAIRED_RANK_BYTES")
			[ "$_pm" = - ] || echo "$_pm $_ptok ref"
		done
		if [ -f "$_prd/drift.tsv" ]; then
			_pm=$(_paired_median "$_prd/drift.tsv" drift-direct-stcpr down "$PAIRED_RANK_BYTES")
			[ "$_pm" = - ] || echo "$_pm direct drift"
			for _ps in $(grep -v '^#' "$_prd/drift.tsv" 2>/dev/null | awk -F'\t' '$1 ~ /^drift-via-/ {sub(/^drift-via-/, "", $1); sub(/-t[0-9]+$/, "", $1); print $1}' | sort -u); do
				_pm=$(_paired_median "$_prd/drift.tsv" "drift-via-$_ps" down "$PAIRED_RANK_BYTES")
				[ "$_pm" = - ] || echo "$_pm $_ps drift"
			done
		fi
	} | sort -k1,1 -rn | awk '!seen[$2]++'
}

# paired_resolve <outdir> [rank] -> the reference token for slot 1 (rank 1) or
# slot 2 (rank 2). Precedence: an explicit PAIRED_REF / PAIRED_REF2, then
# <outdir>/paired-ref.txt (bench/pick-ref.sh's contemporaneous probe of the
# suite's top three), then the suite's own ranking, then `direct`.
paired_resolve() {
	_pr=${2:-1}
	case $_pr in
	2) [ "$PAIRED_REF2" = auto ] || { echo "$PAIRED_REF2"; return 0; } ;;
	*)
		[ "$PAIRED_REF" = auto ] || { echo "$PAIRED_REF"; return 0; }
		if [ -s "$1/paired-ref.txt" ]; then
			_pt=$(head -1 "$1/paired-ref.txt" | tr -d ' \t')
			[ -n "$_pt" ] && { echo "$_pt"; return 0; }
		fi
		;;
	esac
	_pt=$(paired_rank "$1" | awk -v r="$_pr" 'NR==r {print $2}')
	[ -n "$_pt" ] || { [ "$_pr" = 1 ] && _pt=direct; }
	echo "$_pt"
}

# _paired_stop_clean <app>: stop the instance and insist its route groups are
# gone, retrying the stop once (run-mux.sh's rule, self-contained here so the
# library does not depend on the caller's helpers).
_paired_stop_clean() {
	$CLI cli proxy stop -n "$1" >/dev/null 2>&1
	_pw=0
	while :; do
		_pleft=$($CLI cli proxy mux info -n "$1" --json 2>/dev/null | jq -r '.[]?.desc.dst_port' 2>/dev/null | tr '\n' ' ' | sed 's/ *$//')
		[ -z "$_pleft" ] && return 0
		if [ "$_pw" -ge "$PAIRED_RG_WAIT" ]; then
			echo "paired: $1 still holds route group(s) $_pleft after ${PAIRED_RG_WAIT}s — stopping again"
			$CLI cli proxy stop -n "$1" >/dev/null 2>&1
			sleep 3
			return 1
		fi
		sleep 2; _pw=$((_pw + 2))
	done
}

# paired_start <slot> <token> <exit pk> <pins> <sink>: bring the reference
# instance up on that one route and prove it with four 100 KB probes. Returns 1
# when the reference cannot be measured — the caller then runs the set unpaired
# rather than recording ratios against a route that is not there.
paired_start() {
	_pslot=$1; _ptok=$2; _pxpk=$3; _ppins=$4; _psink=$5
	_pn=$(_paired_name "$_pslot"); _pa=$(_paired_addr "$_pslot")
	_paired_stop_clean "$_pn" >/dev/null 2>&1
	case $_ptok in
	"" ) echo "paired: no reference route resolved for slot $_pslot"; return 1 ;;
	direct)
		$CLI cli route settings --prefer default >/dev/null 2>&1
		timeout 240 $CLI cli proxy start -k "$_pxpk" -n "$_pn" -a "$_pa" --direct >/dev/null 2>&1
		paired_route="direct stcpr to the exit (AppDirect shortcut, no route group)"
		;;
	*)
		_ppin=$_ptok
		[ -f "$_ppin" ] || _ppin="$_ppins/via-$_ptok.json"
		[ -f "$_ppin" ] || { echo "paired: no pin file for reference '$_ptok'"; return 1; }
		# a stub pin cannot fail the leg check informatively — it fails it every
		# time, for a reason no line names. Say so before starting anything.
		pin_ok "$_ppin" || { echo "paired: reference slot $_pslot has no usable pin — unpaired"; return 1; }
		timeout 240 $CLI cli proxy start -k "$_pxpk" -n "$_pn" -a "$_pa" --route "$_ppin" 2>&1 |
			grep -iv debug | grep -i "pinned\|running\|error\|fatal" | head -3
		_pwant=$(jq -r '.[0].forward[0].TpID' "$_ppin" 2>/dev/null)
		_phops=$(jq -r '.[0].forward | map(.From + ">" + .To + "@" + .TpID) | join(",")' "$_ppin" 2>/dev/null)
		_phave=$($CLI cli proxy mux info -n "$_pn" --json 2>/dev/null | jq -r '.[0].legs[]?.transport_id')
		echo "$_phave" | grep -q "^$_pwant" || {
			echo "paired: $_pn pinned leg $_pwant NOT present (legs: $(echo "$_phave" | tr '\n' ' '))"
			return 1
		}
		paired_route="$_phops"
		;;
	esac
	_pbad=0
	for _ in 1 2 3 4; do
		_pc=$(curl -s -m 30 --socks5-hostname "$_pa" -o /dev/null -w '%{http_code}' "$_psink/?bytes=100000")
		[ "$_pc" = 200 ] || _pbad=$((_pbad + 1))
	done
	echo "paired: reference slot $_pslot = $_ptok on $_pn@$_pa, $((4 - _pbad))/4 probes ok"
	[ "$_pbad" -eq 0 ] || { echo "paired: reference slot $_pslot failed its probes — not measuring against it"; return 1; }
	return 0
}

paired_stop() { _paired_stop_clean "$(_paired_name "${1:-1}")" >/dev/null 2>&1 || true; }

# paired_legs <slot>: how many legs the reference instance holds right now. A
# --direct reference has no route group, which reads as 0.
paired_legs() {
	$CLI cli proxy mux info -n "$(_paired_name "${1:-1}")" --json 2>/dev/null |
		jq -r '[.[].legs[]?] | length' 2>/dev/null || echo "-"
}

# paired_row <slot> <row> <bytes> <dir> -> "<MB/s> <hash_ok> <ref legs>"
# One reference transfer of the same cell, immediately before the mux row.
# Uses the caller's $out, $set_name and $sink, as every run_set helper does.
paired_row() {
	_prow=$2
	"$PAIRED_HERE/bench.sh" "$(_paired_addr "$1")" "$sink" "$3" "$4" "$set_name-ref-r$_prow" >> "$out/$set_name.paired-rows.tsv"
	_pl=$(tail -1 "$out/$set_name.paired-rows.tsv")
	_plegs=$(paired_legs "$1")
	[ "${_plegs:-0}" -le 1 ] 2>/dev/null || echo "$set_name row $_prow: the reference instance holds $_plegs legs — it is no longer a single-route reference"
	printf '%s %s %s\n' \
		"$(echo "$_pl" | awk -F'\t' '{printf "%.2f", $4/1e6}')" \
		"$(echo "$_pl" | cut -f8)" \
		"${_plegs:--}"
}

# paired_emit <row> <cell> <ref MB/s> <ref ok> <ref legs> <mux MB/s> <mux ok>
# The ratio is blank ("-") unless BOTH sides hash-verified: an unverified
# transfer has no goodput worth dividing.
paired_emit() {
	_pratio=-
	if [ "$4" = 1 ] && [ "$7" = 1 ]; then
		_pratio=$(awk -v m="$6" -v r="$3" 'BEGIN{if (r+0 > 0) printf "%.3f", m/r; else printf "-"}')
	fi
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$6" "$_pratio" "$4" "$7" "$5" >> "$out/$set_name.paired.tsv"
}

# paired_header <ref token> <ref route>: start the set's paired files.
paired_header() {
	printf '# paired reference: %s (%s) via %s@%s — one reference row of the same cell immediately before every mux row\n' \
		"$1" "$2" "$(_paired_name 1)" "$(_paired_addr 1)" > "$out/$set_name.paired.tsv"
	printf '# row\tcell\tref_MBps\tmux_MBps\tratio\tref_ok\tmux_ok\tref_legs\n' >> "$out/$set_name.paired.tsv"
	printf '# reference transfers of %s, bench.sh rows: %s (%s)\n' "$set_name" "$1" "$2" > "$out/$set_name.paired-rows.tsv"
}

# paired_cell <bytes> <dir> -> the cell name used in <set>.paired.tsv ("50down")
paired_cell() { echo "$(($1 / 1000000))$2"; }

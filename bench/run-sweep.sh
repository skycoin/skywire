#!/bin/sh
# run-sweep.sh — drive ONE live knob across a list of values and table the result.
#
#   bench/run-sweep.sh <exit pk> <out dir> <pins dir> <runner> <key> <v1,v2,...> [runner args...]
#
# e.g.
#   TUNNELS=2 LEGS="" bench/run-sweep.sh <exit> bench/2026-09-18/<commit> $S/pins \
#       run-mux.sh upload.chunk_bytes 1MiB,2MiB,4MiB,8MiB 3 http://127.0.0.1:18080 "<order>"
#
# For each value it runs <runner> (run-mux.sh, run-compose.sh, run-standby.sh or
# run-degrade.sh — anything that sources bench/lib-settings.sh) with
#
#   SETTINGS="<whatever SETTINGS already held> <key>=<value>"
#
# into <out dir>/sweep/<key>=<value>/, so each value is a COMPLETE run of that
# runner with its own sets, its own paired references and its own artefacts; the
# knob is the only thing that moves. [runner args...] are passed through after
# the pins directory (trials, sink, pin order), and every other environment knob
# the runners read — TUNNELS, LEGS, PAIRED, SIZES, CUT_ROW, EXIT_RES ... — is
# inherited from this script's own environment, so a sweep is shaped exactly the
# way the campaign it belongs to is shaped.
#
# ROUTE_SETTINGS is inherited too, and a sweep OVER a router flag is spelled by
# setting ROUTE_SETTINGS per value instead — this script sweeps the app knobs,
# which are the ones that can be changed without disturbing the visor.
#
# The table, <out dir>/sweep/<key>.tsv, is one row per value and one column per
# (set, size, direction) cell, each holding
#
#   <median MB/s>;<paired ratio>;<hashes ok/n>;<wire/goodput>
#
# — the median from the cell's rows, the ratio from <set>.paired.tsv when the
# runner produced one (that is the verdict the goal text asks for: a value is
# better only if its PAIRED ratio is better, since the bar swings 2x inside an
# hour), and the last two as bench/summarize.sh computes them. A cell a value
# never produced (an INVALID set, a shape that would not come up) is "-", which
# is the honest answer and not a zero.
#
# Nothing here decides anything: it puts the numbers side by side. The verdict
# is bench/verdict.sh on each value's directory.
set -u
exit_pk=$1; out=$2; pins=$3; runner=$4; key=$5; values=$6
shift 6
here=$(dirname "$0")
[ -x "$here/$runner" ] || { echo "run-sweep: no runner $here/$runner"; exit 1; }
sweep="$out/sweep"
mkdir -p "$sweep"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM
base_settings=${SETTINGS:-}
vlist=$(echo "$values" | tr ',' ' ')
echo "run-sweep: $runner over $key = $(echo "$vlist" | tr ' ' ',')${base_settings:+ (on top of SETTINGS=\"$base_settings\")} -> $sweep"

# --- the runs -----------------------------------------------------------------
for v in $vlist; do
	[ -n "$v" ] || continue
	vout="$sweep/$key=$v"; mkdir -p "$vout"
	for _pr in paired-ref.txt paired-ref.tsv; do [ -f "$out/$_pr" ] && cp "$out/$_pr" "$vout/"; done
	echo "=== $key=$v -> $vout"
	SETTINGS="${base_settings:+$base_settings }$key=$v"
	export SETTINGS
	"$here/$runner" "$exit_pk" "$vout" "$pins" "$@" 2>&1 | sed "s|^|[$key=$v] |"
	echo "=== $key=$v done"
done

# --- the table ----------------------------------------------------------------
# median/hashes/wire come from summarize.sh, which already reads the cells out of
# the rows themselves; the ratio comes from the set's paired file.
# paired_ratio <dir> <set> <MB><dir> -> the MEDIAN ratio of that cell, or "-"
paired_ratio() {
	_p="$1/$2.paired.tsv"
	[ -f "$_p" ] || { echo -; return 0; }
	grep -v '^#' "$_p" | awk -F'\t' -v c="$3" '$2 == c && $5 ~ /^[0-9.]+$/ {print $5}' |
		sort -n | awk '{a[NR] = $1} END {if (NR) printf "%.2f", (NR % 2) ? a[(NR + 1) / 2] : (a[NR / 2] + a[NR / 2 + 1]) / 2; else printf "-"}'
}
cols="$tmp/cols"; : > "$cols"
for v in $vlist; do
	[ -n "$v" ] || continue
	vout="$sweep/$key=$v"; mkdir -p "$vout"
	for _pr in paired-ref.txt paired-ref.tsv; do [ -f "$out/$_pr" ] && cp "$out/$_pr" "$vout/"; done
	[ -d "$vout" ] || continue
	"$here/summarize.sh" "$vout" 2>/dev/null | awk 'NR > 1 && $1 !~ /^INVALID/ && $3 ~ /^[0-9]+$/' > "$tmp/sum.$v"
	# set/<MB><dir>, in first-seen order, unioned across every value
	awk '{print $1 "/" $3 $2}' "$tmp/sum.$v" >> "$cols"
done
# stable union: first occurrence wins, later duplicates dropped
awk '!seen[$0]++' "$cols" > "$tmp/cols.u"
t="$sweep/$key.tsv"
{
	printf '# sweep of %s over %s with %s, one complete run per value under %s/<value>/\n' \
		"$key" "$(echo "$vlist" | tr ' ' ',')" "$runner" "$sweep"
	printf '# each cell: median_MBps;paired_ratio;hashes_ok/n;wire_goodput ("-" = the value never produced that cell)\n'
	printf 'value'
	while read -r c; do printf '\t%s' "$c"; done < "$tmp/cols.u"
	printf '\n'
	for v in $vlist; do
		[ -n "$v" ] || continue
		printf '%s' "$v"
		while read -r c; do
			_set=${c%%/*}; _cell=${c#*/}
			_row=$(awk -v s="$_set" -v c="$_cell" '$1 == s && ($3 $2) == c {print; exit}' "$tmp/sum.$v" 2>/dev/null)
			if [ -z "$_row" ]; then
				printf '\t-'
			else
				# summarize.sh columns: set dir MB ok/n median min max carrier wire/good
				printf '\t%s;%s;%s;%s' \
					"$(echo "$_row" | awk '{print $5}')" \
					"$(paired_ratio "$sweep/$key=$v" "$_set" "$_cell")" \
					"$(echo "$_row" | awk '{print $4}')" \
					"$(echo "$_row" | awk '{print $9}')"
			fi
		done < "$tmp/cols.u"
		printf '\n'
	done
} > "$t"
echo "--- $t"
cat "$t"

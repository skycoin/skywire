#!/bin/sh
# drift-probe.sh — has the rig itself moved since the reference bar was measured?
#
#   bench/drift-probe.sh <exit pk> <results dir> <refs dir> <sink url> [pins dir]
#
# Re-measures the TWO bar routes only — direct stcpr, and the pinned two-hop
# route via 0371ab4bcff7b121f4b91f6856d6740c6f9dc1fe716977850aeb5d84378b300a13
# (US-Atlanta) — with 2 trials of 50 MB down and 50 MB up each, and compares the
# medians against the same cells in <refs dir>/ref-direct-stcpr.tsv and
# <refs dir>/ref-via-0371ab4b.tsv. 8 transfers, ~4 minutes: cheap enough to run
# between campaign units, which is the point — a mux set that misses the bar
# because the PATH got slower is not a mux regression.
#
# Rows land in <results dir>/drift.tsv in bench.sh's shape, so summarize.sh
# reads them unchanged. Per cell one line:
#   drift <route> <dir> <MB> now=<median MB/s> ref=<median MB/s> ratio=<now/ref>
# then `DRIFT OK` or `DRIFT >25% on N cells` (DRIFT_TOL overrides the 25%).
# The exit code is 0 either way: this script reports, the orchestrator decides.
# Both medians count hash-verified rows ONLY, on both sides of the ratio: with
# two trials a single failed transfer's goodput would otherwise read as drift.
#
# The helpers below are the minimum copy of run-refs.sh's (same proxy start,
# same warm, same median) rather than a source of it: run-refs.sh is a script
# with top-level work, not a library.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; refs=$3; sink=$4
pins=${5:-${PINS:-$(dirname "$out")/pins}}
here=$(dirname "$0")
mkdir -p "$out"
via_short=${VIA_SHORT:-0371ab4b}
trials=${DRIFT_TRIALS:-2}
size=${DRIFT_SIZE:-50000000}
tol=${DRIFT_TOL:-0.25}
f="$out/drift.tsv"

# APP=skysocks-client drives the probe through the DEFAULT proxy instance on
# :1080 (APP_PORT overrides), exactly as run-refs.sh does, so the probe measures
# the session the campaign measures.
app_name() { echo "${APP:-$1}"; }
app_addr() { if [ -n "${APP:-}" ]; then echo "127.0.0.1:${APP_PORT:-1080}"; else echo "127.0.0.1:$1"; fi; }
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
tp_of() { # <remote pk> <type> -> tp id
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg pk "$1" --arg t "$2" '.transports[] | select(.remote_pk==$pk and .type==$t) | .id' | head -1
}
# warm <socks> <name> <start args...>: four 100 KB probes must all pass; a proxy
# that comes up in a bad state is restarted, up to three times (run-refs.sh).
warm() {
	socks=$1; name=$2; shift 2
	try=1
	while [ $try -le 3 ]; do
		bad=0
		for _ in 1 2 3 4; do
			code=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
			[ "$code" = 200 ] || bad=$((bad + 1))
		done
		[ $bad -eq 0 ] && { echo "$name warm: 4/4 probes ok (try $try)"; return 0; }
		echo "$name warm: $bad/4 probes failed (try $try) — restarting the proxy"
		stop_app "$name"
		timeout 240 $CLI cli proxy start -n "$name" -a "$socks" "$@" >/dev/null 2>&1
		try=$((try + 1))
	done
	return 1
}
# median <file> <label prefix> <dir> <bytes> -> median goodput in MB/s, or "-"
# (summarize.sh's formula: column 4 is B/s, hash-failed rows are kept out).
median() {
	grep -v '^#' "$1" 2>/dev/null |
		awk -F'\t' -v p="$2" -v d="$3" -v n="$4" '$1 ~ "^" p && $2==d && $3==n && $8==1 {print $4/1e6}' |
		sort -n |
		awk '{a[NR]=$1} END{if (!NR) {print "-"; exit} printf "%.2f\n", (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2}'
}
# rows <label> <socks>: 2 trials of 50 MB down then 2 of 50 MB up, appended to drift.tsv
rows() {
	for dir in down up; do
		t=1
		while [ $t -le "$trials" ]; do
			"$here/bench.sh" "$2" "$sink" "$size" "$dir" "$1-t$t" >> "$f"
			t=$((t + 1))
		done
	done
}

echo "# exit=$exit_pk refs=$refs sink=$sink trials=$trials size=$size" > "$f"

# --- bar 1: direct stcpr -----------------------------------------------------
$CLI cli route settings --prefer default >/dev/null 2>&1
name=$(app_name refstcpr); socks=$(app_addr 1082)
stop_app "$name"
timeout 180 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --direct >/dev/null 2>&1
warm "$socks" "$name" -k "$exit_pk" --direct || echo "$name: still failing probes after 3 restarts — measuring anyway"
echo "drift: direct stcpr tp=$(tp_of "$exit_pk" stcpr)"
rows drift-direct-stcpr "$socks"
stop_app "$name"

# --- bar 2: the pinned two-hop route ----------------------------------------
pin="$pins/via-$via_short.json"
if [ -f "$pin" ]; then
	name=$(app_name "via$via_short"); socks=$(app_addr 1091)
	stop_app "$name"
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --route "$pin" 2>&1 | grep -iv debug | grep -i "pinned\|running\|error\|fatal" | head -3
	want=$(jq -r '.[0].forward[0].TpID' "$pin")
	legs=$($CLI cli proxy mux info -n "$name" --json 2>/dev/null | jq -r '.[0].legs[]?.transport_id')
	echo "$legs" | grep -q "^$want" || echo "$name: pinned leg $want NOT present (legs: $(echo "$legs" | tr '\n' ' ')) — measuring the shape it has"
	warm "$socks" "$name" -k "$exit_pk" --route "$pin" || echo "$name: still failing probes after 3 restarts — measuring anyway"
	rows "drift-via-$via_short" "$socks"
	stop_app "$name"
else
	echo "drift: $pin not found — the via-$via_short bar is not measured (pass the pins dir as arg 5 or set PINS)"
fi

# --- compare against the reference bar ---------------------------------------
mb=$((size / 1000000))
bad=0
for route in "direct-stcpr:ref-direct-stcpr" "via-$via_short:ref-via-$via_short"; do
	short=${route%%:*}; base=${route#*:}; ref="$refs/$base.tsv"
	for dir in down up; do
		now=$(median "$f" "drift-$short" "$dir" "$size")
		r=$(median "$ref" "$base" "$dir" "$size")
		[ -f "$ref" ] || r="-"
		ratio=-
		case "$now$r" in
		*-*) ;;
		*) ratio=$(awk -v a="$now" -v b="$r" 'BEGIN{if (b+0 > 0) printf "%.2f", a/b; else printf "-"}') ;;
		esac
		echo "drift $short $dir $mb now=$now ref=$r ratio=$ratio"
		case $ratio in
		-) ;;
		*) awk -v x="$ratio" -v t="$tol" 'BEGIN{d=x-1; if (d<0) d=-d; exit !(d > t+0)}' && bad=$((bad + 1)) ;;
		esac
	done
done
if [ "$bad" -eq 0 ]; then
	echo "DRIFT OK"
else
	echo "DRIFT >$(awk -v t="$tol" 'BEGIN{printf "%d", t*100}')% on $bad cells"
fi
exit 0

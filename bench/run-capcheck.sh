#!/bin/sh
# run-capcheck.sh — is the ~8 MB/s plateau the path's cap, or the sender's?
#
#   bench/run-capcheck.sh <exit pk> <results dir> <pins dir> <url>
#
# Every mux variant of the campaign lands on the same ~8 MB/s download ceiling.
# Two readings fit that: (a) the host/exit path really is capped there, so no
# scheduler can do better, or (b) one sender simply never asks for more than
# ~8 MB/s and the path has headroom. This script separates them WITHOUT any mux:
# it runs TWO independent single-route proxy clients at once and asks whether
# their goodputs SUM.
#
#   instance A = the default `skysocks-client` on :1080, --direct  (stcpr
#                straight to the exit, tp b414796d — one hop, no route group)
#   instance B = a second named app `skysocks-client-b` on :1081, --route
#                <pins>/via-0371ab4b.json (two hops via US-Atlanta, tp 95839ad0)
#
# `proxy start -n <name>` registers a SECOND app instance of the same
# skysocks-client binary (cmd/skywire-cli/commands/proxy/proxy.go:391 AddApp →
# pkg/visor/visorconfig/v1_runtime.go:272 AddAppConfig, which hands the new app
# its own random routing port 10-99), and -a gives it its own SOCKS5 listener.
# So A and B share nothing but the host NIC and the exit.
#
#   sum(A,B) ~= 2 x best_alone   -> the sender leaves capacity unused (b)
#   sum(A,B) ~=     best_alone   -> the path/exit is genuinely capped (a)
#
# Rows go to <results dir>/capcheck.tsv; both instances are stopped at the end
# and the default instance is LEFT STOPPED.
#
# Run this under the campaign rig lock ($S/rig.lock) — the script does not take
# the lock itself, because the orchestrator's chain already holds it while this
# runs and a self-lock would deadlock against it.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=${1:-}; out=${2:-}; pins=${3:-}; sink=${4:-http://127.0.0.1:18080}
[ -n "$exit_pk" ] && [ -n "$out" ] && [ -n "$pins" ] || {
	echo "usage: bench/run-capcheck.sh <exit pk> <results dir> <pins dir> <url>" >&2
	exit 2
}
here=$(dirname "$0")
mkdir -p "$out"
f="$out/capcheck.tsv"

SIZE=${SIZE:-50000000}       # one 50 MB transfer per row, the campaign's big size
TRIALS=${TRIALS:-3}          # concurrent trials per direction
RG_WAIT=${RG_WAIT:-30}       # seconds to wait for an app's route groups to go away
NAME_A=${NAME_A:-skysocks-client}
NAME_B=${NAME_B:-skysocks-client-b}
SOCKS_A=${SOCKS_A:-127.0.0.1:1080}
SOCKS_B=${SOCKS_B:-127.0.0.1:1081}
PIN_B=${PIN_B:-via-0371ab4b.json}

tmp=$(mktemp -d)
cleaned=0
stop_all() {
	[ "$cleaned" = 1 ] && return 0
	cleaned=1
	stop_app_clean "$NAME_B" || echo "$NAME_B: dst_ports $rg_left left behind"
	stop_app_clean "$NAME_A" || echo "$NAME_A: dst_ports $rg_left left behind"
}
trap 'stop_all; rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM

# --- rig helpers (same shapes as run-refs.sh / run-mux.sh) -------------------
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
rg_ports() { $CLI cli proxy mux info -n "$1" --json 2>/dev/null | jq -r '.[]?.desc.dst_port' 2>/dev/null | tr '\n' ' ' | sed 's/ *$//'; }
wait_no_rg() {
	_w=0
	while :; do
		rg_left=$(rg_ports "$1")
		[ -z "$rg_left" ] && return 0
		[ "$_w" -ge "$RG_WAIT" ] && return 1
		sleep 2; _w=$((_w + 2))
	done
}
# stop_app_clean <app>: stop, then insist on zero route groups, retrying once.
stop_app_clean() {
	stop_app "$1"
	wait_no_rg "$1" && return 0
	echo "$1: route group(s) still registered ${RG_WAIT}s after proxy stop: dst_ports $rg_left — stopping again"
	stop_app "$1"
	wait_no_rg "$1" && return 0
	echo "$1: route group(s) STILL registered after a second proxy stop: dst_ports $rg_left"
	return 1
}
tp_counters() { # <tp id> -> "sent recv"
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id==$id) | "\(.log.sent) \(.log.recv)"'
}
tp_of() { # <remote pk> <type> -> tp id
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg pk "$1" --arg t "$2" '.transports[] | select(.remote_pk==$pk and .type==$t) | .id' | head -1
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
# warm <socks> <name> <start args...>: four 100 KB probes must all pass.
warm() {
	socks=$1; name=$2; shift 2
	try=1
	while [ $try -le 3 ]; do
		bad=0
		for i in 1 2 3 4; do
			code=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
			[ "$code" = 200 ] || bad=$((bad + 1))
		done
		[ $bad -eq 0 ] && { echo "$name warm: 4/4 probes ok (try $try)"; return 0; }
		echo "$name warm: $bad/4 probes failed (try $try) — restarting the proxy"
		stop_app_clean "$name" >/dev/null 2>&1
		timeout 240 $CLI cli proxy start -n "$name" -a "$socks" "$@" >/dev/null 2>&1
		try=$((try + 1))
	done
	return 1
}

# --- one transfer, bench.sh's flags exactly, but launchable in background ----
# The payload of an upload is generated BEFORE the timed section so the two
# concurrent POSTs start at the same instant instead of being skewed by 50 MB
# of /dev/urandom.
curl_down() { # <socks> <prefix>
	curl -s --socks5-hostname "$1" -m 600 -D "$2.h" -o "$2.b" \
		-w '%{http_code} %{size_download} %{time_total} %{speed_download}' \
		"$sink/?bytes=$SIZE" > "$2.w" 2>/dev/null
}
curl_up() { # <socks> <prefix>
	curl -s --socks5-hostname "$1" -m 600 -o "$2.r" \
		-w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' \
		-X POST --data-binary "@$2.payload" "$sink/upload" > "$2.w" 2>/dev/null
}
emit() { # <trial> <mode> <instance> <dir> <prefix>
	_tr=$1; _mode=$2; _inst=$3; _dir=$4; _p=$5
	set -- $(cat "$_p.w" 2>/dev/null)
	_http=${1:-000}; _got=${2:-0}; _secs=${3:-0}; _bps=${4:-0}
	if [ "$_dir" = down ]; then
		_want=$(tr -d '\r' < "$_p.h" 2>/dev/null | awk 'tolower($1)=="x-sha256:"{print $2}')
		_have=$(sha256sum "$_p.b" 2>/dev/null | cut -d' ' -f1)
	else
		_want=$(jq -r .sha256 "$_p.r" 2>/dev/null)
		_have=$(cat "$_p.sha" 2>/dev/null)
	fi
	_ok=0
	[ -n "$_want" ] && [ "$_want" = "$_have" ] && _ok=1
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$_tr" "$_mode" "$_inst" "$_dir" "$SIZE" "$_secs" "$_bps" "$_http" "$_got" "$_ok" >> "$f"
	[ "$_ok" = 1 ] || echo "WARNING trial=$_tr $_mode $_inst $_dir: hash NOT verified (http=$_http got=$_got)"
	echo "  trial=$_tr $_mode $_inst $_dir ${_bps} B/s in ${_secs}s http=$_http hash_ok=$_ok"
}
# carrier <trial> <mode> before|after: the byte counters of the two transports
# the run is supposed to ride, so "two independent paths" is checked, not assumed.
carrier_snap() { # -> "<tpA sent,recv> <tpB sent,recv>"
	echo "$(tp_counters "$tpA" | tr ' ' ','):$(tp_counters "$tpB" | tr ' ' ',')"
}
carrier_delta() { # <trial> <mode> <dir> <before>
	_b=$1; shift
	_a=$(carrier_snap)
	_b1=${_b%%:*}; _b2=${_b##*:}; _a1=${_a%%:*}; _a2=${_a##*:}
	[ -n "$_b1" ] && [ -n "$_a1" ] &&
		echo "# carrier	$1	$2	$3	A=$tpA	sent_delta=$(( ${_a1%,*} - ${_b1%,*} ))	recv_delta=$(( ${_a1#*,} - ${_b1#*,} ))" >> "$f"
	[ -n "$_b2" ] && [ -n "$_a2" ] &&
		echo "# carrier	$1	$2	$3	B=$tpB	sent_delta=$(( ${_a2%,*} - ${_b2%,*} ))	recv_delta=$(( ${_a2#*,} - ${_b2#*,} ))" >> "$f"
	return 0
}

# --- bring both instances up -------------------------------------------------
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD 2>/dev/null)
ec=$(exit_commit)
echo "local=$local_commit exit=$ec sink=$sink size=$SIZE trials=$TRIALS"

pin="$pins/$PIN_B"
[ -f "$pin" ] || { echo "capcheck: pin file $pin not found" >&2; exit 2; }

$CLI cli route settings --prefer default >/dev/null 2>&1

# A: the default instance, one direct stcpr hop to the exit.
stop_app_clean "$NAME_A" >/dev/null 2>&1
timeout 180 $CLI cli proxy start -k "$exit_pk" -n "$NAME_A" -a "$SOCKS_A" --direct >/dev/null 2>&1
warm "$SOCKS_A" "$NAME_A" -k "$exit_pk" --direct || echo "$NAME_A: still failing probes after 3 restarts"
tpA=$(tp_of "$exit_pk" stcpr)
[ -n "$tpA" ] || { echo "capcheck: no stcpr transport to the exit after $NAME_A started" >&2; exit 1; }

# B: a second named instance on its own port, pinned to the two-hop route.
stop_app_clean "$NAME_B" >/dev/null 2>&1
timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$NAME_B" -a "$SOCKS_B" --route "$pin" 2>&1 |
	grep -v DEBUG | grep -i "route pinned\|leg\|FATAL" | head -3
tpB=$(jq -r '.[0].forward[0].TpID' "$pin")
legs=$($CLI cli proxy mux info -n "$NAME_B" --json 2>/dev/null | jq -r '.[0].legs[]? | .transport_id')
echo "$legs" | grep -q "^$tpB" || {
	echo "capcheck: $NAME_B pinned leg $tpB NOT present (legs: $(echo "$legs" | tr '\n' ' '))" >&2
	exit 1
}
[ "$(echo "$legs" | wc -l)" -eq 1 ] || echo "$NAME_B: WARNING $(echo "$legs" | wc -l) legs, expected exactly the pinned one"
warm "$SOCKS_B" "$NAME_B" -k "$exit_pk" --route "$pin" || echo "$NAME_B: still failing probes after 3 restarts"
[ "$tpA" = "$tpB" ] && echo "capcheck: WARNING A and B ride the SAME transport $tpA — the sum test is meaningless"

echo "A=$NAME_A@$SOCKS_A tp=$tpA (direct stcpr)   B=$NAME_B@$SOCKS_B tp=$tpB (pinned $PIN_B)"

# the upload payloads, generated once, outside every timed section
head -c "$SIZE" /dev/urandom > "$tmp/A.payload"; sha256sum "$tmp/A.payload" | cut -d' ' -f1 > "$tmp/A.sha"
head -c "$SIZE" /dev/urandom > "$tmp/B.payload"; sha256sum "$tmp/B.payload" | cut -d' ' -f1 > "$tmp/B.sha"

printf '# capcheck exit=%s local=%s exit_commit=%s A=%s@%s/tp=%s B=%s@%s/tp=%s pin=%s sink=%s\n' \
	"$exit_pk" "$local_commit" "$ec" "$NAME_A" "$SOCKS_A" "$tpA" "$NAME_B" "$SOCKS_B" "$tpB" "$PIN_B" "$sink" > "$f"
echo "# trial	mode	instance	dir	bytes	seconds	goodput_bps	http	got	hash_ok" >> "$f"

# --- the measurements --------------------------------------------------------
# alone <dir>: one instance at a time, one trial each — the contemporary control.
run_alone() {
	_dir=$1
	for _i in A B; do
		case $_i in
		A) _socks=$SOCKS_A ;;
		B) _socks=$SOCKS_B ;;
		esac
		_cb=$(carrier_snap)
		"curl_$_dir" "$_socks" "$tmp/$_i"
		carrier_delta "$_cb" 0 alone "$_dir"
		emit 0 alone "$_i" "$_dir" "$tmp/$_i"
	done
}
# concurrent <dir>: both instances transfer at the same instant, TRIALS times.
run_concurrent() {
	_dir=$1
	_t=1
	while [ "$_t" -le "$TRIALS" ]; do
		_cb=$(carrier_snap)
		"curl_$_dir" "$SOCKS_A" "$tmp/A" &
		_jA=$!
		"curl_$_dir" "$SOCKS_B" "$tmp/B" &
		_jB=$!
		wait "$_jA"
		wait "$_jB"
		carrier_delta "$_cb" "$_t" concurrent "$_dir"
		emit "$_t" concurrent A "$_dir" "$tmp/A"
		emit "$_t" concurrent B "$_dir" "$tmp/B"
		_t=$((_t + 1))
	done
}

echo "--- alone control, download ---";   run_alone down
echo "--- concurrent download ---";       run_concurrent down
echo "--- alone control, upload ---";     run_alone up
echo "--- concurrent upload ---";         run_concurrent up

echo "$(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$10==1' | wc -l)"

stop_all

# --- the verdict lines -------------------------------------------------------
# A and B are the MEAN of the concurrent trials; best_alone is the faster of the
# two single-client controls. ratio ~2 = headroom the sender never used; ratio ~1
# = the path is capped and no scheduler can beat it.
for d in down up; do
	awk -F'\t' -v d="$d" '
		/^#/ { next }
		$4 != d { next }
		$2 == "concurrent" { s[$3] += $7; n[$3]++ }
		$2 == "alone"      { if ($7 + 0 > best) best = $7 + 0 }
		END {
			a = (n["A"] ? s["A"] / n["A"] : 0)
			b = (n["B"] ? s["B"] / n["B"] : 0)
			sum = a + b
			r = (best > 0 ? sum / best : 0)
			printf "CAPCHECK %s: A=%.0f B=%.0f sum=%.0f vs best_alone=%.0f ratio=%.2f\n", d, a, b, sum, best, r
		}' "$f"
done

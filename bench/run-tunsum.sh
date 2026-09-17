#!/bin/sh
# run-tunsum.sh — is the ~8 MB/s single-client ceiling the range splitter's
# pipeline, or the tunnel picker putting every stream on one tunnel?
#
#   bench/run-tunsum.sh <exit pk> <results dir> <pins dir> [sink]
#
# run-capcheck.sh already showed the PATH has headroom: two independent
# single-route proxy clients downloading at the same instant summed to
# 10.41 MB/s where each alone does ~8. One client with `--tunnels 2` pinned to
# those same two routes, downloading ONE object through the HTTP range
# splitter, gets 8.05 (mux-compose-T2xL1), i.e. no better than one route. Two
# readings fit that:
#
#   (a) the splitter's single-client pipeline is the cap — chunk fetch ->
#       in-order writer -> one browser socket — so no picker can do better;
#   (b) the picker puts every stream on one tunnel, so the second tunnel is
#       never asked for a byte.
#
# This separates them WITHOUT the splitter. ONE default client, `--tunnels 2`,
# tunnel 1 pinned to Amsterdam and tunnel 2 to Atlanta (one leg each, so a
# tunnel IS a route), and the load is two plain concurrent SOCKS downloads —
# two browser connections, no splitter, no in-order writer, nothing shared but
# the pick. Each accepted connection is a LONE stream, so it is picked by
# pickSessionFor(pickAny) (pkg/skysocks/client.go:848), which takes the
# lowest measured RTT and only breaks an exact RTT tie by stream count.
#
#   A: two concurrent 50 MB downloads of two different objects, no splitter
#   B: one 50 MB download alone, no splitter          (this session's baseline)
#   C: one 50 MB download through the splitter        (same-session control for
#                                                      mux-compose-T2xL1)
#   D: two concurrent 50 MB downloads, splitter on    (does the PAIR pass 8?)
#
#   A sums to ~10 and the recv deltas show one stream per tunnel
#       -> the picker is fine for plain streams and (a) holds: the splitter
#          pipeline is the single-client cap.
#   A sums to ~8 and one transport carries ~100 MB while the other carries ~0
#       -> (b): the picker stacked both streams on one tunnel; the second
#          tunnel is idle capacity the client never asked for.
#   A sums to ~8 with the bytes SPLIT evenly
#       -> neither: something downstream of the client (exit, host NIC) caps a
#          single client at 8 regardless of how many tunnels it uses.
#
# tunnel_split is per TRIAL, not per stream: it is the recv byte delta of each
# pinned first-hop transport across the trial (`visor state --select
# transports`, the same counters run-compose.sh's .carrier.tsv uses). With two
# streams and two disjoint pinned tunnels that is exactly the evidence needed —
# one tunnel at ~100 MB and the other at ~0 can only mean both streams rode it.
#
# Rows go to <results dir>/tunsum.tsv. The splitter is a `proxy start` flag
# (--range-port), so C/D restart the client and re-pin; the pinning is
# re-asserted and re-checked for every phase.
#
# Run this under the campaign rig lock ($S/rig.lock) — like run-capcheck.sh it
# does not take the lock itself, because the caller's chain already holds it.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=${1:-}; out=${2:-}; pins=${3:-}; sink=${4:-http://127.0.0.1:18080}
[ -n "$exit_pk" ] && [ -n "$out" ] && [ -n "$pins" ] || {
	echo "usage: bench/run-tunsum.sh <exit pk> <results dir> <pins dir> [sink]" >&2
	exit 2
}
here=$(dirname "$0")
mkdir -p "$out"
f="$out/tunsum.tsv"

SIZE=${SIZE:-50000000}          # 50 MB, the campaign's big size
TRIALS=${TRIALS:-3}
RG_WAIT=${RG_WAIT:-30}
NAME=${NAME:-skysocks-client}   # the DEFAULT instance, so status.skysocks shows it
SOCKS=${SOCKS:-127.0.0.1:1080}
PIN1=${PIN1:-03f57e7c}          # tunnel 1: Amsterdam/NL, tp fdab37dd
PIN2=${PIN2:-0371ab4b}          # tunnel 2: US-Atlanta,   tp 95839ad0
RANGE_PORT=${RANGE_PORT:-18080} # the sink's port, for phases C and D only
RTT_SETTLE=${RTT_SETTLE:-15}    # tunnelRTTProbeInterval is 5s: let both tunnels be pinged

tmp=$(mktemp -d)
cleaned=0
stop_all() {
	[ "$cleaned" = 1 ] && return 0
	cleaned=1
	stop_app_clean "$NAME" || echo "$NAME: dst_ports $rg_left left behind"
}
trap 'stop_all; rm -rf "$tmp"' EXIT
trap 'exit 130' INT TERM

# --- rig helpers (the shapes of run-compose.sh / run-capcheck.sh) ------------
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
tp_ips() { $CLI cli tp ls --json 2>/dev/null | jq -c 'map({(.id): (.remote_ip // "")}) | add // {}' 2>/dev/null; }
mux_info() {
	_mi=$($CLI cli proxy mux info -n "$1" --json 2>/dev/null) || return 1
	_ips=$(tp_ips); [ -n "$_ips" ] || _ips='{}'
	echo "$_mi" | jq --argjson ips "$_ips" \
		'map(if (.legs | type) == "array"
			then .legs |= map(. + {remote_ip: ($ips[.transport_id // ""] // "")})
			else . end)' 2>/dev/null || echo "$_mi"
}
rg_ports() { mux_info "$1" 2>/dev/null | jq -r '.[]?.desc.dst_port' 2>/dev/null | tr '\n' ' ' | sed 's/ *$//'; }
wait_no_rg() {
	_w=0
	while :; do
		rg_left=$(rg_ports "$1")
		[ -z "$rg_left" ] && return 0
		[ "$_w" -ge "$RG_WAIT" ] && return 1
		sleep 2; _w=$((_w + 2))
	done
}
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
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
wait_width() { # <app> <groups> <legs each>
	_i=1
	while [ $_i -le 12 ]; do
		_got=$(mux_info "$1" | jq --argjson g "$2" --argjson l "$3" '[.[] | select((.legs|length) >= $l)] | length >= $g' 2>/dev/null)
		[ "$_got" = true ] && return 0
		sleep 2; _i=$((_i + 1))
	done
	return 1
}
warm() { # <socks> <label>
	_bad=0
	for _ in 1 2 3 4; do
		_code=$(curl -s -m 30 --socks5-hostname "$1" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
		[ "$_code" = 200 ] || _bad=$((_bad + 1))
	done
	echo "$2 warm: $((4 - _bad))/4 probes ok"
	[ $_bad -eq 0 ]
}

# --- one download, launchable in background ---------------------------------
# Two DIFFERENT objects = two different URLs: the sink keys its body on ?bytes=
# only (cmd/skywire-cli/commands/proxy/loadtest.go:278 loadtestFixed) and sets
# Cache-Control: no-store, so an extra query string changes the request without
# changing what is certified by X-Sha256.
curl_down() { # <prefix> <url>
	curl -s --socks5-hostname "$SOCKS" -m 600 -D "$1.h" -o "$1.b" \
		-w '%{http_code} %{size_download} %{time_total} %{speed_download}' \
		"$2" > "$1.w" 2>/dev/null
}
emit() { # <trial> <mode> <stream> <prefix> <split>
	_tr=$1; _mode=$2; _st=$3; _p=$4; _split=$5
	set -- $(cat "$_p.w" 2>/dev/null)
	_http=${1:-000}; _got=${2:-0}; _secs=${3:-0}; _bps=${4:-0}
	_want=$(tr -d '\r' < "$_p.h" 2>/dev/null | awk 'tolower($1)=="x-sha256:"{print $2}')
	_have=$(sha256sum "$_p.b" 2>/dev/null | cut -d' ' -f1)
	_ok=0
	[ -n "$_want" ] && [ "$_want" = "$_have" ] && _ok=1
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$_tr" "$_mode" "$_st" "$_got" "$_secs" "$_bps" "$_split" "$_http" "$_ok" >> "$f"
	[ "$_ok" = 1 ] || echo "WARNING trial=$_tr $_mode $_st: hash NOT verified (http=$_http got=$_got)"
	echo "  trial=$_tr $_mode $_st ${_bps} B/s in ${_secs}s http=$_http hash_ok=$_ok"
}
# the recv counters of the two pinned first-hop transports, before/after a trial
snap() { echo "$(tp_counters "$tp1" | cut -d' ' -f2),$(tp_counters "$tp2" | cut -d' ' -f2)"; }
split_of() { # <before snapshot> -> "<tp1 short>=<recv delta> <tp2 short>=<recv delta>"
	_a=$(snap)
	_b1=${1%%,*}; _b2=${1##*,}; _a1=${_a%%,*}; _a2=${_a##*,}
	if [ -n "$_b1" ] && [ -n "$_a1" ] && [ -n "$_b2" ] && [ -n "$_a2" ]; then
		echo "$(echo "$tp1" | cut -c1-8)=$((_a1 - _b1)) $(echo "$tp2" | cut -c1-8)=$((_a2 - _b2))"
	else
		echo "?"
	fi
}

# --- bring the client up in one shape ---------------------------------------
# start_pinned <range port|"">: two tunnels, tunnel 1 -> PIN1, tunnel 2 -> PIN2,
# one leg each (so a tunnel is exactly one route and the two are disjoint).
# Exits non-zero if the realized shape is not 2 groups x 1 pinned leg — a
# half-built session's rows are indistinguishable from real ones in the TSV.
start_pinned() {
	_rp=$1
	stop_app_clean "$NAME" || return 1
	$CLI cli proxy mux cap 1 >/dev/null 2>&1
	$CLI cli proxy mux width 1 >/dev/null 2>&1
	# --route pins ONE group and is refused with --tunnels >1: start plain, then
	# pin each group by its own dst_port (#4967).
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$NAME" -a "$SOCKS" --tunnels 2 \
		${_rp:+--range-port $_rp} 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
	sleep 5
	mux_info "$NAME" > "$tmp/legs.json"
	_ngroups=$(jq 'length' "$tmp/legs.json" 2>/dev/null || echo 0)
	_ndst=$(jq '[.[].desc.dst_port] | unique | length' "$tmp/legs.json" 2>/dev/null || echo 0)
	[ "$_ngroups" -eq 2 ] && [ "$_ndst" -eq 2 ] || {
		echo "tunsum: $_ngroups route group(s) with $_ndst distinct dst_ports — want 2x2" >&2
		return 1
	}
	_i=0
	for _dp in $(jq -r '.[].desc.dst_port' "$tmp/legs.json"); do
		_i=$((_i + 1))
		eval "_pin=\$PIN$_i"
		[ -f "$pins/via-$_pin.json" ] || { echo "tunsum: pin $pins/via-$_pin.json not found" >&2; return 1; }
		jq -s 'add' "$pins/via-$_pin.json" > "$tmp/rg$_dp.target.json"
		cp "$tmp/rg$_dp.target.json" "$out/tunsum.rg$_dp.target.json"
		echo "tunsum: rg$_dp <- $_pin"
		timeout 300 $CLI cli proxy mux set -n "$NAME" --rg "$_dp" --legs "$tmp/rg$_dp.target.json" --prune 2>&1 | grep -iv debug | head -5
	done
	$CLI cli proxy mux cap 1 >/dev/null 2>&1
	$CLI cli proxy mux width 1 >/dev/null 2>&1
	sleep 3
	wait_width "$NAME" 2 1 || echo "$NAME: engine did not reach 2x1 within 24s — checking the shape it has"
	mux_info "$NAME" > "$tmp/legs.json"
	cp "$tmp/legs.json" "$out/tunsum.legs.json"
	tp1=$(jq -r '.[].forward[0].TpID' "$pins/via-$PIN1.json")
	tp2=$(jq -r '.[].forward[0].TpID' "$pins/via-$PIN2.json")
	_have=$(jq -r '.[].legs[].transport_id' "$tmp/legs.json" 2>/dev/null)
	for _w in $tp1 $tp2; do
		echo "$_have" | grep -q "$_w" || { echo "tunsum: pinned leg $_w NOT present (have: $(echo "$_have" | tr '\n' ' '))" >&2; return 1; }
	done
	_shape=$(jq -r '[.[].legs | length] | join("+")' "$tmp/legs.json" 2>/dev/null)
	[ "$_shape" = "1+1" ] || { echo "tunsum: legs per rg $_shape, want 1+1" >&2; return 1; }
	_desc=$(jq -r '[.[] | "rg\(.desc.dst_port)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$tmp/legs.json")
	echo "$NAME: 2 route groups, 1+1 legs, range_port=${_rp:-none} :: $_desc"
	warm "$SOCKS" "$NAME" || echo "$NAME: probes failing — running the phase anyway"
	# a lone stream is picked on measured RTT, and the RTT probe ticks every 5 s
	# (tunnelRTTProbeInterval, pkg/skysocks/client.go:1087): give BOTH tunnels a
	# ping before the first pick, so phase A tests the steady-state policy.
	sleep "$RTT_SETTLE"
	return 0
}

# --- phases ------------------------------------------------------------------
one_alone() { # <mode>
	_t=1
	while [ "$_t" -le "$TRIALS" ]; do
		_b=$(snap)
		curl_down "$tmp/s1" "$sink/?bytes=$SIZE&o=a"
		_sp=$(split_of "$_b")
		printf '# carrier\t%s\t%s\t%s\n' "$_t" "$1" "$_sp" >> "$f"
		emit "$_t" "$1" s1 "$tmp/s1" "$(echo $_sp | tr ' ' ';')"
		echo "  trial=$_t $1 split: $_sp"
		_t=$((_t + 1))
	done
}
two_concurrent() { # <mode>
	_t=1
	while [ "$_t" -le "$TRIALS" ]; do
		_b=$(snap)
		curl_down "$tmp/s1" "$sink/?bytes=$SIZE&o=a" & _j1=$!
		curl_down "$tmp/s2" "$sink/?bytes=$SIZE&o=b" & _j2=$!
		wait "$_j1"; wait "$_j2"
		_sp=$(split_of "$_b")
		printf '# carrier\t%s\t%s\t%s\n' "$_t" "$1" "$_sp" >> "$f"
		emit "$_t" "$1" s1 "$tmp/s1" "$(echo $_sp | tr ' ' ';')"
		emit "$_t" "$1" s2 "$tmp/s2" "$(echo $_sp | tr ' ' ';')"
		echo "  trial=$_t $1 split: $_sp"
		_t=$((_t + 1))
	done
}

local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD 2>/dev/null)
ec=$(exit_commit)
echo "local=$local_commit exit=$ec sink=$sink size=$SIZE trials=$TRIALS pins=$PIN1,$PIN2"
$CLI cli route settings --prefer default >/dev/null 2>&1

printf '# tunsum exit=%s local=%s exit_commit=%s app=%s@%s tunnels=2 pin1=%s pin2=%s range_port=%s sink=%s size=%s trials=%s\n' \
	"$exit_pk" "$local_commit" "$ec" "$NAME" "$SOCKS" "$PIN1" "$PIN2" "$RANGE_PORT" "$sink" "$SIZE" "$TRIALS" > "$f"
echo "# trial	mode	stream	bytes	seconds	goodput_bps	tunnel_split	http	hash_ok" >> "$f"

echo "=== phase 1: --tunnels 2 pinned $PIN1+$PIN2, NO splitter ==="
start_pinned "" || { echo "tunsum: phase 1 setup failed — no rows recorded" >&2; exit 1; }
echo "--- A: two concurrent 50 MB downloads, no splitter ---"; two_concurrent A
echo "--- B: one 50 MB download alone, no splitter ---";       one_alone B

echo "=== phase 2: same shape, splitter on port $RANGE_PORT ==="
if start_pinned "$RANGE_PORT"; then
	echo "--- C: one 50 MB download, splitter on ---";            one_alone C
	echo "--- D: two concurrent 50 MB downloads, splitter on ---"; two_concurrent D
else
	echo "tunsum: phase 2 setup failed — the A/B rows stand, C/D are missing" >&2
fi

echo "$(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$9==1' | wc -l)"
stop_all
echo "route groups after stop: '$(rg_ports "$NAME")'"

# --- the verdict line --------------------------------------------------------
awk -F'\t' '
	/^#/ { next }
	{ s[$2 "/" $3] += $6; n[$2 "/" $3]++ }
	$2 == "A" && $3 == "s1" { split_a[$7] = 1 }
	END {
		a1 = (n["A/s1"] ? s["A/s1"] / n["A/s1"] : 0)
		a2 = (n["A/s2"] ? s["A/s2"] / n["A/s2"] : 0)
		b  = (n["B/s1"] ? s["B/s1"] / n["B/s1"] : 0)
		c  = (n["C/s1"] ? s["C/s1"] / n["C/s1"] : 0)
		d  = (n["D/s1"] ? s["D/s1"] / n["D/s1"] : 0) + (n["D/s2"] ? s["D/s2"] / n["D/s2"] : 0)
		sp = ""
		for (k in split_a) sp = sp (sp ? " | " : "") k
		printf "TUNSUM A: s1=%.0f s2=%.0f sum=%.0f split=%s | B alone=%.0f | C split-one=%.0f | D split-two sum=%.0f\n", \
			a1, a2, a1 + a2, sp, b, c, d
	}' "$f"

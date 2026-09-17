#!/bin/sh
# run-refs.sh — the reference sets of the mux campaign, unattended.
#
#   bench/run-refs.sh <exit pk> <out dir> <pins dir> [trials] [sink]
#
# Sets (one TSV each, header line first):
#   ref-direct-stcpr    a --direct proxy with stcpr preferred
#   ref-direct-squicr   a --direct proxy with squicr preferred
#   ref-via-<short>     one proxy per pin file <pins>/via-<short>.json, pinned
#                       with --route (exactly that two-hop leg)
# Every row is bench.sh's hash-verified transfer; a companion
# <set>.carrier.tsv records, per row, the byte deltas of the transport(s) the
# row is supposed to ride, so the carrier claim is checked, not assumed.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
sizes="10000000 50000000"
# APP=skysocks-client drives every set through the DEFAULT proxy instance on
# :1080 (APP_PORT overrides), so status.skysocks in a browser shows the session
# under test; sets run one at a time and each stops the instance first.
app_name() { echo "${APP:-$1}"; }
app_addr() { if [ -n "${APP:-}" ]; then echo "127.0.0.1:${APP_PORT:-1080}"; else echo "127.0.0.1:$1"; fi; }

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
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
# warm <socks> <name> <start args...>: four 100 KB probes must all pass; a proxy
# that comes up in a bad state (seen right after an exit restart: SOCKS handshake
# failures on about half the connections) is restarted, up to three times.
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
		stop_app "$name"
		timeout 240 $CLI cli proxy start -n "$name" -a "$socks" "$@" >/dev/null 2>&1
		try=$((try + 1))
	done
	return 1
}
exit_tp_counters() { # <tp id> -> "sent recv" as seen by the EXIT (hop 2 of a pinned route)
	timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id==$id) | "\(.log.sent) \(.log.recv)"'
}

# run_set <set> <socks> <tp ids (space separated)> <header> [exit-side tp id]
# mux_events <set> <app>: the router's leg events for this app since the set began (criterion 7: churn with reasons)
mux_events() {
	$CLI cli visor state --select diag --json 2>/dev/null |
		jq --arg app "$2" --arg since "$set_started" '[.diag.mux_events[]? | select(.app==$app and .at[0:19] >= $since)]' > "$out/$1.mux_events.json" 2>/dev/null
	echo "$1: $(jq 'length' "$out/$1.mux_events.json" 2>/dev/null || echo 0) mux events: $(jq -r '[.[] | .event + "(" + (.reason // "") + ")"] | join(" ")' "$out/$1.mux_events.json" 2>/dev/null | cut -c1-300)"
}
run_set() {
	set_name=$1; socks=$2; tps=$3; header=$4; exit_tp=${5:-}; set_started=$(date +%Y-%m-%dT%H:%M:%S) # visor-local time, the zone the event ring is stamped in
	f="$out/$set_name.tsv"; c="$out/$set_name.carrier.tsv"
	echo "# $header" > "$f"
	echo "# row	tp	sent_delta	recv_delta" > "$c"
	[ -n "$exit_tp" ] && xb=$(exit_tp_counters "$exit_tp")
	row=0
	for n in $sizes; do
		for dir in down up; do
			t=1
			while [ $t -le "$trials" ]; do
				row=$((row + 1))
				before=""
				for tp in $tps; do before="$before $tp:$(tp_counters "$tp" | tr ' ' ',')"; done
				"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
				for tp in $tps; do
					b=$(echo "$before" | tr ' ' '\n' | grep "^$tp:" | cut -d: -f2)
					a=$(tp_counters "$tp" | tr ' ' ',')
					[ -n "$a" ] && [ -n "$b" ] || { echo "$row	$tp	?	?" >> "$c"; continue; }
					echo "$row	$tp	$(( ${a%,*} - ${b%,*} ))	$(( ${a#*,} - ${b#*,} ))" >> "$c"
				done
				# the legs the session holds right after the row: the carrier claim needs exactly the pinned one
				if [ -n "${cur_app:-}" ]; then
					info=$($CLI cli proxy mux info -n "$cur_app" --json 2>/dev/null)
					echo "$row	legs	$(echo "$info" | jq -r '[.[].legs[].transport_id[0:8]] | join(",")')	-" >> "$c"
					# loss-recovery counters (sender retx window + SACK feedback, receiver frontier) after every row
					echo "$row	$(echo "$info" | jq -c '[.[] | {rg: .desc.dst_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
				fi
				t=$((t + 1))
			done
		done
	done
	if [ -n "$exit_tp" ]; then
		xa=$(exit_tp_counters "$exit_tp")
		echo "# exit-side $exit_tp sent_delta=$(( ${xa% *} - ${xb% *} )) recv_delta=$(( ${xa#* } - ${xb#* } )) over the set" >> "$c"
	fi
	# the EXIT's view of the same group(s) at the end of the set (its receiver-side wedge counters)
	if [ -n "${cur_app:-}" ]; then
		ports=$($CLI cli proxy mux info -n "$cur_app" --json 2>/dev/null | jq -c '[.[].desc.dst_port]')
		echo "# exit	$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
	fi
	echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
	[ -n "${cur_app:-}" ] && mux_events "$set_name" "$cur_app"
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec"

# --- direct references -------------------------------------------------------
# DIRECT_TYPES="" skips them (rerunning pinned sets alone); PINS_GLOB narrows the pins.
for typ in ${DIRECT_TYPES-stcpr squicr}; do
	case $typ in
	stcpr) pref=default; port=1082 ;;
	squicr) pref=squicr,stcpr,sudph,stcp,swtr,swsr,webrtc,dmsg; port=1083 ;;
	esac
	$CLI cli route settings --prefer "$pref" >/dev/null 2>&1
	name=$(app_name "ref$typ"); socks=$(app_addr "$port")
	cur_app=""; stop_app "$name"
	timeout 180 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --direct >/dev/null 2>&1
	warm "$socks" "$name" -k "$exit_pk" --direct || echo "$name: still failing probes after 3 restarts"
	tp=$(tp_of "$exit_pk" "$typ")
	if [ -z "$tp" ]; then
		echo "$name: no $typ transport to the exit after the proxy started — skipping set"
		stop_app "$name"; continue
	fi
	# a 10 MB probe names the carrier before the set starts
	b=$(tp_counters "$tp"); "$here/bench.sh" "$socks" "$sink" 10000000 down probe >/dev/null; a=$(tp_counters "$tp")
	echo "$name carrier probe: tp=$tp recv_delta=$(( ${a#* } - ${b#* } ))"
	run_set "ref-direct-$typ" "$socks" "$tp" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name route=direct transport=$typ tp=$tp sink=$sink"
	cur_app=""; stop_app "$name"
done
$CLI cli route settings --prefer default >/dev/null 2>&1

# --- one proxy per pinned two-hop route --------------------------------------
port=1091
for pin in "$pins"/${PINS_GLOB:-via-*.json}; do
	short=$(basename "$pin" .json | sed 's/^via-//')
	name=$(app_name "via$short"); socks=$(app_addr "$port"); cur_app=$name
	stop_app "$name"
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --route "$pin" 2>&1 | grep -v DEBUG | grep -i "route pinned\|leg\|FATAL" | head -3
	legs=$($CLI cli proxy mux info -n "$name" --json 2>/dev/null | jq -r '.[0].legs[]? | "\(.transport_id) \(.tp_type)"')
	want=$(jq -r '.[0].forward[0].TpID' "$pin")
	hops=$(jq -r '.[0].forward | map(.From[0:8] + ">" + .To[0:8] + "@" + .TpID[0:8]) | join(",")' "$pin")
	if ! echo "$legs" | grep -q "^$want"; then
		echo "$name: pinned leg $want NOT present (legs: $(echo "$legs" | tr '\n' ' ')) — skipping set"
		port=$((port + 1)); continue
	fi
	if [ "$(echo "$legs" | wc -l)" -ne 1 ]; then
		echo "$name: WARNING $(echo "$legs" | wc -l) legs, expected exactly the pinned one"
	fi
	warm "$socks" "$name" -k "$exit_pk" --route "$pin" || echo "$name: still failing probes after 3 restarts"
	hop2=$(jq -r '.[0].forward[1].TpID' "$pin")
	run_set "ref-via-$short" "$socks" "$want" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name route=$hops transport=stcpr,stcpr legs=[$(echo "$legs" | tr '\n' ';')] sink=$sink" "$hop2"
	[ -n "${APP:-}" ] && stop_app "$name"
	port=$((port + 1))
done

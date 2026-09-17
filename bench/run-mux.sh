#!/bin/sh
# run-mux.sh — stream-level and packet-level multiplexing sets, unattended.
#
#   bench/run-mux.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]
#
# Sets (one TSV each, same row shape as run-refs.sh):
#   mux-tunnels-N   proxy start --tunnels N: N independent tunnels (one route
#                   group each), the visor steers each extra tunnel onto a
#                   different first-hop transport. Stream-level multiplexing.
#   mux-legs-N      one routed proxy, then `proxy mux set --legs --prune` to
#                   exactly N pinned two-hop legs in ONE route group.
#                   Packet-level multiplexing; the group's src_port must not
#                   change across the set.
# <set>.legs.json keeps the `mux info --json` snapshot taken before the set
# (exact groups, legs, transport types, remote pks, and the remote public IP
# each leg's transport lands on) and <set>.carrier.tsv the per-row byte
# deltas of every first-hop transport the set is supposed to ride.
# <set>.exit-recovery.tsv is the EXIT's view of the same route group(s)
# after every row (EXIT_SNAP=0 turns it off, EXIT_SNAP_TIMEOUT bounds it).
# [pin order] is a space-separated list of pin shorts, best route first; legs-N
# takes the first N. Default: every <pins>/via-*.json in name order.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
order=${6:-$(ls "$pins"/via-*.json | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
sizes="10000000 50000000"
tunnel_counts=${TUNNELS-"2 3"}
leg_counts=${LEGS-"2 3 5"}
# APP=skysocks-client drives every set through the DEFAULT proxy instance on
# :1080 (APP_PORT overrides), so status.skysocks in a browser shows the session
# under test; sets run one at a time and each stops the instance first.
app_name() { echo "${APP:-$1}"; }
app_addr() { if [ -n "${APP:-}" ]; then echo "127.0.0.1:${APP_PORT:-1080}"; else echo "127.0.0.1:$1"; fi; }

tp_counters() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id==$id) | "\(.log.sent) \(.log.recv)"'
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
# EXIT_SNAP=1 (the default) adds ONE exit-side query of the same route group(s)
# after EVERY row, written to <set>.exit-recovery.tsv. The end-of-set snapshot
# alone cannot date a receiver-side stall to a row; this can. EXIT_SNAP=0
# restores the old end-of-set-only behaviour exactly.
EXIT_SNAP=${EXIT_SNAP:-1}
EXIT_SNAP_TIMEOUT=${EXIT_SNAP_TIMEOUT:-40}
# exit_rgs <timeout> <ports json array>: the exit's view of the groups whose
# src_port is in <ports> (our desc.dst_port is the exit's desc.src_port).
# `--select mux_route_groups` is the SelectMux projection: the visor builds only
# that subtree (~75 KB) instead of the ~900 KB full snapshot. Empty on failure.
exit_rgs() {
	timeout "$1" $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null |
		jq -c --argjson p "$2" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms}]}]' 2>/dev/null
}
# tp id -> remote public IP. `mux info` names a leg's transport but not where
# it lands; `tp ls --json` carries remote_ip per transport id. Empty object on
# any failure so the enrichment below degrades to remote_ip "".
tp_ips() { $CLI cli tp ls --json 2>/dev/null | jq -c 'map({(.id): (.remote_ip // "")}) | add // {}' 2>/dev/null; }
# `mux info --json` with a remote_ip added to every leg. Pure addition: on a
# failed lookup, or if jq chokes, the untouched mux info snapshot is emitted.
mux_info() {
	_mi=$($CLI cli proxy mux info -n "$1" --json 2>/dev/null) || return 1
	_mi_ips=$(tp_ips)
	[ -n "$_mi_ips" ] || _mi_ips='{}'
	echo "$_mi" | jq --argjson ips "$_mi_ips" \
		'map(if (.legs | type) == "array"
			then .legs |= map(. + {remote_ip: ($ips[.transport_id // ""] // "")})
			else . end)' 2>/dev/null || echo "$_mi"
}
# --- route-group hygiene (campaign16, 2026-09-17) ------------------------------
# `proxy stop` does not always deregister the app's route groups. A group that
# outlives the app makes the NEXT set's `proxy start --route` fail its reconcile
# ("app=... has N active route groups; pass --rg <port>"); the client then rides
# an unpinned group and every row of that set is worthless. No CLI closes a group
# directly, so the only lever is a second `proxy stop` — and when that fails too
# the set is marked INVALID and its rows are never recorded.
RG_WAIT=${RG_WAIT:-30} # seconds to wait for an app's route groups to go away
# rg_ports <app>: the app's route group dst_ports, space separated; empty = none.
# A failed or refused `mux info` also reads empty — the assert immediately before
# `proxy start --route` and the leg-set check after it still catch that case.
rg_ports() { mux_info "$1" 2>/dev/null | jq -r '.[]?.desc.dst_port' 2>/dev/null | tr '\n' ' ' | sed 's/ *$//'; }
# wait_no_rg <app>: poll until the app reports zero route groups (<= RG_WAIT s).
# The last reading is left in $rg_left.
wait_no_rg() {
	_w=0
	while :; do
		rg_left=$(rg_ports "$1")
		[ -z "$rg_left" ] && return 0
		[ "$_w" -ge "$RG_WAIT" ] && return 1
		sleep 2; _w=$((_w + 2))
	done
}
# stop_app_clean <app>: stop, then insist on zero route groups, retrying the stop
# once. Returns 1 with the surviving dst_ports in $rg_left.
stop_app_clean() {
	stop_app "$1"
	wait_no_rg "$1" && return 0
	echo "$1: route group(s) still registered ${RG_WAIT}s after proxy stop: dst_ports $rg_left — stopping again"
	stop_app "$1"
	wait_no_rg "$1" && return 0
	echo "$1: route group(s) STILL registered after a second proxy stop: dst_ports $rg_left"
	return 1
}
# invalid_set <set> <reason>: no rows for this set, ever. summarize.sh and
# verdict.sh skip any set carrying a .INVALID marker and print the reason.
invalid_set() {
	printf '%s\n' "$2" > "$out/$1.INVALID"
	echo "$1: INVALID — $2"
}
# abort_set <set> <reason>: invalid_set, then leave the rig as a finished set
# leaves it (steady width 2, app stopped) before the next set starts.
abort_set() {
	invalid_set "$1" "$2"
	$CLI cli proxy mux width 2 >/dev/null 2>&1
	[ -n "${cur_app:-}" ] && { stop_app_clean "$cur_app" || echo "$cur_app: dst_ports $rg_left left behind by an aborted set"; }
	return 0
}
warm() {
	socks=$1; name=$2
	bad=0
	for i in 1 2 3 4; do
		code=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
		[ "$code" = 200 ] || bad=$((bad + 1))
	done
	echo "$name warm: $((4 - bad))/4 probes ok"
	[ $bad -eq 0 ]
}
# mux_events <set> <app>: the router's leg events for this app since the set began (criterion 7: churn with reasons)
mux_events() {
	$CLI cli visor state --select diag --json 2>/dev/null |
		jq --arg app "$2" --arg since "${setup_started:-$set_started}" '[.diag.mux_events[]? | select(.app==$app and .at[0:19] >= $since)]' > "$out/$1.mux_events.json" 2>/dev/null
	echo "$1: $(jq 'length' "$out/$1.mux_events.json" 2>/dev/null || echo 0) mux events: $(jq -r '[.[] | .event + "(" + (.reason // "") + ")"] | join(" ")' "$out/$1.mux_events.json" 2>/dev/null | cut -c1-300)"
}
run_set() { # <set> <socks> <tp ids> <header>
	set_name=$1; socks=$2; tps=$3; header=$4; set_started=$(date +%Y-%m-%dT%H:%M:%S) # visor-local time, the zone the event ring is stamped in
	f="$out/$set_name.tsv"; c="$out/$set_name.carrier.tsv"
	echo "# $header" > "$f"
	echo "# row	tp	sent_delta	recv_delta" > "$c"
	[ "$EXIT_SNAP" = 1 ] && printf '# row\texit_mux_route_groups\n' > "$out/$set_name.exit-recovery.tsv"
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
				# legs held + loss-recovery counters (sender retx window + SACK feedback, receiver frontier) after every row
				info=$(mux_info "$cur_app")
				echo "$row	legs	$(echo "$info" | jq -r '[.[] | (.desc.dst_port|tostring) + ":" + ([.legs[].transport_id[0:8]] | join(","))] | join(" ")')	-" >> "$c"
				echo "$row	$(echo "$info" | jq -c '[.[] | {rg: .desc.dst_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
				p=$(echo "$info" | jq -c '[.[].desc.dst_port]')
				# the EXIT's view of the same group(s) after this row. A slow exit
				# must never stall the set, so it is bounded and a failure is
				# recorded as an empty object rather than retried.
				xr=""
				if [ "$EXIT_SNAP" = 1 ]; then
					xr=$(exit_rgs "$EXIT_SNAP_TIMEOUT" "$p")
					[ -n "$xr" ] || { xr='{}'; echo "$set_name row $row: exit snapshot failed/timed out after ${EXIT_SNAP_TIMEOUT}s — empty object recorded"; }
					printf '%s\t%s\n' "$row" "$xr" >> "$out/$set_name.exit-recovery.tsv"
				fi
				# a failed row loses the exit's view when the session re-dials: snapshot it NOW
				if [ "$(tail -1 "$f" | cut -f8)" != 1 ]; then
					# the per-row snapshot above is that snapshot when it worked; only
					# query the exit a second time when EXIT_SNAP is off or it failed
					[ -n "$xr" ] && [ "$xr" != '{}' ] || xr=$(exit_rgs 60 "$p")
					echo "# exit-on-fail $row	$xr" >> "$out/$set_name.recovery.tsv"
				fi
				t=$((t + 1))
			done
		done
	done
	echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
	mux_events "$set_name" "$cur_app"
	# the EXIT's view of the same group(s) at the end of the set (its receiver-side wedge counters)
	# our desc.dst_port (the ephemeral local port) is the exit's desc.src_port for the same group
	ports=$(mux_info "$cur_app" | jq -c '[.[].desc.dst_port]')
	x=""; for try in 1 2 3; do
		x=$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms}]}]')
		[ -n "$x" ] && [ "$x" != "[]" ] && break
		sleep 3
	done
	echo "# exit	$x" >> "$out/$set_name.recovery.tsv"
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec order=$order"

# --- stream-level: N tunnels ---------------------------------------------------
port=1101
for N in $tunnel_counts; do
	name=$(app_name "tun$N"); socks=$(app_addr "$port"); set_name="mux-tunnels-$N"; cur_app=$name
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	# a group left over from the previous set would be dialed instead of this
	# set's own tunnels: refuse to measure until the app owns nothing.
	stop_app_clean "$name" || { abort_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"; port=$((port + 1)); continue; }
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --tunnels "$N" ${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} ${RANGE_CONCURRENCY:+--range-concurrency $RANGE_CONCURRENCY} 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
	sleep 5
	mux_info "$name" > "$out/$set_name.legs.json"
	groups=$(jq 'length' "$out/$set_name.legs.json" 2>/dev/null || echo 0)
	tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
	desc=$(jq -r '[.[] | "rg\(.desc.dst_port)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
	echo "$name: $groups route group(s); first-hop tps: $tps"
	# the target IS N tunnels; a set that came up in another shape measures
	# something else and must not be recorded as this set (campaign16).
	[ "$groups" -eq "$N" ] || { abort_set "$set_name" "shape differs from target: $groups route group(s), asked for $N (groups=$desc)"; port=$((port + 1)); continue; }
	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=$N route_groups=$groups groups=$desc sink=$sink"
	# the rows are already written, so a leftover group here invalidates the
	# NEXT set (its own pre-setup check), not this one — just say so loudly.
	stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set — the next set will be invalidated if they persist"
	port=$((port + 1))
done

# --- packet-level: N legs in one route group -----------------------------------
port=1111
for N in $leg_counts; do
	name=$(app_name "leg$N"); socks=$(app_addr "$port"); set_name="mux-legs-$N"; cur_app=$name
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	chosen=$(echo "$order" | tr ' ' '\n' | grep -v '^$' | head -n "$N")
	[ "$(echo "$chosen" | wc -l)" -eq "$N" ] || { echo "$set_name: only $(echo "$chosen" | wc -l) pins available — skipping"; continue; }
	first=$(echo "$chosen" | head -1)
	legs_file="$out/$set_name.target.json"
	for s in $chosen; do cat "$pins/via-$s.json"; done | jq -s 'add' > "$legs_file"
	stop_app_clean "$name" || { abort_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"; port=$((port + 1)); continue; }
	# `proxy start --route` reconciles against the app's live groups and is FATAL
	# when more than one exists; starting anyway leaves the client on an unpinned
	# group. Assert zero one last time immediately before the dial.
	rg_left=$(rg_ports "$name")
	[ -z "$rg_left" ] || { abort_set "$set_name" "$rg_left still registered at the dial — 'proxy start --route' would fail its reconcile"; port=$((port + 1)); continue; }
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --route "$pins/via-$first.json" 2>&1 | grep -iv debug | grep -i "pinned\|running\|error\|fatal" | head -3
	timeout 300 $CLI cli proxy mux set -n "$name" --legs "$legs_file" --prune 2>&1 | grep -iv debug | head -5
	# pin the adaptive engine's active width to N so every pinned leg stripes from the first row
	# (the default steady width is 2: extra legs sit in warm standby until the engine ramps)
	$CLI cli proxy mux cap "$N" >/dev/null 2>&1; $CLI cli proxy mux width "$N" >/dev/null 2>&1
	sleep 3
	mux_info "$name" > "$out/$set_name.legs.json"
	groups=$(jq 'length' "$out/$set_name.legs.json" 2>/dev/null || echo 0)
	port_before=$(jq -r '.[0].desc.dst_port' "$out/$set_name.legs.json")
	nlegs=$(jq '.[0].legs | length' "$out/$set_name.legs.json")
	tps=$(jq -r '.[0].legs[].transport_id' "$out/$set_name.legs.json" | tr '\n' ' ')
	want=$(jq -r '.[].forward[0].TpID' "$legs_file" | tr '\n' ' ')
	desc=$(jq -r '[.[0].legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")' "$out/$set_name.legs.json")
	echo "$name: $groups rg, src_port=$port_before, legs=$nlegs/$N present: $desc"
	missing=0; for w in $want; do echo "$tps" | grep -q "$w" || { echo "$name: pinned leg $w NOT present"; missing=$((missing + 1)); }; done
	# rows for a leg set that is not the target one measure an unknown shape:
	# abort the set instead of recording it (campaign16 produced 20 such rows).
	[ "$groups" -eq 1 ] || { abort_set "$set_name" "$groups route group(s) for a legs set, want exactly 1 (rg_port=$port_before)"; port=$((port + 1)); continue; }
	[ "$nlegs" -eq "$N" ] && [ "$missing" -eq 0 ] || { abort_set "$set_name" "leg set differs from target: legs=$nlegs/$N missing=$missing present=$desc"; port=$((port + 1)); continue; }
	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name legs=$N/$nlegs width=$N rg_port=$port_before pins=$(echo $chosen | tr ' ' ',') legs_desc=$desc sink=$sink"
	port_after=$(mux_info "$name" | jq -r '.[0].desc.dst_port')
	echo "# rg src_port before=$port_before after=$port_after $( [ "$port_before" = "$port_after" ] && echo constant || echo CHANGED)" >> "$out/$set_name.carrier.tsv"
	$CLI cli proxy mux width 2 >/dev/null 2>&1 # back to the default steady width
	echo "$name: rg src_port $port_before -> $port_after"
	stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set — the next set will be invalidated if they persist"
	port=$((port + 1))
done

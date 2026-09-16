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
# (exact groups, legs, transport types, remote pks) and <set>.carrier.tsv the
# per-row byte deltas of every first-hop transport the set is supposed to ride.
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
tunnel_counts=${TUNNELS:-"2 3"}
leg_counts=${LEGS:-"2 3 5"}

tp_counters() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id==$id) | "\(.log.sent) \(.log.recv)"'
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
mux_info() { $CLI cli proxy mux info -n "$1" --json 2>/dev/null; }
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
run_set() { # <set> <socks> <tp ids> <header>
	set_name=$1; socks=$2; tps=$3; header=$4
	f="$out/$set_name.tsv"; c="$out/$set_name.carrier.tsv"
	echo "# $header" > "$f"
	echo "# row	tp	sent_delta	recv_delta" > "$c"
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
				t=$((t + 1))
			done
		done
	done
	echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec order=$order"

# --- stream-level: N tunnels ---------------------------------------------------
port=1101
for N in $tunnel_counts; do
	name="tun$N"; set_name="mux-tunnels-$N"
	stop_app "$name"
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "127.0.0.1:$port" --tunnels "$N" 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
	sleep 5
	mux_info "$name" > "$out/$set_name.legs.json"
	groups=$(jq 'length' "$out/$set_name.legs.json" 2>/dev/null || echo 0)
	tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
	desc=$(jq -r '[.[] | "rg\(.desc.src_port)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
	echo "$name: $groups route group(s); first-hop tps: $tps"
	warm "127.0.0.1:$port" "$name" || echo "$name: probes failing — running the set anyway"
	run_set "$set_name" "127.0.0.1:$port" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=$N route_groups=$groups groups=$desc sink=$sink"
	stop_app "$name"
	port=$((port + 1))
done

# --- packet-level: N legs in one route group -----------------------------------
port=1111
for N in $leg_counts; do
	name="leg$N"; set_name="mux-legs-$N"
	chosen=$(echo "$order" | tr ' ' '\n' | grep -v '^$' | head -n "$N")
	[ "$(echo "$chosen" | wc -l)" -eq "$N" ] || { echo "$set_name: only $(echo "$chosen" | wc -l) pins available — skipping"; continue; }
	first=$(echo "$chosen" | head -1)
	legs_file="$out/$set_name.target.json"
	for s in $chosen; do cat "$pins/via-$s.json"; done | jq -s 'add' > "$legs_file"
	stop_app "$name"
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "127.0.0.1:$port" --route "$pins/via-$first.json" 2>&1 | grep -iv debug | grep -i "pinned\|running\|error\|fatal" | head -3
	timeout 300 $CLI cli proxy mux set -n "$name" --legs "$legs_file" --prune 2>&1 | grep -iv debug | head -5
	sleep 3
	mux_info "$name" > "$out/$set_name.legs.json"
	groups=$(jq 'length' "$out/$set_name.legs.json" 2>/dev/null || echo 0)
	port_before=$(jq -r '.[0].desc.src_port' "$out/$set_name.legs.json")
	nlegs=$(jq '.[0].legs | length' "$out/$set_name.legs.json")
	tps=$(jq -r '.[0].legs[].transport_id' "$out/$set_name.legs.json" | tr '\n' ' ')
	want=$(jq -r '.[].forward[0].TpID' "$legs_file" | tr '\n' ' ')
	desc=$(jq -r '[.[0].legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")' "$out/$set_name.legs.json")
	echo "$name: $groups rg, src_port=$port_before, legs=$nlegs/$N present: $desc"
	missing=0; for w in $want; do echo "$tps" | grep -q "$w" || { echo "$name: pinned leg $w NOT present"; missing=$((missing + 1)); }; done
	[ "$nlegs" -eq "$N" ] && [ "$missing" -eq 0 ] || echo "$name: WARNING leg set differs from target — set recorded as-is"
	warm "127.0.0.1:$port" "$name" || echo "$name: probes failing — running the set anyway"
	run_set "$set_name" "127.0.0.1:$port" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name legs=$N/$nlegs rg_port=$port_before pins=$(echo $chosen | tr ' ' ',') legs_desc=$desc sink=$sink"
	port_after=$(mux_info "$name" | jq -r '.[0].desc.src_port')
	echo "# rg src_port before=$port_before after=$port_after $( [ "$port_before" = "$port_after" ] && echo constant || echo CHANGED)" >> "$out/$set_name.carrier.tsv"
	echo "$name: rg src_port $port_before -> $port_after"
	stop_app "$name"
	port=$((port + 1))
done

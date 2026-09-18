#!/bin/sh
# run-standby.sh — the standby tunnel pool, measured end to end.
#
#   bench/run-standby.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]
#
# One set per run: mux-standby-<pool>, where <pool> is the pool size the session
# SETTLED at — observed, never asked for. The proxy dials its active tunnels and
# then fills the pool in the background until the topology runs out of disjoint
# first hops; this script waits for that fill to go quiet and takes the count it
# finds. A run whose pool never grew past the active set is recorded as
# mux-standby-2 and says so in its header, so a dead pool cannot be mistaken for
# a full one.
#
# The session under test is the DEFAULT proxy instance (APP=skysocks-client on
# :1080, APP/APP_PORT override), started exactly as run-mux.sh starts its
# tunnels-N sets — `proxy start -k <exit> -n <app> -a <addr> --tunnels N` and
# nothing else. The pool ships on by default, so there is no flag to pass;
# STANDBY_POOL is an optional passthrough for the control run (STANDBY_POOL=0
# turns the pool off) and is omitted entirely when unset, which is what keeps
# this script runnable against a develop that has no such flag.
#
# Rows are run-mux.sh's twenty, in run-mux.sh's order and artefacts: 1-5 10 MB
# down, 6-10 10 MB up, 11-15 50 MB down, 16-20 50 MB up (with trials=5; the
# chaos row is trials*2+1 in general).
#
# CHAOS. Row 11 — the first 50 MB download — is the chaos row. CHAOS_AFTER_S (5)
# seconds into the transfer, the first-hop transport of one ACTIVE tunnel is
# removed with `tp rm <id>`, and the row is then timed to the first byte that
# arrives after the cut (ttfb_after_s). Which tunnel is "active": the group whose
# `tunnel_role` says so, when the visor reports that field, and otherwise the
# group whose first-hop transport actually moved the download bytes of rows 1-10
# (the carrier deltas this script is already recording). `tunnel_role` does not
# exist on develop as this is written — it is the field the standby-pool work
# adds to MuxRouteGroupInfo — so it is read as optional and the carrier fallback
# is the path that runs today.
#
# What may be cut is fenced exactly as run-degrade.sh fences its tunnels-2 cut,
# and for the same reason — a transport that cannot be restored to the SAME id
# breaks the rig for every later set:
#   1. it must be the first hop of one of the pinned hop-1 stcpr transports, so
#      `tp add -t stcpr <pk>` rebuilds the identical id (transport ids are
#      MakeTransportID over the sorted edge keys + type);
#   2. it must not be the transport to the EXIT;
#   3. it must not also carry another route group.
# A run with no candidate passing all three is INVALID before a single row is
# measured, rather than measured and then explained.
#
# RESTORE. The cut transport is re-added the way run-degrade.sh re-adds it — a
# `tp add -t stcpr <pk>` retry loop against `tp ls`, keyed on the PIN's remote pk
# — immediately after the chaos row, and every hop-1 pin is swept once more at
# the end of the set. The sweep only touches this visor's hop-1 transports; if it
# cannot put one back it says so and the operator's rig-restore.sh (which also
# owns the exit's hop-2 side) is the next step.
#
# Artefacts, all named for the set so summarize.sh / verdict.sh keep working
# (they glob mux-*; the extra TSVs below carry no size/direction columns, so the
# summary table steps over them exactly as it steps over .recovery.tsv):
#   <set>.tsv             the twenty rows, bench.sh's columns
#   <set>.carrier.tsv     per-row byte deltas of every first-hop transport
#   <set>.legs.json       the settled pool: groups, legs, hops (FULL pks),
#                         remote_ip, and tunnel_role when the visor reports it
#   <set>.recovery.tsv    per-row loss-recovery counters, this end
#   <set>.exit-recovery.tsv  the same from the EXIT after every row (EXIT_SNAP)
#   <set>.mux_events.json the router's mux events for the set
#   <set>.chaos.tsv       what was cut, when, and what the row did afterwards
#   <set>.assert.tsv      the set's pass/fail table (see below)
#   <set>.cut.log         raw output of the tp rm / tp add calls
#
# Asserts (<set>.assert.tsv: assert, value, want, verdict):
#   hashes_after_cut      rows 11-15 hash-verified, want trials/trials
#   survivors_kept        every pre-cut route group except the cut one still
#                         held after the row — the no-rebuild test
#   rg_ports_lost         ports gone after the row, want at most the cut group
#   rg_ports_gained       ports the pool refilled with — INFO, not a failure
#   promote_event         >=1 tunnel_promoted (or leg_promoted on today's
#                         develop; the value names which kind was found)
#   ttfb_after_cut_s      first byte after the cut, want < CHAOS_TTFB_MAX_S
#   local_reorder_wedge   reorder_wedge events + wedge counter, this end, want 0
#   exit_reorder_wedge    the exit's wedge counter, want 0
#   pool_size / pool_settled / cut_target  INFO rows that date the run
#
# Functions are copied from run-mux.sh and run-degrade.sh rather than sourced:
# they are scripts with top-level work, not libraries, and factoring a bench
# library out of them would change files a measurement run is reading right now.
# SETTINGS="key=value ..." applies live `proxy settings` knobs to the app under
# test once per set — after it is up and warm, before the first row, waited for
# and recorded in the set header and <set>.settings.json. ROUTE_SETTINGS="--flag
# value ..." does the same for the visor-wide `route settings` and is restored
# at set end. The paired references never receive either. bench/lib-settings.sh;
# bench/run-sweep.sh drives one knob across a list of values.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
order=${6:-$(ls "$pins"/via-*.json | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
# shellcheck source=bench/lib-settings.sh
. "$here/lib-settings.sh"
sizes="10000000 50000000"
tunnels=${TUNNELS:-2}
# the chaos row is the first 50 MB download: trials 10 MB downs, trials 10 MB
# ups, then it. CHAOS_ROW overrides for a hand-aimed run.
chaos_row=${CHAOS_ROW:-$((trials * 2 + 1))}
chaos_size=50000000
chaos_after=${CHAOS_AFTER_S:-5}
chaos_pct=${CHAOS_PCT:-40}
chaos_ttfb_max=${CHAOS_TTFB_MAX_S:-2}
# pool fill: poll every POOL_POLL s until the group count has not moved for
# POOL_QUIET s, giving up after POOL_WAIT s either way.
pool_wait=${POOL_WAIT:-180}
pool_quiet=${POOL_QUIET:-20}
pool_poll=${POOL_POLL:-5}
name=${APP:-skysocks-client}
socks=127.0.0.1:${APP_PORT:-1080}
cur_app=$name

tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM

tp_counters() {
	$CLI cli visor state --select transports --json 2>/dev/null |
		jq -r --arg id "$1" '.transports[] | select(.id==$id) | "\(.log.sent) \(.log.recv)"'
}
tp_present() { # <tp id> -> 0 when the local visor holds it
	$CLI cli tp ls --json 2>/dev/null |
		jq -e --arg id "$1" 'any(.[]; .id==$id)' >/dev/null 2>&1
}
exit_commit() {
	timeout 60 $CLI cli visor state --via "dmsg://$exit_pk" --select summary --json 2>/dev/null |
		jq -r '.summary.overview.build_info.commit[0:9] // "unknown"'
}
stop_app() { $CLI cli proxy stop -n "$1" >/dev/null 2>&1; }
# EXIT_SNAP=1 (the default) adds ONE exit-side query of the same route group(s)
# after EVERY row, written to <set>.exit-recovery.tsv. The end-of-set snapshot
# alone cannot date a receiver-side stall to a row; this can.
EXIT_SNAP=${EXIT_SNAP:-1}
EXIT_SNAP_TIMEOUT=${EXIT_SNAP_TIMEOUT:-40}
# exit_rgs <timeout> <ports json array>: the exit's view of the groups whose
# src_port is in <ports> (our desc.dst_port is the exit's desc.src_port).
exit_rgs() {
	timeout "$1" $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null |
		jq -c --argjson p "$2" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, tunnel_role, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms}]}]' 2>/dev/null
}
# tp id -> remote public IP. `mux info` names a leg's transport but not where it
# lands; `tp ls --json` carries remote_ip per transport id.
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
# --- the pool, as the VISOR sees it -------------------------------------------
# `proxy mux info --json` round-trips through a CLI-local mirror struct that
# drops every field the mirror does not name — tunnel_role among them — so the
# pool's shape is read from `visor state --select mux_route_groups`, the same
# projection the exit side is read with.
#
# Groups are scoped to this exit by EITHER end of the descriptor. For a group
# THIS visor dialed to the exit the local projection reports desc.src_pk = the
# EXIT and desc.dst_pk = this visor, so the old `desc.dst_pk == $e` filter
# matched nothing: a settled pool of eight groups (two active, six standby)
# counted as ZERO, the set aborted INVALID as mux-standby-0, and the
# "roles: absent" it reported was only the empty list talking — tunnel_role was
# on every element. A visor that fails to answer still reads as an empty array,
# never as "the pool is empty".
state_rgs() {
	timeout "${1:-60}" $CLI cli visor state --select mux_route_groups --json 2>/dev/null |
		jq -c --arg e "$exit_pk" '[.mux_route_groups[]? | select(.desc.src_pk == $e or .desc.dst_pk == $e)]' 2>/dev/null
}
# role_map: {"<dst_port>": "<tunnel_role>"} from a state_rgs snapshot. Empty
# object when the field is absent, which is today's develop.
role_map() {
	echo "$1" | jq -c '[.[] | select((.tunnel_role // "") != "") | {(.desc.dst_port|tostring): .tunnel_role}] | add // {}' 2>/dev/null || echo '{}'
}
RG_WAIT=${RG_WAIT:-30}
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
# invalid_set <set> <reason>: no rows for this set, ever. summarize.sh and
# verdict.sh skip any set carrying a .INVALID marker and print the reason.
invalid_set() {
	printf '%s\n' "$2" > "$out/$1.INVALID"
	echo "$1: INVALID — $2"
}
# abort_set <set> <reason>: invalid_set, then leave the rig as a finished set
# leaves it (app stopped, transports whole) before anything else runs.
abort_set() {
	invalid_set "$1" "$2"
	stop_app_clean "$cur_app" || echo "$cur_app: dst_ports $rg_left left behind by an aborted set"
	rig_sweep
	return 0
}
warm() {
	bad=0
	for _ in 1 2 3 4; do
		code=$(curl -s -m 30 --socks5-hostname "$socks" -o /dev/null -w '%{http_code}' "$sink/?bytes=100000")
		[ "$code" = 200 ] || bad=$((bad + 1))
	done
	echo "$1 warm: $((4 - bad))/4 probes ok"
	[ $bad -eq 0 ]
}
# mux_events <set> <app> <ports json array>: the router's events for this set.
# The app tag is the primary filter, as in run-mux.sh; a promote reported by the
# APP (the pool's promoter reaches the ring through an ingress call, not through
# the router) may land untagged, so an event on one of this set's own route
# group ports counts too.
mux_events() {
	$CLI cli visor state --select diag --json 2>/dev/null |
		jq --arg app "$2" --argjson p "$3" --arg since "${setup_started:-$set_started}" \
			'[.diag.mux_events[]? | select(.at[0:19] >= $since) |
				select(.app == $app or ((.app // "") == "" and (.desc.src_port as $s | $p | index($s))) or (.desc.dst_port as $d | $p | index($d)))]' \
			> "$out/$1.mux_events.json" 2>/dev/null
	echo "$1: $(jq 'length' "$out/$1.mux_events.json" 2>/dev/null || echo 0) mux events: $(jq -r '[.[] | .event + "(" + (.reason // "") + ")"] | join(" ")' "$out/$1.mux_events.json" 2>/dev/null | cut -c1-300)"
}
now() { date +%s.%N; }
since_s() { awk -v a="$1" -v b="$(now)" 'BEGIN{printf "%.3f", b - a}'; }
ge() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a + 0 >= b + 0)}'; }
div() { awk -v a="$1" -v b="$2" 'BEGIN{if (b + 0 > 0) printf "%.0f", a / b; else print "-"}'; }
lt() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a + 0 < b + 0)}'; }
# pin_short <tp id>: the pin file whose FIRST HOP is that transport. Doubles as
# the "is this one of the pins" fence — a transport no pin names cannot be
# restored to the same id and is never cut.
pin_short() {
	for p in "$pins"/via-*.json; do
		[ "$(jq -r '.[0].forward[0].TpID' "$p" 2>/dev/null)" = "$1" ] && { basename "$p" .json | sed 's/^via-//'; return 0; }
	done
	return 1
}
pin_pk() { jq -r '.[0].forward[0].To' "$pins/via-$1.json" 2>/dev/null; }
# rig_sweep: every hop-1 pin transport back, as run-degrade.sh leaves the rig.
rig_sweep() {
	echo "--- rig sweep"
	for p in "$pins"/via-*.json; do
		id=$(jq -r '.[0].forward[0].TpID' "$p"); pk=$(jq -r '.[0].forward[0].To' "$p")
		tp_present "$id" && continue
		echo "re-adding hop-1 stcpr to $pk ($id)"
		timeout 120 $CLI cli tp add -t stcpr "$pk" 2>&1 | grep -v DEBUG | grep -i error
		sleep 1
		tp_present "$id" || echo "WARNING: $id still missing — run rig-restore.sh"
	done
	echo "hop-1 stcpr present: $(for p in "$pins"/via-*.json; do tp_present "$(jq -r '.[0].forward[0].TpID' "$p")" && echo x; done | wc -l)/$(ls "$pins"/via-*.json | wc -l)"
}

# --- pool fill ----------------------------------------------------------------
# wait_pool: poll the visor's own view of this exit's route groups until the
# count has not moved for $pool_quiet seconds. Leaves the last snapshot in
# $pool_state, the settled count in $pool_size and why it stopped in
# $pool_reason. A pool that is still growing when $pool_wait runs out is
# reported as such — the count is still recorded, the set still runs, and the
# header says the fill had not gone quiet.
wait_pool() {
	_w=0; _last=-1; _stable=0; pool_state='[]'; pool_size=0; pool_reason=timeout
	while [ "$_w" -lt "$pool_wait" ]; do
		pool_state=$(state_rgs 60)
		[ -n "$pool_state" ] || pool_state='[]'
		pool_size=$(echo "$pool_state" | jq 'length' 2>/dev/null || echo 0)
		if [ "$pool_size" -eq "$_last" ]; then
			_stable=$((_stable + pool_poll))
		else
			_stable=0; _last=$pool_size
			echo "pool: $pool_size route group(s) to the exit after ${_w}s"
		fi
		if [ "$_stable" -ge "$pool_quiet" ] && [ "$pool_size" -ge "$((tunnels + 1))" ]; then
			pool_reason="quiet ${pool_quiet}s"
			return 0
		fi
		sleep "$pool_poll"; _w=$((_w + pool_poll))
	done
	[ "$_stable" -ge "$pool_quiet" ] && pool_reason="quiet ${pool_quiet}s, below tunnels+1"
	return 1
}

# --- chaos --------------------------------------------------------------------
# fenced_candidates <mux info json>: "dst_port tp_id remote_pk pin_short" for
# every route group whose FIRST HOP passes all three fences. Printed in group
# order; the caller decides which of them is the active tunnel.
fenced_candidates() {
	_n=$(echo "$1" | jq 'length' 2>/dev/null || echo 0)
	_i=0
	while [ "$_i" -lt "$_n" ]; do
		_p=$(echo "$1" | jq -r --argjson i "$_i" '.[$i].desc.dst_port // empty')
		_t=$(echo "$1" | jq -r --argjson i "$_i" '.[$i].legs[0].transport_id // empty')
		_k=$(echo "$1" | jq -r --argjson i "$_i" '.[$i].legs[0].remote_pk // empty')
		_shared=$(echo "$1" | jq -r --argjson i "$_i" '[to_entries[] | select(.key != $i) | .value.legs[].transport_id] | join(" ")')
		_i=$((_i + 1))
		[ -n "$_t" ] && [ -n "$_p" ] || continue
		[ "$_k" != "$exit_pk" ] || continue                     # never the transport to the exit
		echo " $_shared " | grep -q " $_t " && continue          # never one another group also holds
		_s=$(pin_short "$_t" || true)
		[ -n "${_s:-}" ] || continue                             # never one no pin can restore
		echo "$_p $_t $(pin_pk "$_s") $_s"
	done
}
# active_ports <state json>: the dst_ports the visor calls active tunnels.
# Empty when the visor does not report tunnel_role, which is develop today.
active_ports() {
	echo "$1" | jq -r '.[] | select((.tunnel_role // "") == "active") | .desc.dst_port' 2>/dev/null
}
# carrier_rank: the set's first-hop transports ordered by the DOWNLOAD bytes
# they moved over the rows run so far, busiest first — the fallback answer to
# "which tunnel is carrying the traffic" when no role is reported.
carrier_rank() {
	awk -F'\t' '$1 ~ /^[0-9]+$/ && $2 != "legs" && $4 ~ /^[0-9]+$/ {s[$2] += $4}
		END {for (k in s) printf "%d %s\n", s[k], k}' "$c" | sort -rn | awk '{print $2}'
}
# choose_chaos: sets cut_rg, cut_tp, cut_pk, cut_short, cut_by. Returns 1 when
# nothing survives the fences.
choose_chaos() {
	_info=$(mux_info "$name")
	_state=$(state_rgs 60); [ -n "$_state" ] || _state='[]'
	fenced_candidates "$_info" > "$tmp/cand"
	[ -s "$tmp/cand" ] || { echo "no route group passes the cut fences"; return 1; }
	cut_rg=""; cut_tp=""; cut_pk=""; cut_short=""; cut_by=""; _line=""
	_act=$(active_ports "$_state" | tr '\n' ' ')
	if [ -n "$(echo "$_act" | tr -d ' ')" ]; then
		for _p in $_act; do
			_line=$(awk -v p="$_p" '$1 == p {print; exit}' "$tmp/cand")
			[ -n "$_line" ] || continue
			cut_by="tunnel_role=active"
			break
		done
	fi
	if [ -z "${_line:-}" ]; then
		# no role field (today's develop), or no active group passed the fences:
		# take the fenced group whose first hop actually carried the download.
		for _t in $(carrier_rank); do
			_line=$(awk -v t="$_t" '$2 == t {print; exit}' "$tmp/cand")
			[ -n "$_line" ] || continue
			cut_by="carrier-growth"
			break
		done
	fi
	[ -n "${_line:-}" ] || { _line=$(head -1 "$tmp/cand"); cut_by="first-fenced"; }
	cut_rg=$(echo "$_line" | awk '{print $1}')
	cut_tp=$(echo "$_line" | awk '{print $2}')
	cut_pk=$(echo "$_line" | awk '{print $3}')
	cut_short=$(echo "$_line" | awk '{print $4}')
	echo "$set_name: cut target = tp $cut_tp on rg $cut_rg (remote $cut_pk, pin via-$cut_short, by $cut_by)"
	return 0
}
# restore_tp: put the cut transport back, as run-degrade.sh does — a re-dialled
# stcpr is not up the instant `tp add` returns, so retry until `tp ls` shows the
# same id rather than sampling once. Echoes 1 when it is back.
restore_tp() {
	_ra=1
	while [ "$_ra" -le 4 ]; do
		tp_present "$cut_tp" && { echo 1; return; }
		timeout 120 $CLI cli tp add -t stcpr "$cut_pk" >> "$out/$set_name.cut.log" 2>&1
		sleep 5
		_ra=$((_ra + 1))
	done
	if tp_present "$cut_tp"; then echo 1; else echo 0; fi
}
# chaos_row <label>: the 50 MB download that gets cut. Writes bench.sh's row to
# $f and the cut record to $chaosf, and leaves the measurement in $ttfb,
# $cut_at, $bytes_at_cut, $ports_after.
chaos_row() {
	label=$1 # `set --` below reassigns the positional params, as run-degrade.sh does
	w="$tmp/row"; rm -rf "$w"; mkdir -p "$w"
	ports_before=$(mux_info "$name" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
	start=$(now)
	curl -s --socks5-hostname "$socks" -m 900 -D "$w/h" -o "$w/b" \
		-w '%{http_code} %{size_download} %{time_total} %{speed_download}' "$sink/?bytes=$chaos_size" > "$w/w" 2>/dev/null &
	cpid=$!
	cut_done=0; cut_at=0; bytes_at_cut=0; ttfb=-; cut_ok=0; cut_ts=-
	while kill -0 "$cpid" 2>/dev/null; do
		e=$(since_s "$start")
		p=0; [ -f "$w/b" ] && p=$(wc -c < "$w/b" | tr -d ' ')
		case $p in '' | *[!0-9]*) p=0 ;; esac
		if [ "$cut_done" -eq 0 ]; then
			if [ "$p" -ge "$((chaos_size * chaos_pct / 100))" ] || ge "$e" "$chaos_after"; then
				bytes_at_cut=$p; cut_at=$e; cut_ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
				timeout 60 $CLI cli tp rm "$cut_tp" >> "$out/$set_name.cut.log" 2>&1 && cut_ok=1 || cut_ok=0
				cut_done=1
				echo "$set_name $label: cut tp $cut_tp (rg $cut_rg, remote $cut_pk) at ${cut_at}s after $bytes_at_cut bytes (ok=$cut_ok)"
			fi
		elif [ "$ttfb" = - ] && [ "$p" -gt "$bytes_at_cut" ]; then
			ttfb=$(awk -v a="$e" -v b="$cut_at" 'BEGIN{printf "%.3f", a - b}')
		fi
		sleep 0.25
	done
	wait "$cpid" 2>/dev/null
	# shellcheck disable=SC2046 # the four -w fields are split on purpose, as bench.sh does
	set -- $(cat "$w/w" 2>/dev/null)
	http=${1:-000}; got=${2:-0}; secs=${3:-0}; speed=${4:-0}
	want=$(tr -d '\r' < "$w/h" 2>/dev/null | awk 'tolower($1)=="x-sha256:"{print $2}')
	have=$(sha256sum "$w/b" 2>/dev/null | cut -d' ' -f1)
	ok=0; [ -n "$want" ] && [ "$want" = "$have" ] && ok=1
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$label" down "$chaos_size" "$speed" "$http" "$got" "$secs" "$ok" >> "$f"
	# the no-rebuild reading has to be taken before the restore lets the pool
	# refill: a port that appears later is a refill, not a survivor.
	ports_after=$(mux_info "$name" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
	rem=$((got - bytes_at_cut)); [ "$rem" -lt 0 ] && rem=0
	gp_before=$(div "$bytes_at_cut" "$cut_at")
	gp_after=$(div "$rem" "$(awk -v a="$secs" -v b="$cut_at" 'BEGIN{printf "%.3f", a - b}')")
	restored=$(restore_tp)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$row" "$cut_tp" "$cut_pk" "$cut_ts" "$cut_rg" "$cut_by" "$cut_ok" "$cut_at" \
		"$bytes_at_cut" "$ttfb" "$gp_before" "$gp_after" "$restored" >> "$chaosf"
	echo "$set_name $label: http=$http got=$got hash_ok=$ok before=${gp_before}B/s after=${gp_after}B/s ttfb_after=${ttfb}s restored=$restored rg_ports $ports_before -> $ports_after"
}

# --- the set ------------------------------------------------------------------
run_set() { # <tp ids> <header>
	tps=$1; header=$2
	f="$out/$set_name.tsv"; c="$out/$set_name.carrier.tsv"; chaosf="$out/$set_name.chaos.tsv"
	set_started=$(date +%Y-%m-%dT%H:%M:%S) # visor-local time, the zone the event ring is stamped in
	echo "# $header" > "$f"
	printf '# row\ttp\tsent_delta\trecv_delta\n' > "$c"
	printf '# row\ttp_id\tfirst_hop_pk\tts\trg\tchosen_by\tcut_ok\tcut_at_s\tbytes_before_cut\tttfb_after_s\tgoodput_before_Bps\tgoodput_after_Bps\trestored\n' > "$chaosf"
	: > "$out/$set_name.cut.log"
	: > "$out/$set_name.recovery.tsv"
	[ "$EXIT_SNAP" = 1 ] && printf '# row\texit_mux_route_groups\n' > "$out/$set_name.exit-recovery.tsv"
	row=0
	for n in $sizes; do
		for dir in down up; do
			t=1
			while [ $t -le "$trials" ]; do
				row=$((row + 1))
				before=""
				for tp in $tps; do before="$before $tp:$(tp_counters "$tp" | tr ' ' ',')"; done
				# the chaos row is a 50 MB DOWNLOAD by construction; a hand-aimed
				# CHAOS_ROW pointing at any other cell would measure a size the
				# row layout does not claim, so it is refused, not adapted.
				if [ "$row" -eq "$chaos_row" ] && { [ "$n" != "$chaos_size" ] || [ "$dir" != down ]; }; then
					echo "$set_name row $row: CHAOS_ROW is a $n $dir cell, not a $chaos_size down — running it uncut"
					cut_tp=-
					"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
				elif [ "$row" -eq "$chaos_row" ]; then
					choose_chaos || { echo "$set_name row $row: no cut target — running the row uncut"; cut_tp=-; }
					if [ "$cut_tp" = - ]; then
						"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
					else
						chaos_row "$set_name-t$t"
					fi
				else
					"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
				fi
				for tp in $tps; do
					b=$(echo "$before" | tr ' ' '\n' | grep "^$tp:" | cut -d: -f2)
					a=$(tp_counters "$tp" | tr ' ' ',')
					[ -n "$a" ] && [ -n "$b" ] || { printf '%s\t%s\t?\t?\n' "$row" "$tp" >> "$c"; continue; }
					printf '%s\t%s\t%s\t%s\n' "$row" "$tp" "$(( ${a%,*} - ${b%,*} ))" "$(( ${a#*,} - ${b#*,} ))" >> "$c"
				done
				# legs held + loss-recovery counters after every row
				info=$(mux_info "$name")
				printf '%s\tlegs\t%s\t-\n' "$row" "$(echo "$info" | jq -r '[.[] | (.desc.dst_port|tostring) + ":" + ([.legs[].transport_id[0:8]] | join(","))] | join(" ")')" >> "$c"
				printf '%s\t%s\n' "$row" "$(echo "$info" | jq -c '[.[] | {rg: .desc.dst_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
				p=$(echo "$info" | jq -c '[.[].desc.dst_port]')
				xr=""
				if [ "$EXIT_SNAP" = 1 ]; then
					xr=$(exit_rgs "$EXIT_SNAP_TIMEOUT" "$p")
					[ -n "$xr" ] || { xr='{}'; echo "$set_name row $row: exit snapshot failed/timed out after ${EXIT_SNAP_TIMEOUT}s — empty object recorded"; }
					printf '%s\t%s\n' "$row" "$xr" >> "$out/$set_name.exit-recovery.tsv"
				fi
				# a failed row loses the exit's view when the session re-dials: snapshot it NOW
				if [ "$(tail -1 "$f" | cut -f8)" != 1 ]; then
					[ -n "$xr" ] && [ "$xr" != '{}' ] || xr=$(exit_rgs 60 "$p")
					printf '# exit-on-fail %s\t%s\n' "$row" "$xr" >> "$out/$set_name.recovery.tsv"
				fi
				t=$((t + 1))
			done
		done
	done
	echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
	ports=$(mux_info "$name" | jq -c '[.[].desc.dst_port]')
	mux_events "$set_name" "$name" "$ports"
	# the EXIT's view of the same group(s) at the end of the set
	x=""; for _ in 1 2 3; do
		x=$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, tunnel_role, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms}]}]')
		[ -n "$x" ] && [ "$x" != "[]" ] && break
		sleep 3
	done
	printf '# exit\t%s\n' "$x" >> "$out/$set_name.recovery.tsv"
}

# --- asserts ------------------------------------------------------------------
assert_row() { printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >> "$af"; }
verdict() { if [ "$1" = 1 ]; then echo PASS; else echo FAIL; fi; }
# ports_minus <"a,b,c"> <"a,c">: the members of the first list absent from the second.
ports_minus() {
	echo "$1" | tr ',' '\n' | grep -v '^$' | while read -r _p; do
		echo ",$2," | grep -q ",$_p," || echo "$_p"
	done | tr '\n' ' ' | sed 's/ *$//'
}
write_asserts() {
	af="$out/$set_name.assert.tsv"
	printf '# assert\tvalue\twant\tverdict\n' > "$af"
	assert_row pool_size "$pool_size" "> $tunnels" "$(verdict "$([ "$pool_size" -gt "$tunnels" ] && echo 1 || echo 0)")"
	assert_row pool_settled "$pool_reason" "quiet" INFO
	# (a) rows chaos_row..chaos_row+trials-1 — the 50 MB downloads — all verified
	_hok=$(grep -v '^#' "$f" | awk -F'\t' -v a="$chaos_row" -v b="$((chaos_row + trials - 1))" 'NR >= a && NR <= b && $8 == 1' | wc -l)
	assert_row hashes_after_cut "$_hok/$trials" "$trials/$trials" "$(verdict "$([ "$_hok" -eq "$trials" ] && echo 1 || echo 0)")"
	if [ "${cut_tp:--}" = - ] || [ -z "${cut_tp:-}" ]; then
		assert_row cut_target "none — no group passed the fences" "one active tunnel" FAIL
		return 0
	fi
	assert_row cut_target "tp $cut_tp rg $cut_rg remote $cut_pk (by $cut_by)" "an active tunnel's first hop" INFO
	# (b) the survivors kept their ports: nothing but the cut group may vanish,
	# and a port that appears is the pool refilling, recorded on its own row.
	_lost=$(ports_minus "$ports_before" "$ports_after")
	_gained=$(ports_minus "$ports_after" "$ports_before")
	_kept=1
	for _p in $_lost; do [ "$_p" = "$cut_rg" ] || _kept=0; done
	_want=$(echo "$ports_before" | tr ',' '\n' | grep -vc '^$')
	_have=$(echo "$ports_before" | tr ',' '\n' | grep -v '^$' | while read -r _p; do
		[ "$_p" = "$cut_rg" ] && continue
		echo ",$ports_after," | grep -q ",$_p," && echo x
	done | wc -l)
	assert_row survivors_kept "$_have/$((_want - 1)) ($ports_before -> $ports_after)" "all but rg $cut_rg" \
		"$(verdict "$([ "$_have" -eq "$((_want - 1))" ] && echo 1 || echo 0)")"
	assert_row rg_ports_lost "${_lost:-none}" "at most rg $cut_rg" "$(verdict "$_kept")"
	assert_row rg_ports_gained "${_gained:-none}" "pool refill, not a rebuild" INFO
	# (c) a promote. tunnel_promoted is the pool's own event; leg_promoted is what
	# today's develop emits from the packet-level adaptive engine. Either counts,
	# and the value names which was found so the two are never conflated.
	_tp=$(jq '[.[] | select(.event == "tunnel_promoted")] | length' "$out/$set_name.mux_events.json" 2>/dev/null || echo 0)
	_lp=$(jq '[.[] | select(.event == "leg_promoted")] | length' "$out/$set_name.mux_events.json" 2>/dev/null || echo 0)
	_kind=none
	[ "$_lp" -gt 0 ] && _kind="leg_promoted x$_lp"
	[ "$_tp" -gt 0 ] && _kind="tunnel_promoted x$_tp"
	[ "$_tp" -gt 0 ] && [ "$_lp" -gt 0 ] && _kind="tunnel_promoted x$_tp + leg_promoted x$_lp"
	assert_row promote_event "$_kind" ">=1 tunnel_promoted (or leg_promoted)" \
		"$(verdict "$([ "$((_tp + _lp))" -gt 0 ] && echo 1 || echo 0)")"
	# (d) the first byte after the cut
	if [ "${ttfb:--}" = - ]; then
		assert_row ttfb_after_cut_s "no byte arrived after the cut" "< $chaos_ttfb_max" FAIL
	else
		assert_row ttfb_after_cut_s "$ttfb" "< $chaos_ttfb_max" "$(verdict "$(lt "$ttfb" "$chaos_ttfb_max" && echo 1 || echo 0)")"
	fi
	# (e) no reorder wedge at either end. This end has both the event and the
	# counter; the exit has the counter, from the per-row snapshots.
	_ev=$(jq '[.[] | select(.event == "reorder_wedge")] | length' "$out/$set_name.mux_events.json" 2>/dev/null || echo 0)
	_lw=$(awk -F'\t' '$1 ~ /^[0-9]+$/ {print $2}' "$out/$set_name.recovery.tsv" 2>/dev/null |
		jq -s '[.[][]? | .recovery.wedges // 0] | max // 0' 2>/dev/null || echo 0)
	assert_row local_reorder_wedge "$_ev events, wedges counter $_lw" 0 \
		"$(verdict "$([ "$((_ev + _lw))" -eq 0 ] && echo 1 || echo 0)")"
	_xw=0
	if [ -f "$out/$set_name.exit-recovery.tsv" ]; then
		_xw=$(awk -F'\t' '$1 ~ /^[0-9]+$/ {print $2}' "$out/$set_name.exit-recovery.tsv" 2>/dev/null |
			jq -s '[.[] | select(type == "array") | .[]? | .recovery.wedges // 0] | max // 0' 2>/dev/null || echo 0)
	fi
	assert_row exit_reorder_wedge "$_xw" 0 "$(verdict "$([ "$_xw" -eq 0 ] && echo 1 || echo 0)")"
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec app=$name addr=$socks tunnels=$tunnels chaos_row=$chaos_row order=$order"
[ "$(echo "$order" | tr ' ' '\n' | grep -vc '^$')" -ge 1 ] || { echo "no pins in $pins — the cut could not be restored, refusing to run"; exit 2; }

setup_started=$(date +%Y-%m-%dT%H:%M:%S)
set_name=mux-standby-pending
# a group left over from something else would be dialed as part of this pool:
# refuse to measure until the app owns nothing.
stop_app_clean "$name" || { invalid_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"; exit 1; }
timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --tunnels "$tunnels" \
	${STANDBY_POOL:+--standby-pool $STANDBY_POOL} \
	${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} ${RANGE_CONCURRENCY:+--range-concurrency $RANGE_CONCURRENCY} 2>&1 |
	grep -iv debug | grep -i "tunnel\|standby\|pool\|running\|error\|fatal" | head -5

wait_pool || echo "pool: did NOT settle inside ${pool_wait}s (last count $pool_size, $pool_reason)"
set_name="mux-standby-$pool_size"
echo "$name: pool settled at $pool_size route group(s) ($pool_reason)"

# the pool as it stands, in run-mux.sh's .legs.json shape: mux info (groups,
# legs, hops with FULL pks) + remote_ip per leg + tunnel_role per group when the
# visor reports one.
roles=$(role_map "$pool_state"); [ -n "$roles" ] || roles='{}'
mux_info "$name" | jq --argjson r "$roles" \
	'map(. + {tunnel_role: ($r[(.desc.dst_port|tostring)] // null)})' > "$out/$set_name.legs.json" 2>/dev/null ||
	mux_info "$name" > "$out/$set_name.legs.json"
groups=$(jq 'length' "$out/$set_name.legs.json" 2>/dev/null || echo 0)
tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
desc=$(jq -r '[.[] | "rg\(.desc.dst_port)" + (if .tunnel_role then "/" + .tunnel_role else "" end) + "=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
roles_seen=$(jq -r '[.[] | .tunnel_role // empty] | join(",") | if . == "" then "absent" else . end' "$out/$set_name.legs.json" 2>/dev/null)
echo "$name: $groups route group(s); roles: $roles_seen; first-hop tps: $tps"
# a pool that never grew past the active set measures the old behaviour under a
# new name: say so and keep the rows out of the campaign.
[ "$pool_size" -gt "$tunnels" ] || { abort_set "$set_name" "pool did not grow past the active set: $pool_size group(s) with --tunnels $tunnels ($pool_reason; groups=$desc)"; exit 1; }
# and a pool with nothing cuttable cannot answer the question the set exists for
c="$out/$set_name.carrier.tsv"
fenced_candidates "$(mux_info "$name")" > "$tmp/preflight"
[ -s "$tmp/preflight" ] || { abort_set "$set_name" "no route group's first hop passes the cut fences (a pin, not the exit, not shared) — nothing restorable to cut (groups=$desc)"; exit 1; }
echo "$name: $(grep -c . "$tmp/preflight") group(s) cuttable and restorable"

warm "$name" || echo "$name: probes failing — running the set anyway"
# live knobs, once per set: the visor's app store is cleared when the app stops,
# so SETTINGS can only be applied here — after the dial, after the pool settled
# and the warm probes, and before the first row (bench/lib-settings.sh).
settings_apply "$set_name" "$name"
ports_before=""; ports_after=""; ttfb=-; cut_tp=""; cut_rg=""; cut_pk=""; cut_by=""
run_set "$tps" \
	"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=$tunnels pool=$pool_size pool_settled=$pool_reason route_groups=$groups roles=$roles_seen groups=$desc chaos_row=$chaos_row chaos_after=${chaos_after}s sink=$sink${settings_note:+ $settings_note}"
settings_restore "$set_name" # the app knobs die with the app; the ROUTER knobs do not
write_asserts
echo "--- $set_name asserts"
cat "$out/$set_name.assert.tsv"
ports_end=$(mux_info "$name" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
printf '# rg dst_ports settled=%s after_cut=%s end=%s\n' "$ports_before" "$ports_after" "$ports_end" >> "$c"
stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set"
rig_sweep

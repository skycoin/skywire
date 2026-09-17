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
#   mux-tunnels-2-up2  (UP2=1) two CONCURRENT 50 MB uploads through the same
#                   two-tunnel session, both hash-verified. The goal's upload
#                   criterion: their summed MB/s must reach the sum of the two
#                   best single-route reference uploads.
# <set>.legs.json keeps the `mux info --json` snapshot taken before the set
# (exact groups, legs, transport types, remote pks, and the remote public IP
# each leg's transport lands on) and <set>.carrier.tsv the per-row byte
# deltas of every first-hop transport the set is supposed to ride.
# <set>.exit-recovery.tsv is the EXIT's view of the same route group(s) at set
# start, after the cut row and at set end (EXIT_SNAP=0 turns it off,
# EXIT_SNAP_TIMEOUT bounds each query).
# [pin order] is a space-separated list of pin shorts, best route first; legs-N
# takes the first N. Default: every <pins>/via-*.json in name order.
#
# The 2026-09-17 goal text adds these to every campaign, and they are the knobs
# below:
#
#   PAIRED / PAIRED_REF — "references measured contemporaneously … every verdict
#     is the paired ratio mux/best-reference". One reference row of the SAME
#     cell runs on ONE route in a second proxy instance immediately BEFORE each
#     mux row, into <set>.paired.tsv. See bench/lib-paired.sh. PAIRED=0 restores
#     exactly the old behaviour.
#   SIZES / CELL100 — "a 100 MB download cell for criterion 4". SIZES takes
#     megabytes ("10 50 100") or bytes; CELL100=1 appends the 100 MB cell to
#     whatever SIZES holds. The 100 MB cell is a DOWNLOAD cell (DIRS100="down up"
#     measures the upload too). With the default 5 download / 3 upload trials
#     the layout is 10 down 1-5, 10 up 6-8, 50 down 9-13, 50 up 14-16, 100 down
#     17-21; with TRIALS_UP=5 it is the old 1-5/6-10/11-15/16-20 and 21-25.
#   CUT_CELL / CUT_TRIAL / CUT_ROW — "every campaign includes a cut row".
#     CUT_AFTER_S (5) seconds into trial CUT_TRIAL (3) of cell CUT_CELL (50down)
#     one route group loses its first-hop transport, the set carries on, and
#     <set>.cut.tsv records what was cut and how long the transfer took to move
#     again. CUT_ROW=<n> names the row outright, CUT_ROW=0 turns it off. See
#     bench/lib-cut.sh.
#   EXIT_RES — "every deploy records exit RssAnon and CPU before and after each
#     set and fails the run if either grows across it". bench/exit-resources.sh
#     around every set, bench/exit-resources-check.sh after it; a FAIL keeps the
#     results and makes this script exit non-zero at the very end.
#   TRIALS_UP — upload cells take 3 trials, download cells the [trials]
#     argument (5). Uploads repeat themselves; downloads do not.
#
# DEFAULT SUITE: TUNNELS="2" LEGS="2", 5 download trials and 3 upload trials.
# The sets that never win — three tunnels, three legs — are on-demand:
# TUNNELS="2 3" LEGS="2 3 5" runs them.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
order=${6:-$(ls "$pins"/via-*.json | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
PAIRED_HERE=$here; CUT_HERE=$here # read by the libraries below
export PAIRED_HERE CUT_HERE
# shellcheck source=bench/lib-paired.sh
. "$here/lib-paired.sh"
# shellcheck source=bench/lib-cut.sh
. "$here/lib-cut.sh"
# norm_sizes: SIZES takes megabytes ("10 50 100") or bytes ("10000000 50000000");
# an entry below 1000 is megabytes. Anything non-numeric is dropped.
norm_sizes() {
	for _ns in $1; do
		case $_ns in *[!0-9]* | "") continue ;; esac
		[ "$_ns" -lt 1000 ] && _ns=$((_ns * 1000000))
		printf '%s ' "$_ns"
	done
}
sizes=$(norm_sizes "${SIZES:-10000000 50000000}")
if [ "${CELL100:-0}" = 1 ]; then
	case " $sizes " in *" 100000000 "*) ;; *) sizes="${sizes}100000000 " ;; esac
fi
# size_dirs <bytes>: the 100 MB cell of criterion 4 is a DOWNLOAD cell.
size_dirs() { if [ "$1" -ge 100000000 ]; then echo "${DIRS100:-down}"; else echo "${DIRS:-down up}"; fi; }
# trials_for <dir>: downloads take the [trials] argument, uploads TRIALS_UP.
# The upload direction repeats itself — direct 50 MB up measured 9.52, 9.60,
# 9.65 and 10.32 MB/s on four consecutive campaigns, and tunnels-2 50 MB up
# 9.07, 9.12, 9.07 — while a download cell swings by 2x inside an hour. Five
# download trials buy a real median; five upload trials buy the same number
# twice more.
trials_for() { if [ "$1" = up ]; then echo "${TRIALS_UP:-3}"; else echo "$trials"; fi; }
tunnel_counts=${TUNNELS-"2"}
leg_counts=${LEGS-"2"}
# The goal asks for the cut FIVE SECONDS into the row, so the cut row is timed,
# not progress-triggered: cut_pct 100 can never fire first (100 % of the
# transfer is the transfer) and cut_after is the 5 s.
# shellcheck disable=SC2034 # both are read by cut_transfer in lib-cut.sh
cut_after=${CUT_AFTER_S:-5}
# shellcheck disable=SC2034 # read by cut_transfer in lib-cut.sh
cut_pct=${CUT_PCT:-100}
# cut_row_number: which row the cut lands on. CUT_ROW=<n> names it outright and
# CUT_ROW=0 turns the cut off; otherwise it is trial CUT_TRIAL of cell CUT_CELL
# (the third 50 MB download), COUNTED IN THIS RUN'S LAYOUT — with the default
# 5 download / 3 upload trials that is row 11, with TRIALS_UP=5 it is row 13.
CUT_CELL=${CUT_CELL:-50down}
CUT_TRIAL=${CUT_TRIAL:-3}
cut_row_number() {
	_r=0
	for _n in $sizes; do
		for _d in $(size_dirs "$_n"); do
			_t=1
			while [ "$_t" -le "$(trials_for "$_d")" ]; do
				_r=$((_r + 1))
				[ "$((_n / 1000000))$_d" = "$CUT_CELL" ] && [ "$_t" = "$CUT_TRIAL" ] && { echo "$_r"; return 0; }
				_t=$((_t + 1))
			done
		done
	done
	echo 0
}
EXIT_RES=${EXIT_RES:-1}
exit_res_fail=0
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT INT TERM
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
# EXIT_SNAP=1 (the default) writes the EXIT's view of this set's route group(s)
# to <set>.exit-recovery.tsv THREE times: at set start, immediately after the
# cut row, and at set end — the row column holds `start`, `cut` or `end`
# instead of a row number. It used to be one query after EVERY row, which
# bought a per-row timeline at up to 40 s of dead time per row whenever dmsg
# was slow: 20 rows x 40 s is 13 minutes of a set spent waiting on a snapshot.
# The three moments that are actually read — the shape it started in, what the
# cut did to it, where it ended — cost three queries. A row that FAILS still
# snapshots the exit immediately, because a re-dial would erase the evidence.
# EXIT_SNAP=0 turns the whole file off.
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
# exit_snap_row <start|cut|end>: ONE exit-side snapshot of the set's current
# route group(s), tagged with the moment rather than a row number. Bounded and
# never retried — a slow exit must not stall a set.
exit_snap_row() {
	[ "$EXIT_SNAP" = 1 ] || return 0
	_esp=$(mux_info "$cur_app" | jq -c '[.[].desc.dst_port]' 2>/dev/null)
	[ -n "$_esp" ] || _esp='[]'
	_esx=$(exit_rgs "$EXIT_SNAP_TIMEOUT" "$_esp")
	[ -n "$_esx" ] || { _esx='{}'; echo "$set_name: $1 exit snapshot failed/timed out after ${EXIT_SNAP_TIMEOUT}s — empty object recorded"; }
	printf '%s\t%s\n' "$1" "$_esx" >> "$out/$set_name.exit-recovery.tsv"
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
# --- standby pool (#4986) ------------------------------------------------------
# `proxy start --standby-pool` holds MORE route groups than --tunnels N: the
# active set plus standby tunnels that are dialed, kept alive and measured on the
# same 5 s ping but carry no streams, so a tunnel that dies is replaced by a
# route that already exists. A shape check that counts route groups therefore
# has to count the ACTIVE ones — a live smoke run of `--tunnels 2` came up with
# three groups (two active, one standby) and every tunnels set aborted INVALID.
#
# `proxy mux info --json` marks each group with tunnel_role, and the field is
# omitempty: a binary that predates the pool emits none. When NO group carries
# it, every group counts as active — exactly the old behaviour.
_role_filter='[.[]?] as $rgs | if ($rgs | map(select(.tunnel_role != null)) | length) == 0 then $rgs else ($rgs | map(select(.tunnel_role == "active"))) end' # $rgs, never $g: wait_width passes jq an --argjson g
# rg_roles <legs.json> -> "<all> <active> <standby>"
rg_roles() {
	jq -r '[.[]?] as $rgs
		| ($rgs | map(select(.tunnel_role != null)) | length) as $tagged
		| ($rgs | length) as $all
		| ($rgs | map(select(.tunnel_role == "active")) | length) as $act
		| ($rgs | map(select(.tunnel_role == "standby")) | length) as $sb
		| if $tagged == 0 then "\($all) \($all) 0" else "\($all) \($act) \($sb)" end' "$1" 2>/dev/null || echo "0 0 0"
}
# active_json <legs.json> -> the same array filtered to the ACTIVE groups
active_json() { jq -c "$_role_filter" "$1" 2>/dev/null || echo '[]'; }
# active_ports <legs.json> -> the ACTIVE groups' dst_ports, one per line
active_ports() { jq -r "$_role_filter | .[].desc.dst_port" "$1" 2>/dev/null; }
# wait_pool <app>: the pool fills ONE dial at a time after the active set is up,
# so a snapshot 5 s after `proxy start` sees a pool that is still growing. Wait
# until the group count has been unchanged for POOL_STABLE_S, bounded by
# POOL_WAIT_S, then say how big the pool settled at.
POOL_STABLE_S=${POOL_STABLE_S:-10}
POOL_WAIT_S=${POOL_WAIT_S:-60}
wait_pool() {
	_pw=0; _plast=-1; _pstable=0
	while [ "$_pw" -lt "$POOL_WAIT_S" ]; do
		_pn=$($CLI cli proxy mux info -n "$1" --json 2>/dev/null | jq 'length' 2>/dev/null)
		case ${_pn:-} in '' | *[!0-9]*) _pn=0 ;; esac
		if [ "$_pn" = "$_plast" ]; then
			_pstable=$((_pstable + 2))
			if [ "$_pstable" -ge "$POOL_STABLE_S" ]; then
				echo "$1: route group count stable at $_pn for ${_pstable}s"
				return 0
			fi
		else
			_pstable=0; _plast=$_pn
		fi
		sleep 2; _pw=$((_pw + 2))
	done
	echo "$1: route group count still moving after ${POOL_WAIT_S}s (now $_plast) — snapshotting anyway"
	return 0
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
# cut_transfer_row <label> <dir> <bytes>: the campaign's ONE cut row. The
# transfer is a normal hash-verified row of its cell — it lands in <set>.tsv
# like every other row, so the cell still has `trials` rows and its median is
# still the median of five — but CUT_AFTER_S seconds in, one route group loses
# its first-hop transport and the rest of the transfer has to survive it.
# The rig is put back before the next row.
# shellcheck disable=SC2154 # ct_* and cut_* are cut_transfer/choose_cut results (lib-cut.sh)
cut_transfer_row() {
	rg_before=$(mux_info "$cur_app" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
	cut_transfer "$1" "$2" "$3"
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$ct_speed" "$ct_http" "$ct_got" "$ct_secs" "$ct_ok" >> "$f"
	restored=$(restore_cut)
	rg_after=$(mux_info "$cur_app" | jq -r '[.[].desc.dst_port] | join(",")' 2>/dev/null)
	printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' \
		"$row" "$cut_tp" "$cut_pk" "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$ct_ttfb" \
		"$rg_before" "$rg_after" "$ct_cut_ok" "$restored" >> "$out/$set_name.cut.tsv"
	echo "$set_name row $row: CUT $cut_kind $cut_tp (first hop $cut_pk) at ${ct_cut_at}s, ttfb_after_cut=${ct_ttfb}s, rg $rg_before -> $rg_after, restored=$restored, hash_ok=$ct_ok"
	# a group the cut cost the session has to be back before the next row, or
	# every later row measures an already-degraded session.
	if [ "$cut_kind" = tp ] && [ "$restored" = 1 ]; then sleep 5; fi
	exit_snap_row cut
}

run_set() { # <set> <socks> <tp ids> <header>
	set_name=$1; socks=$2; tps=$3; header=$4; set_started=$(date +%Y-%m-%dT%H:%M:%S) # visor-local time, the zone the event ring is stamped in
	f="$out/$set_name.tsv"; c="$out/$set_name.carrier.tsv"
	echo "# $header" > "$f"
	echo "# row	tp	sent_delta	recv_delta" > "$c"
	[ "$EXIT_SNAP" = 1 ] && printf '# row\texit_mux_route_groups\n' > "$out/$set_name.exit-recovery.tsv"
	set_paired=0
	if [ "$PAIRED" = 1 ]; then
		if paired_start 1 "$paired_ref" "$exit_pk" "$pins" "$sink"; then
			set_paired=1
			paired_header "$paired_ref" "${paired_route:-?}"
		else
			echo "$set_name: no contemporaneous reference — the set is measured UNPAIRED (verdict.sh falls back to the bar)"
		fi
	fi
	cut_at_row=0
	if [ "${CUT_ROW:-}" != 0 ]; then
		: > "$out/$set_name.cut.log"
		if choose_cut auto; then
			cut_at_row=${CUT_ROW:-$(cut_row_number)}
			printf '# row\ttp\tfirst_hop_pk\tts\tttfb_after_cut_s\trg_ports_before\trg_ports_after\tcut_ok\trestored\n' > "$out/$set_name.cut.tsv"
			echo "$set_name: the cut row is row $cut_at_row (trial $CUT_TRIAL of the $CUT_CELL cell), ${cut_after}s in"
		else
			# choose_cut refused: the only targets left were the paired
			# reference's route (CUT_REF_FENCE), or nothing could be put back.
			# The set runs with NO cut row and says so where the cut row would
			# have been recorded — cutting the reference would cost every later
			# paired row of the set.
			printf '# row\ttp\tfirst_hop_pk\tts\tttfb_after_cut_s\trg_ports_before\trg_ports_after\tcut_ok\trestored\n' > "$out/$set_name.cut.tsv"
			printf '# cut=skipped:%s\n' "${cut_skip_reason:-no cut target}" >> "$out/$set_name.cut.tsv"
		fi
	fi
	exit_snap_row start
	row=0
	for n in $sizes; do
		for dir in $(size_dirs "$n"); do
			t=1
			while [ $t -le "$(trials_for "$dir")" ]; do
				row=$((row + 1))
				# The paired reference row of the SAME cell, on one single route,
				# immediately BEFORE the mux row and never at the same time as it.
				pref=-; pok=0; plegs=-
				if [ "$set_paired" = 1 ]; then
					_pv=$(paired_row 1 "$row" "$n" "$dir")
					pref=${_pv%% *}; _pv=${_pv#* }; pok=${_pv%% *}; plegs=${_pv##* }
				fi
				before=""
				for tp in $tps; do before="$before $tp:$(tp_counters "$tp" | tr ' ' ',')"; done
				if [ "$row" = "$cut_at_row" ]; then
					cut_transfer_row "$set_name-t$t" "$dir" "$n"
				else
					"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
				fi
				if [ "$set_paired" = 1 ]; then
					_mlast=$(tail -1 "$f")
					paired_emit "$row" "$(paired_cell "$n" "$dir")" "$pref" "$pok" "$plegs" \
						"$(echo "$_mlast" | awk -F'\t' '{printf "%.2f", $4/1e6}')" "$(echo "$_mlast" | cut -f8)"
				fi
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
				# A FAILED row loses the exit's view when the session re-dials, so
				# that one case is still snapshotted immediately. Healthy rows are
				# not: see exit_snap_row.
				if [ "$(tail -1 "$f" | cut -f8)" != 1 ]; then
					echo "# exit-on-fail $row	$(exit_rgs 60 "$p")" >> "$out/$set_name.recovery.tsv"
				fi
				t=$((t + 1))
			done
		done
	done
	[ "$set_paired" = 1 ] && paired_stop 1
	echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
	if [ -f "$out/$set_name.paired.tsv" ]; then
		echo "$set_name: paired ratios vs $paired_ref: $(grep -v '^#' "$out/$set_name.paired.tsv" | awk -F'\t' '$5!="-" {v[$2]=v[$2]" "$5} END{for (k in v) printf "%s:%s ", k, v[k]}')"
	fi
	if [ "$cut_at_row" != 0 ] && [ -f "$out/$set_name.cut.tsv" ]; then
		echo "$set_name: cut row $cut_at_row ttfb_after_cut=$(grep -v '^#' "$out/$set_name.cut.tsv" | awk -F'\t' '{print $5}' | tail -1)s"
	fi
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
	# the same reading is the `end` row of the per-set exit snapshots: one query,
	# two readers.
	[ "$EXIT_SNAP" = 1 ] && printf 'end\t%s\n' "${x:-{\}}" >> "$out/$set_name.exit-recovery.tsv"
	return 0
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec order=$order sizes=$sizes tunnels=$tunnel_counts legs=$leg_counts"
paired_ref=""
if [ "$PAIRED" = 1 ]; then
	paired_ref=$(paired_resolve "$out" 1)
	echo "paired references: PAIRED_REF=$PAIRED_REF resolved to '$paired_ref'; ranking was:"
	paired_rank "$out" | sed 's/^/  /'
fi
# res_set <label>: one exit RssAnon/CPU reading, named for the set it brackets.
res_set() { [ "$EXIT_RES" = 1 ] && "$here/exit-resources.sh" "$out" "$1" "$exit_pk"; return 0; }
# res_check <set>: score the pair. The FAILURE is remembered, not acted on —
# the results are kept and this script exits non-zero only at the very end.
res_check() {
	[ "$EXIT_RES" = 1 ] || return 0
	"$here/exit-resources-check.sh" "$out" "$1" || exit_res_fail=1
	return 0
}

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
	wait_pool "$name" # the standby pool is still filling 5 s after the dial
	mux_info "$name" > "$out/$set_name.legs.json"
	roles=$(rg_roles "$out/$set_name.legs.json")
	groups=${roles%% *}; roles_rest=${roles#* }; active=${roles_rest%% *}; standby=${roles_rest##* }
	# the carriers are named from EVERY group, standby included: a standby tunnel
	# moves only its keepalive, and a leg promoted mid-set has to be in the list.
	tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
	desc=$(jq -r '[.[] | "rg\(.desc.dst_port)\(if .tunnel_role then "/" + .tunnel_role else "" end)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
	echo "$name: $groups route group(s) — $active active, $standby standby; first-hop tps: $tps"
	# the target IS N ACTIVE tunnels; a set that came up in another shape measures
	# something else and must not be recorded as this set (campaign16). Standby
	# tunnels are not part of the shape: the pool holds as many as the router has
	# disjoint first hops for.
	[ "$active" -eq "$N" ] || { abort_set "$set_name" "shape differs from target: $active active route group(s) of $groups ($standby standby), asked for $N (groups=$desc)"; port=$((port + 1)); continue; }
	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	res_set "$set_name-pre"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=$N route_groups=$groups active=$active standby=$standby groups=$desc sink=$sink"
	res_set "$set_name-post"; res_check "$set_name"
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
	roles=$(rg_roles "$out/$set_name.legs.json")
	groups=${roles%% *}; roles_rest=${roles#* }; active=${roles_rest%% *}; standby=${roles_rest##* }
	# a legs set is ONE active group; the standby pool may hold tunnels beside it,
	# so every field below is read from the ACTIVE group, not from index 0.
	aj=$(active_json "$out/$set_name.legs.json")
	port_before=$(echo "$aj" | jq -r '.[0].desc.dst_port')
	nlegs=$(echo "$aj" | jq '.[0].legs | length')
	tps=$(echo "$aj" | jq -r '.[0].legs[].transport_id' | tr '\n' ' ')
	want=$(jq -r '.[].forward[0].TpID' "$legs_file" | tr '\n' ' ')
	desc=$(echo "$aj" | jq -r '[.[0].legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")')
	echo "$name: $groups rg ($active active, $standby standby), src_port=$port_before, legs=$nlegs/$N present: $desc"
	missing=0; for w in $want; do echo "$tps" | grep -q "$w" || { echo "$name: pinned leg $w NOT present"; missing=$((missing + 1)); }; done
	# rows for a leg set that is not the target one measure an unknown shape:
	# abort the set instead of recording it (campaign16 produced 20 such rows).
	[ "$active" -eq 1 ] || { abort_set "$set_name" "$active active route group(s) of $groups for a legs set, want exactly 1 (rg_port=$port_before)"; port=$((port + 1)); continue; }
	[ "$nlegs" -eq "$N" ] && [ "$missing" -eq 0 ] || { abort_set "$set_name" "leg set differs from target: legs=$nlegs/$N missing=$missing present=$desc"; port=$((port + 1)); continue; }
	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	res_set "$set_name-pre"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name legs=$N/$nlegs width=$N rg_port=$port_before route_groups=$groups active=$active standby=$standby pins=$(echo $chosen | tr ' ' ',') legs_desc=$desc sink=$sink"
	res_set "$set_name-post"; res_check "$set_name"
	mux_info "$name" > "$tmp/$set_name.after.json"
	port_after=$(active_json "$tmp/$set_name.after.json" | jq -r '.[0].desc.dst_port')
	echo "# rg src_port before=$port_before after=$port_after $( [ "$port_before" = "$port_after" ] && echo constant || echo CHANGED)" >> "$out/$set_name.carrier.tsv"
	$CLI cli proxy mux width 2 >/dev/null 2>&1 # back to the default steady width
	echo "$name: rg src_port $port_before -> $port_after"
	stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set — the next set will be invalidated if they persist"
	port=$((port + 1))
done

# --- the two-upload cell (UP2=1) -----------------------------------------------
# The goal's upload criterion has two halves. The single upload is the `up`
# cells of every set above, paired against ONE reference. The other half is
# "two concurrent uploads >= the sum of the two best references", and a single
# POST cannot show it: a lone POST takes the lowest-latency tunnel by policy
# (#4972/#4978), so one upload is a single-route number however many tunnels
# exist. This set puts TWO 50 MB uploads through the SAME two-tunnel session at
# the same instant and asks whether they sum.
#
# The bar is measured the same way everything else here is: sequentially, one
# reference upload on each of the two best single routes (slots 1 and 2), per
# trial, immediately before the concurrent pair. The three transfers of a trial
# never overlap each other.
#
# Rows: both concurrent uploads land in mux-tunnels-2-up2.tsv as ordinary
# bench.sh rows (labels …-t<N>a / …-t<N>b) so summarize.sh reads them; the
# per-trial sum and its ratio go to mux-tunnels-2-up2.up2.tsv.
if [ "${UP2:-0}" = 1 ]; then
	set_name="mux-tunnels-2-up2"; name=$(app_name tun2up2); socks=$(app_addr 1141); cur_app=$name
	up2_size=$(norm_sizes "${UP2_SIZE:-50000000}"); up2_size=${up2_size% }
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	f="$out/$set_name.tsv"; u="$out/$set_name.up2.tsv"
	ref1=$paired_ref; ref2=$(paired_resolve "$out" 2)
	if ! stop_app_clean "$name"; then
		abort_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"
	elif [ -z "$ref1" ] || [ -z "$ref2" ] || [ "$ref1" = "$ref2" ]; then
		invalid_set "$set_name" "the two-upload sum needs TWO distinct reference routes, got '$ref1' and '$ref2'"
	else
		timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --tunnels 2 ${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} ${RANGE_CONCURRENCY:+--range-concurrency $RANGE_CONCURRENCY} 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
		sleep 5
		wait_pool "$name" # the standby pool is still filling 5 s after the dial
		mux_info "$name" > "$out/$set_name.legs.json"
		roles=$(rg_roles "$out/$set_name.legs.json")
		groups=${roles%% *}; roles_rest=${roles#* }; active=${roles_rest%% *}; standby=${roles_rest##* }
		tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
		desc=$(jq -r '[.[] | "rg\(.desc.dst_port)\(if .tunnel_role then "/" + .tunnel_role else "" end)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
		if [ "$active" -ne 2 ]; then
			abort_set "$set_name" "shape differs from target: $active active route group(s) of $groups ($standby standby), asked for 2 (groups=$desc)"
		elif ! paired_start 1 "$ref1" "$exit_pk" "$pins" "$sink"; then
			invalid_set "$set_name" "reference slot 1 ($ref1) would not come up"
			stop_app_clean "$name" >/dev/null 2>&1
		elif ! paired_start 2 "$ref2" "$exit_pk" "$pins" "$sink"; then
			invalid_set "$set_name" "reference slot 2 ($ref2) would not come up"
			paired_stop 1; stop_app_clean "$name" >/dev/null 2>&1
		else
			warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
			res_set "$set_name-pre"
			set_started=$(date +%Y-%m-%dT%H:%M:%S)
			echo "# exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=2 route_groups=$groups active=$active standby=$standby groups=$desc refs=$ref1,$ref2 size=$up2_size sink=$sink" > "$f"
			printf '# reference uploads of %s (sequential, one per route), bench.sh rows\n' "$set_name" > "$out/$set_name.paired-rows.tsv"
			printf '# trial\ta_MBps\tb_MBps\tsum_MBps\tref1_MBps\tref2_MBps\tref_sum_MBps\tratio\ta_ok\tb_ok\n' > "$u"
			# payloads once, outside every timed section, as run-capcheck.sh does
			head -c "$up2_size" /dev/urandom > "$tmp/up2a"; sha_a=$(sha256sum "$tmp/up2a" | cut -d' ' -f1)
			head -c "$up2_size" /dev/urandom > "$tmp/up2b"; sha_b=$(sha256sum "$tmp/up2b" | cut -d' ' -f1)
			t=1
			while [ "$t" -le "$trials" ]; do
				r1=$(paired_row 1 "$t" "$up2_size" up); r1=${r1%% *}
				r2=$(paired_row 2 "$t" "$up2_size" up); r2=${r2%% *}
				curl -s --socks5-hostname "$socks" -m 900 -o "$tmp/up2a.r" \
					-w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' \
					-X POST --data-binary "@$tmp/up2a" "$sink/upload" > "$tmp/up2a.w" 2>/dev/null &
				ja=$!
				curl -s --socks5-hostname "$socks" -m 900 -o "$tmp/up2b.r" \
					-w '%{http_code} %{size_upload} %{time_total} %{speed_upload}' \
					-X POST --data-binary "@$tmp/up2b" "$sink/upload" > "$tmp/up2b.w" 2>/dev/null &
				jb=$!
				wait "$ja"; wait "$jb"
				ok_a=0; ok_b=0
				for half in a b; do
					want=$(jq -r .sha256 "$tmp/up2$half.r" 2>/dev/null)
					case $half in a) have=$sha_a ;; *) have=$sha_b ;; esac
					ok=0; [ -n "$want" ] && [ "$want" = "$have" ] && ok=1
					# shellcheck disable=SC2046 # the four -w fields are split on purpose
					set -- $(cat "$tmp/up2$half.w" 2>/dev/null)
					printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$set_name-t$t$half" up "$up2_size" "${4:-0}" "${1:-000}" "${2:-0}" "${3:-0}" "$ok" >> "$f"
					case $half in a) ok_a=$ok ;; *) ok_b=$ok ;; esac
				done
				a=$(grep -v '^#' "$f" | awk -F'\t' -v l="$set_name-t${t}a" '$1==l {printf "%.2f", $4/1e6}')
				b=$(grep -v '^#' "$f" | awk -F'\t' -v l="$set_name-t${t}b" '$1==l {printf "%.2f", $4/1e6}')
				awk -v t="$t" -v a="$a" -v b="$b" -v r1="$r1" -v r2="$r2" -v oa="$ok_a" -v ob="$ok_b" \
					'BEGIN{s=a+b; rs=r1+r2; printf "%d\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%.2f\t%s\t%s\t%s\n", t, a, b, s, r1, r2, rs, (rs>0 ? sprintf("%.3f", s/rs) : "-"), oa, ob}' >> "$u"
				echo "$set_name trial $t: $a + $b MB/s vs references $r1 + $r2 MB/s"
				t=$((t + 1))
			done
			paired_stop 1; paired_stop 2
			res_set "$set_name-post"; res_check "$set_name"
			echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
			mux_events "$set_name" "$name"
			stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set"
		fi
	fi
fi

# The exit-resource gate is the only thing that can fail this script: every set
# that ran is on disk either way.
[ "$exit_res_fail" = 0 ] || echo "run-mux: an exit-resource check FAILED — see $out/exit-resources.tsv"
exit "$exit_res_fail"

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
#   mux-spread-3    (SPREAD=1) the DEFAULT pool session — no pins, no --tunnels,
#                   the pool discovered as the standby set discovers it — under
#                   the LIVE spread policy: SETTINGS gets
#                   spread.max_share=0.4 spread.min_routes=3 merged into it.
#                   50 MB down and up, no cut row. Criterion 10 (v4): >= 0.8 x
#                   the paired reference with no route over its cap, scored from
#                   bench/direction.sh into <set>.assert.tsv.
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
#     results and makes this script exit non-zero at the very end. The run buys
#     the exit's cold-start heap high-water in ONE warm-up transfer before the
#     first reading (EXIT_RES_WARM_BYTES, 50 MB), each set is scored on a
#     <set>-settled reading taken EXIT_RES_SETTLE_S (30) seconds after its post
#     — Go's scavenger gives back what a set borrowed — and the whole run is
#     scored once more by `exit-resources-check.sh <out> --slope`.
#   SETTINGS / ROUTE_SETTINGS — live tuning knobs, applied to the APP UNDER TEST
#     once per set, after it is up and warm and before the first row, and waited
#     for: a knob reaches the app on its keepalive tick, not at once. SETTINGS
#     takes `proxy settings` key=value pairs ("upload.chunk_bytes=2MiB"),
#     ROUTE_SETTINGS whole `route settings` flags, which are restored at set end
#     because a router knob outlives the app. The paired references never
#     receive either — they are the control. Both are recorded in the set header
#     and in <set>.settings.json. See bench/lib-settings.sh, and
#     bench/run-sweep.sh to drive one knob across a list of values.
#   TRIALS_UP — upload cells take 3 trials, download cells the [trials]
#     argument (5). Uploads repeat themselves; downloads do not.
#   SPREAD — criterion 10's spread policy, as live knobs on the default session:
#     SPREAD_SHARE (0.4) and SPREAD_ROUTES (3) are the cap and the floor,
#     SPREAD_SIZE (50 MB) the cell, SPREAD_RATIO_MIN (0.8) the throughput bar and
#     SPREAD_CHUNK_BYTES (4 MiB) the one-chunk slack the cap assert allows
#     (cap + chunk/size). The endpoint ceiling the v4 upload and composition
#     bars are scored against is a separate run: bench/run-ceiling.sh, whose
#     ceiling.tsv bench/verdict.sh picks up from the same directory.
#
# DEFAULT SUITE: TUNNELS="2" LEGS="2", 5 download trials and 3 upload trials.
# The sets that never win — three tunnels, three legs — are on-demand:
# TUNNELS="2 3" LEGS="2 3 5" runs them.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
here=$(dirname "$0")
mkdir -p "$out"
# shellcheck source=bench/lib-pins.sh
. "$here/lib-pins.sh"
# The pin order is a MEASUREMENT, not an alphabet: pin_order ranks the pins by
# this campaign's own reference numbers and drops a route whose reference could
# not carry 50 MB (bench/lib-pins.sh). The explicit 6th argument still wins.
order=${6:-$(pin_order "$out" "$pins")}
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
PAIRED_HERE=$here; CUT_HERE=$here # read by the libraries below
export PAIRED_HERE CUT_HERE
# shellcheck source=bench/lib-paired.sh
. "$here/lib-paired.sh"
# shellcheck source=bench/lib-cut.sh
. "$here/lib-cut.sh"
# shellcheck source=bench/lib-settings.sh
. "$here/lib-settings.sh"
# shellcheck source=bench/lib-blackout.sh
. "$here/lib-blackout.sh"
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
		jq -c --argjson p "$2" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms, sent_bytes, sent_packets, recv_bytes, recv_packets}]}]' 2>/dev/null
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
# --- carrier tracking (campaign21) ---------------------------------------------
# <set>.carrier.tsv used to read the byte deltas of a FIXED list of transports
# captured at set start. After campaign21's cut row the visor dialed a
# REPLACEMENT group (49172) whose first hop was a transport that did not exist
# when the list was taken (sudph 93139b56-acc0-00e5-8ca0-cd0bad229d62), so the
# three 50 MB uploads that rode it show no sender at all in carrier.tsv.
#
# The tracked list is therefore a UNION: the app's route groups are re-read
# before and after every row and every transport they hold is added to it. It is
# never shortened — a cut transport keeps its (empty) rows, and a transport that
# appears mid-set simply becomes new carrier rows from the row it appeared at, so
# the column layout summarize.sh and verdict.sh read is unchanged.
#
# <set>.tps.tsv names each tracked transport ONCE — tp, type, remote pk and the
# row it was first seen at (0 = held at set start) — so a README can say what
# carried which row.
tracked=""      # every transport this set has been measured over, space separated
late_rgs=""     # route groups created after row 1: "<port>@<first hop>(<type>)"
rgs_seen=""     # the ports the set held through row 1
# tps_note <tp> <type> <remote pk> <row>: track a transport on first sight.
tps_note() {
	case " $tracked " in *" $1 "*) return 0 ;; esac
	tracked="$tracked $1"
	printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >> "$out/$set_name.tps.tsv"
}
# track_row <row> <mux info json>: union this reading's transports into the
# tracked list, and — from row 2 on — name any route group the session has
# grown since. Row 0 is the set-start snapshot.
track_row() {
	for _tt in $(echo "$2" | jq -r '.[]?.legs[]?.transport_id // empty' 2>/dev/null); do
		case " $tracked " in *" $_tt "*) continue ;; esac
		_tty=$(echo "$2" | jq -r --arg id "$_tt" 'first(.[]?.legs[]? | select(.transport_id==$id) | .tp_type) // "?"' 2>/dev/null)
		_ttp=$(echo "$2" | jq -r --arg id "$_tt" 'first(.[]?.legs[]? | select(.transport_id==$id) | .remote_pk) // "?"' 2>/dev/null)
		tps_note "$_tt" "${_tty:-?}" "${_ttp:-?}" "$1"
		[ "$1" -le 1 ] || echo "$set_name row $1: new carrier $_tt (${_tty:-?}, remote ${_ttp:-?}) — tracked from this row"
	done
	for _tr in $(echo "$2" | jq -r '.[]?.desc.dst_port // empty' 2>/dev/null); do
		case " $rgs_seen " in *" $_tr "*) continue ;; esac
		rgs_seen="$rgs_seen $_tr"
		[ "$1" -le 1 ] && continue
		_trh=$(echo "$2" | jq -r --argjson p "$_tr" 'first(.[]? | select(.desc.dst_port==$p) | .legs[0] | (.transport_id // "?") + "(" + (.tp_type // "?") + ")") // "?"' 2>/dev/null)
		late_rgs="${late_rgs:+$late_rgs }$_tr@${_trh:-?}"
		echo "$set_name row $1: route group $_tr was created after row 1, first hop ${_trh:-?}"
	done
	return 0
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
	$CLI cli proxy settings --reset mux.width mux.cap >/dev/null 2>&1 # back to inherit
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
# and, when the session grew a route group after row 1, that group's port, first
# hop and transport type — campaign21's cut row left its replacement group
# (49172, sudph 93139b56-acc0-00e5-8ca0-cd0bad229d62) unnamed in every summary.
mux_events() {
	$CLI cli visor state --select diag --json 2>/dev/null |
		jq --arg app "$2" --arg since "${setup_started:-$set_started}" '[.diag.mux_events[]? | select(.app==$app and .at[0:19] >= $since)]' > "$out/$1.mux_events.json" 2>/dev/null
	echo "$1: $(jq 'length' "$out/$1.mux_events.json" 2>/dev/null || echo 0) mux events: $(jq -r '[.[] | .event + "(" + (.reason // "") + ")"] | join(" ")' "$out/$1.mux_events.json" 2>/dev/null | cut -c1-300)${late_rgs:+ | route group(s) created after row 1 (port@first hop): $late_rgs}"
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
	# The carriers are a UNION re-read every row, not the fixed list of the set's
	# tps argument: see track_row. <set>.tps.tsv names each one on first sight.
	tracked=""; late_rgs=""; rgs_seen=""
	printf '# transports %s was measured over; first_seen_row 0 = held at set start\n' "$set_name" > "$out/$set_name.tps.tsv"
	printf '# tp\ttype\tremote_pk\tfirst_seen_row\n' >> "$out/$set_name.tps.tsv"
	if [ -f "$out/$set_name.legs.json" ]; then track_row 0 "$(cat "$out/$set_name.legs.json")"; fi
	for tp in $tps; do tps_note "$tp" '?' '?' 0; done
	[ "$EXIT_SNAP" = 1 ] && printf '# row\texit_mux_route_groups\n' > "$out/$set_name.exit-recovery.tsv"
	set_paired=0
	if [ "$PAIRED" = 1 ]; then
		if paired_start 1 "$paired_ref" "$exit_pk" "$pins" "$sink"; then
			set_paired=1
			paired_header "$paired_ref" "${paired_route:-?}"
		else
			echo "$set_name: unpaired — no contemporaneous reference came up; the set is measured against the bar instead (verdict.sh)"
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
				# the carriers this row may ride: everything tracked so far plus
				# whatever the session has dialed since the last row
				track_row "$row" "$(mux_info "$cur_app")"
				before=""
				for tp in $tracked; do before="$before $tp:$(tp_counters "$tp" | tr ' ' ',')"; done
				if [ "$row" = "$cut_at_row" ]; then
					cut_transfer_row "$set_name-t$t" "$dir" "$n"
				else
					# the answer of a NON-2xx row is kept beside the blackout capture of the same
					# row (bench.sh BENCH_FAIL_PREFIX): X-Upload-Error names why a 502 happened.
					BENCH_FAIL_PREFIX="$out/$set_name.row$row" "$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
				fi
				if [ "$set_paired" = 1 ]; then
					_mlast=$(tail -1 "$f")
					paired_emit "$row" "$(paired_cell "$n" "$dir")" "$pref" "$pok" "$plegs" \
						"$(echo "$_mlast" | awk -F'\t' '{printf "%.2f", $4/1e6}')" "$(echo "$_mlast" | cut -f8)"
				fi
				for tp in $tracked; do
					b=$(echo "$before" | tr ' ' '\n' | grep "^$tp:" | cut -d: -f2)
					a=$(tp_counters "$tp" | tr ' ' ',')
					[ -n "$a" ] && [ -n "$b" ] || { echo "$row	$tp	?	?" >> "$c"; continue; }
					echo "$row	$tp	$(( ${a%,*} - ${b%,*} ))	$(( ${a#*,} - ${b#*,} ))" >> "$c"
				done
				# legs held + loss-recovery counters (sender retx window + SACK feedback, receiver frontier) after every row
				info=$(mux_info "$cur_app")
				# a group dialed DURING the row (the cut row's replacement) is
				# tracked from this row, so its deltas start with the next one
				track_row "$row" "$info"
				echo "$row	legs	$(echo "$info" | jq -r '[.[] | (.desc.dst_port|tostring) + ":" + ([.legs[].transport_id[0:8]] | join(","))] | join(" ")')	-" >> "$c"
				echo "$row	$(echo "$info" | jq -c '[.[] | {rg: .desc.dst_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
				p=$(echo "$info" | jq -c '[.[].desc.dst_port]')
				# A FAILED row loses the exit's view when the session re-dials, so
				# that one case is still snapshotted immediately. Healthy rows are
				# not: see exit_snap_row.
				if [ "$(tail -1 "$f" | cut -f8)" != 1 ]; then
					# The local receive-loop proof FIRST: the parked goroutine
					# and the route group at capacity clear within a minute,
					# and the exit query below can spend 60 s of that
					# (bench/lib-blackout.sh).
					blackout_capture "$out" "$set_name" "$row"
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
		x=$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms, sent_bytes, sent_packets, recv_bytes, recv_packets}]}]')
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
# res_warmup <socks>: one warm-up transfer, once per run, before the FIRST
# reading of the FIRST set. The exit pays its cold-start heap high-water on
# whichever set runs first — campaign21 and the dc23fd9ff smoke both failed set 1
# by +66 / +76 MiB and passed every set after it — so the high-water is bought
# here, outside any set's pre/post bracket, and the `warmup` reading records
# where it left the exit. Skipped when the out dir already holds a `-pre` row.
res_warmup() {
	[ "$EXIT_RES" = 1 ] || return 0
	[ -f "$out/exit-resources.tsv" ] &&
		awk -F'\t' '$2 ~ /-pre$/ { f = 1 } END { exit !f }' "$out/exit-resources.tsv" && return 0
	echo "exit-resources: warm-up transfer (${EXIT_RES_WARM_BYTES:-50000000} B down) before the first reading of the run"
	"$here/bench.sh" "$1" "$sink" "${EXIT_RES_WARM_BYTES:-50000000}" down warmup >/dev/null 2>&1
	res_set warmup
	return 0
}
# res_settle <set>: the reading the set is actually scored on. Go's scavenger
# hands back what a set borrowed within seconds (the smoke returned 52 MiB in 29
# idle ones), so `post` measures the peak and `settled` measures what the exit
# kept. EXIT_RES_SETTLE_S=0 turns the wait off and the check falls back to
# post - pre.
res_settle() {
	[ "$EXIT_RES" = 1 ] || return 0
	[ "${EXIT_RES_SETTLE_S:-30}" -gt 0 ] 2>/dev/null || return 0
	sleep "${EXIT_RES_SETTLE_S:-30}"
	res_set "$1-settled"
	return 0
}
# res_slope: the campaign check, once at the end of the run. No single set of
# campaign21 failed the per-set rule while the exit drifted 347 -> 479 MB across
# it; a slope over the whole run is what sees that.
res_slope() {
	[ "$EXIT_RES" = 1 ] || return 0
	"$here/exit-resources-check.sh" "$out" --slope || exit_res_fail=1
	return 0
}

# tunnel_dial <app> <socks> <N> <set>: dial the session with N tunnels, let the
# standby pool settle, and read the REALIZED shape into $groups / $active /
# $standby / $tps / $desc (and <set>.legs.json). Two callers: the set's own dial
# and the reconcile below, which has to re-read the same fields.
tunnel_dial() {
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$1" -a "$2" --tunnels "$3" ${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} ${RANGE_CONCURRENCY:+--range-concurrency $RANGE_CONCURRENCY} 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
	sleep 5
	wait_pool "$1" # the standby pool is still filling 5 s after the dial
	mux_info "$1" > "$out/$4.legs.json"
	roles=$(rg_roles "$out/$4.legs.json")
	groups=${roles%% *}; roles_rest=${roles#* }; active=${roles_rest%% *}; standby=${roles_rest##* }
	# the carriers are named from EVERY group, standby included: a standby tunnel
	# moves only its keepalive, and a leg promoted mid-set has to be in the list.
	tps=$(jq -r '.[].legs[].transport_id' "$out/$4.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
	desc=$(jq -r '[.[] | "rg\(.desc.dst_port)\(if .tunnel_role then "/" + .tunnel_role else "" end)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$4.legs.json" 2>/dev/null)
	echo "$1: $groups route group(s) — $active active, $standby standby; first-hop tps: $tps"
}

# --- stream-level: N tunnels ---------------------------------------------------
port=1101
for N in $tunnel_counts; do
	name=$(app_name "tun$N"); socks=$(app_addr "$port"); set_name="mux-tunnels-$N"; cur_app=$name
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	# a group left over from the previous set would be dialed instead of this
	# set's own tunnels: refuse to measure until the app owns nothing.
	stop_app_clean "$name" || { abort_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"; port=$((port + 1)); continue; }
	tunnel_dial "$name" "$socks" "$N" "$set_name"
	# the target IS N ACTIVE tunnels; a set that came up in another shape measures
	# something else and must not be recorded as this set (campaign16). Standby
	# tunnels are not part of the shape: the pool holds as many as the router has
	# disjoint first hops for.
	#
	# A session that already holds MORE actives than this set asks for is not a
	# measurement problem, it is leftover shape: the UP2 cell of 2026-09-18
	# 1008cc8e5 left three actives behind and mux-tunnels-2 went INVALID on a
	# shape it could simply have re-taken. Restart the session once with the
	# asked shape, let the pool settle, and judge the re-read.
	if [ "$active" -ne "$N" ]; then
		echo "$set_name: session holds $active active route group(s) of $groups, asked for $N — restarting it with --tunnels $N and re-reading the shape"
		if stop_app_clean "$name"; then
			tunnel_dial "$name" "$socks" "$N" "$set_name"
		else
			echo "$set_name: route group(s) $rg_left survived two proxy stops during the reconcile"
		fi
	fi
	[ "$active" -eq "$N" ] || { abort_set "$set_name" "shape differs from target after a reconcile: $active active route group(s) of $groups ($standby standby), asked for $N (groups=$desc)"; port=$((port + 1)); continue; }
	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	# live knobs, once per set: the visor's app store is cleared when the app
	# stops, so SETTINGS can only be applied here — after the dial, the shape
	# check and the warm probes, and before the first row (bench/lib-settings.sh).
	settings_apply "$set_name" "$name"
	res_warmup "$socks"
	res_set "$set_name-pre"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=$N route_groups=$groups active=$active standby=$standby groups=$desc sink=$sink${settings_note:+ $settings_note}"
	res_set "$set_name-post"; res_settle "$set_name"; res_check "$set_name"
	settings_restore "$set_name" # the app knobs die with the app; the ROUTER knobs do not
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
	# N pinned first hops or no legs set: a set run on fewer measures a width it
	# does not claim. `wc -l` counts an empty $chosen as one line, so the pins
	# are counted, not the lines.
	nchosen=$(printf '%s\n' "$chosen" | grep -vc '^$')
	[ "$nchosen" -eq "$N" ] || { abort_set "$set_name" "need $N ranked pins for a legs-$N set, have $nchosen (${order:-none}) — see the pin order lines above; PIN_ALLOW_UNRANKED=1 or an explicit 6th-argument order overrides"; port=$((port + 1)); continue; }
	first=$(echo "$chosen" | head -1)
	legs_file="$out/$set_name.target.json"
	pin_bad=0
	for s in $chosen; do pin_ok "$pins/via-$s.json" || pin_bad=$((pin_bad + 1)); done
	[ "$pin_bad" -eq 0 ] || { abort_set "$set_name" "$pin_bad of $N pin file(s) are stubs, not routes — fix the pins dir $pins"; port=$((port + 1)); continue; }
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
	# live knobs, once per set: the visor's app store is cleared when the app
	# stops, so SETTINGS can only be applied here — after the dial, the shape
	# check and the warm probes, and before the first row (bench/lib-settings.sh).
	settings_apply "$set_name" "$name"
	res_warmup "$socks"
	res_set "$set_name-pre"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name legs=$N/$nlegs width=$N rg_port=$port_before route_groups=$groups active=$active standby=$standby pins=$(echo $chosen | tr ' ' ',') legs_desc=$desc sink=$sink${settings_note:+ $settings_note}"
	res_set "$set_name-post"; res_settle "$set_name"; res_check "$set_name"
	settings_restore "$set_name" # the app knobs die with the app; the ROUTER knobs do not
	mux_info "$name" > "$tmp/$set_name.after.json"
	port_after=$(active_json "$tmp/$set_name.after.json" | jq -r '.[0].desc.dst_port')
	echo "# rg src_port before=$port_before after=$port_after $( [ "$port_before" = "$port_after" ] && echo constant || echo CHANGED)" >> "$out/$set_name.carrier.tsv"
	$CLI cli proxy settings --reset mux.width mux.cap >/dev/null 2>&1 # back to inherit: the per-app override is persisted, a leftover width shapes every later dial
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
	late_rgs="" # this set writes no per-row carrier deltas, so it tracks no late groups
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
			invalid_set "$set_name" "unpaired: reference slot 1 ($ref1) would not come up"
			stop_app_clean "$name" >/dev/null 2>&1
		elif ! paired_start 2 "$ref2" "$exit_pk" "$pins" "$sink"; then
			invalid_set "$set_name" "unpaired: reference slot 2 ($ref2) would not come up"
			paired_stop 1; stop_app_clean "$name" >/dev/null 2>&1
		else
			warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
			# live knobs, once per set: the visor's app store is cleared when the app
			# stops, so SETTINGS can only be applied here — after the dial, the shape
			# check and the warm probes, and before the first row (bench/lib-settings.sh).
			settings_apply "$set_name" "$name"
			res_warmup "$socks"
			res_set "$set_name-pre"
			set_started=$(date +%Y-%m-%dT%H:%M:%S)
			echo "# exit=$exit_pk local=$local_commit exit_commit=$ec session=$name tunnels=2 route_groups=$groups active=$active standby=$standby groups=$desc refs=$ref1,$ref2 size=$up2_size sink=$sink${settings_note:+ $settings_note}" > "$f"
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
			res_set "$set_name-post"; res_settle "$set_name"; res_check "$set_name"
			settings_restore "$set_name" # the app knobs die with the app; the ROUTER knobs do not
			echo "$set_name: $(grep -vc '^#' "$f") rows, hash_ok=$(grep -v '^#' "$f" | awk -F'\t' '$8==1' | wc -l)"
			mux_events "$set_name" "$name"
			stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set"
		fi
	fi
fi

# --- the spread policy set (SPREAD=1) ------------------------------------------
# Criterion 10 (v4, 2026-09-18): "under a spread policy that caps any one route
# at 40 % of the bytes and holds at least three routes, throughput >= 0.8 x the
# best single reference and no route exceeds its cap, measured from both ends'
# per-leg counters at 50 MB down and up; the policy is a live setting".
#
# So the set is the DEFAULT POOL SESSION — no pins, no --tunnels, the pool
# discovered exactly as the standby set discovers it — with the policy applied
# as live knobs on top (bench/lib-settings.sh), which is the "live setting" half
# of the criterion: nothing here is a boot flag and nothing is compiled in.
#
#   SETTINGS="spread.max_share=${SPREAD_SHARE:-0.4} spread.min_routes=${SPREAD_ROUTES:-3}"
#
# merged with whatever SETTINGS the campaign already holds, so a sweep over
# another knob still carries the spread policy.
#
# 50 MB down and up, `trials` trials each, paired like every other set. NO CUT
# ROW: the cut moves bytes between routes mid-set, which is exactly the quantity
# the cap assert measures. Afterwards bench/direction.sh turns the two ends'
# per-leg counters into per-row shares and <set>.assert.tsv scores them.
sp_assert() { printf '%s\t%s\t%s\t%s\n' "$1" "$2" "$3" "$4" >> "$sp_af"; }
sp_verdict() { if [ "$1" = 1 ]; then echo PASS; else echo FAIL; fi; }
# spread_asserts: criterion 10's four asserts, from the artefacts the set wrote.
# The cap tolerance is ONE CHUNK's worth of slack — a scheduler that places whole
# chunks cannot land on 40.000 %, and the last chunk of a transfer is indivisible
# — stated as a fraction: SPREAD_SHARE + SPREAD_CHUNK_BYTES / <transfer size>.
spread_asserts() {
	sp_af="$out/$set_name.assert.tsv"
	sp_dir="$out/$set_name.direction.tsv"
	sp_tol=$(awk -v c="$sp_share" -v k="$sp_chunk" -v n="$sp_size" 'BEGIN{printf "%.3f", c + k / n}')
	{
		printf '# assert\tvalue\twant\tverdict\n'
		printf '# criterion 10: cap %s of the bytes on any one route, hold >= %s routes, >= %s x the paired reference, at %s MB down and up\n' \
			"$sp_share" "$sp_routes" "$sp_ratio_min" "$((sp_size / 1000000))"
		printf '# shares are bench/direction.sh per-row leg shares in the PAYLOAD direction (reverse bytes on a download, forward bytes on an upload), over the legs of ACTIVE groups only\n'
		printf '# cap tolerance = %s + %s/%s = %s (one chunk of slack)\n' "$sp_share" "$sp_chunk" "$sp_size" "$sp_tol"
	} > "$sp_af"
	if [ ! -f "$sp_dir" ]; then
		sp_assert routes_active "no $set_name.direction.tsv" ">= $sp_routes" FAIL
		sp_assert max_share "no $set_name.direction.tsv" "<= $sp_tol" FAIL
	else
		# per row: in-scope legs, and the largest payload-direction share
		sp_row=$(awk -F'\t' '!/^#/ && NF >= 16 && $1 ~ /^[0-9]+$/ && $8 == "in" {
				sh = ($3 == "up") ? $15 : $16
				sub(/%$/, "", sh)
				if (sh == "-" || sh == "") next
				legs[$1]++
				if (sh + 0 > mx[$1]) { mx[$1] = sh + 0; who[$1] = $5 }
			}
			END {
				for (r in legs) {
					n++
					if (minlegs == 0 || legs[r] < minlegs) minlegs = legs[r]
					if (legs[r] > maxlegs) maxlegs = legs[r]
					if (mx[r] > worst) { worst = mx[r]; wrow = r; wleg = who[r] }
				}
				printf "%d %d %d %.4f %d %s", n + 0, minlegs + 0, maxlegs + 0, worst / 100, wrow + 0, (wleg == "" ? "-" : wleg)
			}' "$sp_dir")
		sp_n=$(echo "$sp_row" | cut -d' ' -f1); sp_min=$(echo "$sp_row" | cut -d' ' -f2)
		sp_max=$(echo "$sp_row" | cut -d' ' -f3); sp_worst=$(echo "$sp_row" | cut -d' ' -f4)
		sp_wrow=$(echo "$sp_row" | cut -d' ' -f5); sp_wleg=$(echo "$sp_row" | cut -d' ' -f6)
		if [ "${sp_n:-0}" -eq 0 ]; then
			sp_assert routes_active "no scored rows in $set_name.direction.tsv" ">= $sp_routes" FAIL
			sp_assert max_share "no scored rows in $set_name.direction.tsv" "<= $sp_tol" FAIL
		else
			sp_assert routes_active "min $sp_min, max $sp_max over $sp_n row(s)" ">= $sp_routes" \
				"$(sp_verdict "$([ "$sp_min" -ge "$sp_routes" ] && echo 1 || echo 0)")"
			sp_assert max_share "$sp_worst (worst row $sp_wrow, leg $sp_wleg)" "<= $sp_tol ($sp_share + chunk/size)" \
				"$(sp_verdict "$(awk -v a="$sp_worst" -v b="$sp_tol" 'BEGIN{print (a + 0 <= b + 0) ? 1 : 0}')")"
		fi
	fi
	# throughput: the paired ratio of each cell against the contemporaneous
	# single-route reference, which is what "the best single reference" means
	# here — the reference bench/pick-ref.sh probed for this campaign.
	for sp_cell in "$((sp_size / 1000000))down" "$((sp_size / 1000000))up"; do
		sp_r=$(grep -v '^#' "$out/$set_name.paired.tsv" 2>/dev/null |
			awk -F'\t' -v c="$sp_cell" '$2==c && $5!="-" {print $5}' | sort -n |
			awk '{a[NR]=$1} END{if (!NR) {print "-"; exit} printf "%.3f", (NR%2)?a[(NR+1)/2]:(a[NR/2]+a[NR/2+1])/2}')
		if [ "$sp_r" = - ] || [ -z "$sp_r" ]; then
			sp_assert "ratio_$sp_cell" "no paired rows" ">= $sp_ratio_min" FAIL
		else
			sp_assert "ratio_$sp_cell" "$sp_r" ">= $sp_ratio_min" \
				"$(sp_verdict "$(awk -v a="$sp_r" -v b="$sp_ratio_min" 'BEGIN{print (a + 0 >= b + 0) ? 1 : 0}')")"
		fi
	done
	sp_rows=$(grep -vc '^#' "$out/$set_name.tsv" 2>/dev/null || echo 0)
	sp_hok=$(grep -v '^#' "$out/$set_name.tsv" 2>/dev/null | awk -F'\t' '$8==1' | wc -l)
	sp_assert hashes "$sp_hok/$sp_rows" "$sp_rows/$sp_rows" \
		"$(sp_verdict "$([ "$sp_rows" -gt 0 ] && [ "$sp_hok" -eq "$sp_rows" ] && echo 1 || echo 0)")"
	# a policy the binary refused is a set measured on the DEFAULT policy under
	# the spread set's name: that has to read FAIL, not pass quietly.
	case ${settings_note:-} in
	*REFUSED* | *settings_pending=*) sp_sv=FAIL ;;
	"") sp_sv=FAIL ;;
	*) sp_sv=PASS ;;
	esac
	sp_assert spread_policy "${settings_note:-not applied}" \
		"spread.max_share=$sp_share spread.min_routes=$sp_routes applied" "$sp_sv"
}
if [ "${SPREAD:-0}" = 1 ]; then
	late_rgs=""
	sp_share=${SPREAD_SHARE:-0.4}; sp_routes=${SPREAD_ROUTES:-3}
	sp_chunk=${SPREAD_CHUNK_BYTES:-4194304}   # one range chunk, the granularity a share can miss by
	sp_ratio_min=${SPREAD_RATIO_MIN:-0.8}
	sp_size=$(norm_sizes "${SPREAD_SIZE:-50000000}"); sp_size=${sp_size% }
	set_name="mux-spread-$sp_routes"; name=$(app_name spread3); socks=$(app_addr 1151); cur_app=$name
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	if ! stop_app_clean "$name"; then
		abort_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"
	else
		# no pins, no --tunnels: the DEFAULT session, pool discovered, exactly as
		# the standby set dials it. The policy arrives as a live knob below.
		timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" \
			${SPREAD_TUNNELS:+--tunnels $SPREAD_TUNNELS} \
			${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} ${RANGE_CONCURRENCY:+--range-concurrency $RANGE_CONCURRENCY} 2>&1 |
			grep -iv debug | grep -i "tunnel\|standby\|pool\|running\|error\|fatal" | head -5
		sleep 5
		wait_pool "$name"
		mux_info "$name" > "$out/$set_name.legs.json"
		roles=$(rg_roles "$out/$set_name.legs.json")
		groups=${roles%% *}; roles_rest=${roles#* }; active=${roles_rest%% *}; standby=${roles_rest##* }
		tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
		desc=$(jq -r '[.[] | "rg\(.desc.dst_port)\(if .tunnel_role then "/" + .tunnel_role else "" end)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
		echo "$name: $groups route group(s) — $active active, $standby standby; first-hop tps: $tps"
		if [ "${groups:-0}" -lt 1 ]; then
			abort_set "$set_name" "the default session came up with no route group at all (groups=$desc)"
		else
			# NOT a shape check: how many routes the policy manages to hold IS
			# the measurement (the routes_active assert), so a session that holds
			# too few is recorded and scored FAIL, never aborted.
			warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
			sp_settings_before=${SETTINGS:-}
			SETTINGS="${sp_settings_before:+$sp_settings_before }spread.max_share=$sp_share spread.min_routes=$sp_routes"
			settings_apply "$set_name" "$name"
			res_warmup "$socks"
			res_set "$set_name-pre"
			# 50 MB down and up only, and no cut row
			sp_sizes_before=$sizes; sizes=$sp_size
			sp_dirs_set=0; [ "${DIRS+x}" = x ] && { sp_dirs_set=1; sp_dirs_before=$DIRS; }
			DIRS="down up"
			sp_cut_set=0; [ "${CUT_ROW+x}" = x ] && { sp_cut_set=1; sp_cut_before=$CUT_ROW; }
			CUT_ROW=0
			run_set "$set_name" "$socks" "$tps" \
				"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name spread=max_share=$sp_share,min_routes=$sp_routes route_groups=$groups active=$active standby=$standby groups=$desc pins=none sink=$sink${settings_note:+ $settings_note}"
			sizes=$sp_sizes_before
			if [ "$sp_dirs_set" = 1 ]; then DIRS=$sp_dirs_before; else unset DIRS; fi
			if [ "$sp_cut_set" = 1 ]; then CUT_ROW=$sp_cut_before; else unset CUT_ROW; fi
			res_set "$set_name-post"; res_settle "$set_name"; res_check "$set_name"
			settings_restore "$set_name" # the app knobs die with the app; the ROUTER knobs do not
			SETTINGS=$sp_settings_before
			# both ends' per-leg counters -> per-row shares, then the asserts
			"$here/direction.sh" "$out" "$set_name" || echo "$set_name: direction.sh did not run — the share asserts will say so"
			spread_asserts
			echo "--- $set_name asserts"
			cat "$out/$set_name.assert.tsv"
			stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set"
		fi
	fi
fi

# The exit-resource gate is the only thing that can fail this script: every set
# that ran is on disk either way. The per-set checks have already run; this is
# the campaign-long RssAnon slope over every reading the run took.
res_slope
[ "$exit_res_fail" = 0 ] || echo "run-mux: an exit-resource check FAILED — see $out/exit-resources.tsv"
exit "$exit_res_fail"

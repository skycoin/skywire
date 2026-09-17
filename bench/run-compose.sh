#!/bin/sh
# run-compose.sh — criterion 4 "composition": tunnels AND legs at the same time.
#
#   bench/run-compose.sh <exit pk> <out dir> <pins dir> [trials] [sink] [pin order]
#
# One set per COMPOSE entry "<tunnels>x<legs>" (default "2x2"): the proxy
# starts with `--tunnels T` (T independent route groups, stream-level mux) and
# every route group is then pinned to L two-hop legs of its own (packet-level
# mux), tunnel i taking pins [i*L, i*L+L) of the pin order — so the tunnels are
# transport-disjoint and T x L pinned first-hop transports carry the set.
#
# The rg selector is the group's OWN port, desc.dst_port (#4967): every rg of
# one app shares src_port=3 and differs only in dst_port, so
# `proxy mux set --rg <dst_port> --legs <file> --prune` is what names a single
# tunnel. `proxy start --route ... --tunnels N>1` is rejected on purpose —
# start the tunnels, then pin each one. The header records pinned=yes|no and
# the realized shape is recorded as-is either way.
#
# Artefacts are exactly run-mux.sh's, so summarize.sh / verdict.sh work
# unchanged: mux-compose-T<t>xL<l>.tsv (+ .carrier.tsv, .recovery.tsv,
# .mux_events.json, .legs.json, .exit-recovery.tsv, .paired.tsv). Row order is
# run-mux.sh's: 10 MB down x trials, 10 MB up, 50 MB down, 50 MB up, and — with
# CELL100=1 — 100 MB down.
#
# The same 2026-09-17 goal knobs as run-mux.sh apply here, since criterion 4 is
# scored against the other sets of the same run:
#   PAIRED / PAIRED_REF  one contemporaneous reference row per mux row
#                        (bench/lib-paired.sh, <set>.paired.tsv)
#   SIZES / CELL100      the 100 MB download cell criterion 4 asks for
#   TRIALS_UP            3 upload trials against the [trials] download trials
#   EXIT_RES             exit RssAnon/CPU recorded and gated around every set,
#                        with the run's one warm-up transfer, the per-set
#                        <set>-settled reading (EXIT_RES_SETTLE_S) and the
#                        end-of-run --slope check
#
# The functions below are copied from run-mux.sh rather than sourced: run-mux.sh
# is a script with top-level work, not a library. What IS shared lives in
# bench/lib-paired.sh and bench/lib-cut.sh, which are libraries.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
exit_pk=$1; out=$2; pins=$3; trials=${4:-5}; sink=${5:-http://127.0.0.1:18080}
order=${6:-$(ls "$pins"/via-*.json | sed 's|.*/via-||; s|\.json$||' | tr '\n' ' ')}
here=$(dirname "$0")
mkdir -p "$out"
local_commit=$(git -C "$here/.." rev-parse --short=9 HEAD)
PAIRED_HERE=$here # read by the library below
export PAIRED_HERE
# shellcheck source=bench/lib-paired.sh
. "$here/lib-paired.sh"
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
# CELL100=1 adds the 100 MB DOWNLOAD cell criterion 4 asks for: composition has
# to be "not worse than each alone at 10 MB, and >= the better of the two alone
# at 50 MB and 100 MB", and the 100 MB cell is where a per-object prelude stops
# dominating. DIRS100="down up" measures its upload too.
if [ "${CELL100:-0}" = 1 ]; then
	case " $sizes " in *" 100000000 "*) ;; *) sizes="${sizes}100000000 " ;; esac
fi
size_dirs() { if [ "$1" -ge 100000000 ]; then echo "${DIRS100:-down}"; else echo "${DIRS:-down up}"; fi; }
# trials_for <dir>: 5 download trials, 3 upload trials. Upload medians repeat
# within 5 % across runs; download medians do not.
trials_for() { if [ "$1" = up ]; then echo "${TRIALS_UP:-3}"; else echo "$trials"; fi; }
# DEFAULT SUITE: the 2x2 composition only. 2x1 — two tunnels of one pinned leg
# each — is the hand-picked ceiling check (it took the best single cell of
# campaign20, 8.33 MB/s) and is on demand: COMPOSE="2x2 2x1".
compose=${COMPOSE:-"2x2"}
EXIT_RES=${EXIT_RES:-1}
exit_res_fail=0
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
# to <set>.exit-recovery.tsv at set START and set END — the row column holds
# `start` or `end` instead of a row number. It used to be one query after EVERY
# row, which costs up to EXIT_SNAP_TIMEOUT seconds of dead time per row whenever
# dmsg is slow. A row that FAILS still snapshots the exit immediately, because a
# re-dial would erase the evidence. EXIT_SNAP=0 turns the file off.
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
	for _ in 1 2 3 4; do
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
	printf '# row\ttp\tsent_delta\trecv_delta\n' > "$c"
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
	exit_snap_row start
	row=0
	for n in $sizes; do
		for dir in $(size_dirs "$n"); do
			t=1
			while [ $t -le "$(trials_for "$dir")" ]; do
				row=$((row + 1))
				# the paired reference row of the SAME cell, on one single route,
				# immediately BEFORE the mux row and never at the same time as it
				pref=-; pok=0; plegs=-
				if [ "$set_paired" = 1 ]; then
					_pv=$(paired_row 1 "$row" "$n" "$dir")
					pref=${_pv%% *}; _pv=${_pv#* }; pok=${_pv%% *}; plegs=${_pv##* }
				fi
				before=""
				for tp in $tps; do before="$before $tp:$(tp_counters "$tp" | tr ' ' ',')"; done
				"$here/bench.sh" "$socks" "$sink" "$n" "$dir" "$set_name-t$t" >> "$f"
				if [ "$set_paired" = 1 ]; then
					_mlast=$(tail -1 "$f")
					paired_emit "$row" "$(paired_cell "$n" "$dir")" "$pref" "$pok" "$plegs" \
						"$(echo "$_mlast" | awk -F'\t' '{printf "%.2f", $4/1e6}')" "$(echo "$_mlast" | cut -f8)"
				fi
				for tp in $tps; do
					b=$(echo "$before" | tr ' ' '\n' | grep "^$tp:" | cut -d: -f2)
					a=$(tp_counters "$tp" | tr ' ' ',')
					[ -n "$a" ] && [ -n "$b" ] || { printf '%s\t%s\t?\t?\n' "$row" "$tp" >> "$c"; continue; }
					printf '%s\t%s\t%s\t%s\n' "$row" "$tp" "$(( ${a%,*} - ${b%,*} ))" "$(( ${a#*,} - ${b#*,} ))" >> "$c"
				done
				# legs held per rg + loss-recovery counters after every row
				info=$(mux_info "$cur_app")
				printf '%s\tlegs\t%s\t-\n' "$row" "$(echo "$info" | jq -r '[.[] | (.desc.dst_port|tostring) + ":" + ([.legs[].transport_id[0:8]] | join(",")) ] | join(" ")')" >> "$c"
				printf '%s\t%s\n' "$row" "$(echo "$info" | jq -c '[.[] | {rg: .desc.dst_port, recovery}]')" >> "$out/$set_name.recovery.tsv"
				p=$(echo "$info" | jq -c '[.[].desc.dst_port]')
				# A FAILED row loses the exit's view when the session re-dials, so
				# that one case is still snapshotted immediately. Healthy rows are
				# not: the exit is queried once at set start and once at set end
				# (see exit_snap_row), because a per-row query costs up to
				# EXIT_SNAP_TIMEOUT seconds of dead time whenever dmsg is slow.
				if [ "$(tail -1 "$f" | cut -f8)" != 1 ]; then
					printf '# exit-on-fail %s\t%s\n' "$row" "$(exit_rgs 60 "$p")" >> "$out/$set_name.recovery.tsv"
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
	mux_events "$set_name" "$cur_app"
	# the EXIT's view of the same groups at the end of the set (its receiver-side wedge counters);
	# our desc.dst_port (the ephemeral local port) is the exit's desc.src_port for the same group
	ports=$(mux_info "$cur_app" | jq -c '[.[].desc.dst_port]')
	x=""; for _ in 1 2 3; do
		x=$(timeout 90 $CLI cli visor state --via "dmsg://$exit_pk" --select mux_route_groups --json 2>/dev/null | jq -c --argjson p "$ports" '[.mux_route_groups[]? | select(.desc.src_port as $s | $p | index($s)) | {rg: .desc.src_port, recovery, legs: [.legs[] | {tp: .transport_id[0:8], standby, retransmits, dup_bytes, ack_delay_ms}]}]')
		[ -n "$x" ] && [ "$x" != "[]" ] && break
		sleep 3
	done
	printf '# exit\t%s\n' "$x" >> "$out/$set_name.recovery.tsv"
	# the same reading is the `end` row of the per-set exit snapshots: one query,
	# two readers.
	[ "$EXIT_SNAP" = 1 ] && printf 'end\t%s\n' "${x:-{\}}" >> "$out/$set_name.exit-recovery.tsv"
	return 0
}

# exit_snap_row <start|cut|end>: ONE exit-side snapshot of the set's current
# route group(s), tagged with the moment rather than a row number.
exit_snap_row() {
	[ "$EXIT_SNAP" = 1 ] || return 0
	_esp=$(mux_info "$cur_app" | jq -c '[.[].desc.dst_port]' 2>/dev/null)
	[ -n "$_esp" ] || _esp='[]'
	_esx=$(exit_rgs "$EXIT_SNAP_TIMEOUT" "$_esp")
	[ -n "$_esx" ] || { _esx='{}'; echo "$set_name: $1 exit snapshot failed/timed out after ${EXIT_SNAP_TIMEOUT}s — empty object recorded"; }
	printf '%s\t%s\n' "$1" "$_esx" >> "$out/$set_name.exit-recovery.tsv"
}

# wait_width <app> <groups> <legs each>: let the adaptive engine converge to the
# requested shape before the set starts (width applies on the rg's next tick).
wait_width() {
	i=1
	while [ $i -le 12 ]; do
		# ACTIVE groups only: a standby tunnel holds one leg and would otherwise
		# make a half-converged shape look converged.
		got=$(mux_info "$1" | jq --argjson g "$2" --argjson l "$3" \
			"$_role_filter | [.[] | select((.legs|length) >= \$l)] | length >= \$g" 2>/dev/null)
		[ "$got" = true ] && return 0
		sleep 2
		i=$((i + 1))
	done
	return 1
}

ec=$(exit_commit)
echo "local=$local_commit exit=$ec order=$order compose=$compose sizes=$sizes"
paired_ref=""
if [ "$PAIRED" = 1 ]; then
	paired_ref=$(paired_resolve "$out" 1)
	echo "paired references: PAIRED_REF=$PAIRED_REF resolved to '$paired_ref'"
fi
# res_set <label>: one exit RssAnon/CPU reading, named for the set it brackets.
res_set() { [ "$EXIT_RES" = 1 ] && "$here/exit-resources.sh" "$out" "$1" "$exit_pk"; return 0; }
# res_check <set>: score the pair. The FAILURE is remembered, not acted on — the
# results are kept and this script exits non-zero only at the very end.
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
npins=$(echo "$order" | tr ' ' '\n' | grep -vc '^$')

port=1121
for spec in $compose; do
	T=${spec%x*}; L=${spec#*x}
	case "$T$L" in *[!0-9]*) echo "compose entry '$spec' is not <tunnels>x<legs> — skipping"; continue ;; esac
	name=$(app_name "comp$spec"); socks=$(app_addr "$port"); set_name="mux-compose-T${T}xL${L}"; cur_app=$name
	setup_started=$(date +%Y-%m-%dT%H:%M:%S)
	# a group left over from the previous set would be dialed instead of this
	# set's own tunnels (and breaks any later `--route` reconcile): refuse to
	# measure until the app owns nothing.
	stop_app_clean "$name" || { abort_set "$set_name" "route group(s) $rg_left survived two proxy stops before setup"; port=$((port + 1)); continue; }
	# pin the engine's active width to L BEFORE the dial: the established mux
	# width is decided at dial time, and width/cap apply live on the next tick.
	$CLI cli proxy mux cap "$L" >/dev/null 2>&1
	$CLI cli proxy mux width "$L" >/dev/null 2>&1
	# --route pins ONE group and is rejected with --tunnels >1 on purpose: start
	# the tunnels plain, then pin each one by its own port below.
	timeout 240 $CLI cli proxy start -k "$exit_pk" -n "$name" -a "$socks" --tunnels "$T" ${RANGE_PORT:+--range-port $RANGE_PORT} ${RANGE_CHUNK_KIB:+--range-chunk-kib $RANGE_CHUNK_KIB} ${RANGE_CONCURRENCY:+--range-concurrency $RANGE_CONCURRENCY} 2>&1 | grep -iv debug | grep -i "tunnel\|running\|error\|fatal" | head -3
	sleep 5
	wait_pool "$name" # the standby pool is still filling 5 s after the dial

	# --- per-tunnel pinning, by the rg's OWN port (desc.dst_port, #4967) -----
	# At this point each rg holds the single auto leg it dialed on; `mux set
	# --rg <dst_port> --legs <L pins> --prune` replaces it with L pinned legs,
	# so tunnel i ends up on pins [i*L, i*L+L) and the tunnels are disjoint.
	# Only the ACTIVE groups are pinned and counted: the standby pool holds as
	# many more as the router has disjoint first hops for, and they carry no
	# streams (#4986).
	pinned=no
	mux_info "$name" > "$out/$set_name.legs.json"
	roles=$(rg_roles "$out/$set_name.legs.json")
	allgroups=${roles%% *}; roles_rest=${roles#* }; ngroups=${roles_rest%% *}; nstandby=${roles_rest##* }
	ndst=$(active_json "$out/$set_name.legs.json" | jq '[.[].desc.dst_port] | unique | length' 2>/dev/null || echo 0)
	echo "$name: $allgroups route group(s) — $ngroups active, $nstandby standby"
	rm -f "$out/$set_name".rg*.target.json # a re-run's rg ports differ; don't mix targets
	assign=""
	if [ "$ngroups" -eq "$T" ] && [ "$ndst" -eq "$T" ] && [ $((T * L)) -le "$npins" ]; then
		i=0
		for dp in $(active_ports "$out/$set_name.legs.json"); do
			legs_file="$out/$set_name.rg$dp.target.json"
			slice=$(echo "$order" | tr ' ' '\n' | grep -v '^$' | sed -n "$((i * L + 1)),$((i * L + L))p")
			for s in $slice; do cat "$pins/via-$s.json"; done | jq -s 'add' > "$legs_file"
			assign="$assign rg$dp=$(echo $slice | tr ' ' ',')"
			echo "$set_name: rg$dp <- $(echo $slice | tr '\n' ' ')"
			timeout 300 $CLI cli proxy mux set -n "$name" --rg "$dp" --legs "$legs_file" --prune 2>&1 | grep -iv debug | head -5
			i=$((i + 1))
		done
		pinned=yes
	else
		echo "$set_name: per-tunnel pinning skipped (active groups=$ngroups/$T of $allgroups, distinct_dst_ports=$ndst pins=$npins needed=$((T * L))) — recording the engine's own shape"
	fi
	# pin the engine's active width to L AFTER the legs exist, so every pinned
	# leg stripes from the first row instead of sitting in warm standby
	# (width/cap apply live on the rg's next tick; the default steady width is 2).
	$CLI cli proxy mux cap "$L" >/dev/null 2>&1
	$CLI cli proxy mux width "$L" >/dev/null 2>&1
	sleep 3
	wait_width "$name" "$T" "$L" || echo "$name: engine did not reach ${T}x${L} within 24s — recording the shape it has"
	mux_info "$name" > "$out/$set_name.legs.json"

	# every pinned first hop actually present?
	missing=0
	if [ "$pinned" = yes ]; then
		want=$(cat "$out/$set_name".rg*.target.json | jq -r '.[].forward[0].TpID' | sort -u)
		have=$(active_json "$out/$set_name.legs.json" | jq -r '.[].legs[].transport_id' 2>/dev/null)
		for w in $want; do echo "$have" | grep -q "$w" || { echo "$name: pinned leg $w NOT present"; missing=$((missing + 1)); }; done
	fi

	roles=$(rg_roles "$out/$set_name.legs.json")
	allgroups=${roles%% *}; roles_rest=${roles#* }; groups=${roles_rest%% *}; nstandby=${roles_rest##* }
	aj=$(active_json "$out/$set_name.legs.json")
	# the shape is the ACTIVE tunnels'; the carriers are named from every group,
	# standby included, because a standby tunnel can be promoted mid-set.
	shape=$(echo "$aj" | jq -r '[.[].legs | length] | join("+")')
	tps=$(jq -r '.[].legs[].transport_id' "$out/$set_name.legs.json" 2>/dev/null | sort -u | tr '\n' ' ')
	ports_before=$(echo "$aj" | jq -r '[.[].desc.dst_port] | join(",")')
	desc=$(jq -r '[.[] | "rg\(.desc.dst_port)\(if .tunnel_role then "/" + .tunnel_role else "" end)=[" + ([.legs[] | .tp_type + ">" + .remote_pk[0:8] + "@" + .transport_id[0:8]] | join(";")) + "]"] | join(" ")' "$out/$set_name.legs.json" 2>/dev/null)
	echo "$name: $groups/$T active route group(s) of $allgroups ($nstandby standby), legs per active rg: $shape (want $L each), pinned=$pinned"
	echo "$name: first-hop tps: $tps"
	# Nothing below here may run on a shape that is not the target one: rows from
	# an unpinned or half-built session are indistinguishable from real ones in
	# the TSV, which is exactly how campaign16 recorded 20 invalid rows.
	narrow=$(echo "$shape" | tr '+' '\n' | awk -v l="$L" '$1!=l {bad++} END{print bad+0}')
	[ "$groups" -eq "$T" ] || { abort_set "$set_name" "shape differs from target: $groups active route group(s) of $allgroups ($nstandby standby), asked for $T (groups=$desc)"; port=$((port + 1)); continue; }
	[ "$narrow" -eq 0 ] || { abort_set "$set_name" "shape differs from target: legs per rg $shape, want $L each"; port=$((port + 1)); continue; }
	[ "$missing" -eq 0 ] || { abort_set "$set_name" "$missing pinned leg(s) missing from the realized shape (groups=$desc)"; port=$((port + 1)); continue; }

	warm "$socks" "$name" || echo "$name: probes failing — running the set anyway"
	res_warmup "$socks"
	res_set "$set_name-pre"
	run_set "$set_name" "$socks" "$tps" \
		"exit=$exit_pk local=$local_commit exit_commit=$ec session=$name compose=${T}x${L} tunnels=$T width=$L route_groups=$allgroups active=$groups standby=$nstandby legs_per_rg=$shape pinned=$pinned rg_ports=$ports_before pin_assign=$(echo $assign) groups=$desc sink=$sink"

	res_set "$set_name-post"; res_settle "$set_name"; res_check "$set_name"
	mux_info "$name" > "$out/$set_name.after.json"
	ports_after=$(active_json "$out/$set_name.after.json" | jq -r '[.[].desc.dst_port] | join(",")')
	rm -f "$out/$set_name.after.json"
	printf '# rg dst_ports before=%s after=%s %s\n' "$ports_before" "$ports_after" \
		"$([ "$ports_before" = "$ports_after" ] && echo constant || echo CHANGED)" >> "$out/$set_name.carrier.tsv"
	echo "$name: rg dst_ports $ports_before -> $ports_after"
	$CLI cli proxy mux width 2 >/dev/null 2>&1 # back to the default steady width
	# the rows are already written, so a leftover group here invalidates the NEXT
	# set (its own pre-setup check), not this one — just say so loudly.
	stop_app_clean "$name" || echo "$set_name: dst_ports $rg_left outlived the set — the next set will be invalidated if they persist"
	port=$((port + 1))
done

# The exit-resource gate is the only thing that can fail this script: every set
# that ran is on disk either way. The per-set checks have already run; this is
# the campaign-long RssAnon slope over every reading the run took.
res_slope
[ "$exit_res_fail" = 0 ] || echo "run-compose: an exit-resource check FAILED — see $out/exit-resources.tsv"
exit "$exit_res_fail"

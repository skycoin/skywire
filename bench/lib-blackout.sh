#!/bin/sh
# shellcheck shell=sh
# lib-blackout.sh — capture the proof of a LOCAL receive-loop stall at the
# instant a bench row fails.
#
#   BLACKOUT_HERE=$here; . "$here/lib-blackout.sh"
#   ... blackout_capture "$out" "$set_name" "$row"
#
# The failures the runners record as http=000 are not the exit's: they are this
# visor's single inbound goroutine parked on one route group's readCh, which
# stalls EVERY transport behind it (pkg/router/router_intake.go documents the
# 30 s-per-packet block). The evidence is gone within a minute — the app
# re-dials, the queue drains — so it has to be taken on the failing row itself,
# next to the exit-side snapshot the runners already take.
#
# Three readings, in this order (the goroutines first, because they are the
# thing that clears):
#
#   <set>.row<row>.goroutines-serve.txt  stacks matching serveTransportManager
#                                        — the shared inbound loop
#   <set>.row<row>.goroutines-data.txt   stacks matching handleDataPacket
#                                        — who it is parked on
#   <set>.row<row>.intake.json           `visor state --select diag` .intake:
#                                        route_group_queues gives every group's
#                                        readCh depth/capacity, and the group at
#                                        capacity is the stalled one
#   <set>.row<row>.visorlog.tail         the last 400 lines of the visor log
#
# plus one summary line appended to <set>.recovery.tsv, in the same
# `# exit-on-fail`-style comment form the runners already write:
#
#   # blackout-capture <row>	goroutines=<n>	readch_full=<n>	write_timeout=<n>	rg_full=<desc,...|none>
#
# The counters are whole-file counts of the visor log, which restarts with the
# visor, so they are "since this visor came up" and are read as a delta between
# rows.
#
# Nothing here may abort a run: every step is `|| true` behind a `timeout`, and
# the whole capture is bounded by three BLACKOUT_STEP_S waits (~60 s by
# default) plus local file work. BLACKOUT=0 turns it off.

BLACKOUT=${BLACKOUT:-1}
BLACKOUT_STEP_S=${BLACKOUT_STEP_S:-20}

# blackout_log: the dev visor's current log file. The repo-local log directory
# is rotated (skywire-<stamp>.log), so this takes the newest non-crash file;
# VISOR_LOG names one outright.
blackout_log() {
	if [ -n "${VISOR_LOG:-}" ]; then
		echo "$VISOR_LOG"
		return 0
	fi
	_blh=${BLACKOUT_HERE:-${here:-$(dirname "$0")}}
	# the stamp is in the name, so the last match of the sorted glob is the
	# newest; skywire-crash.log matches the glob and is skipped.
	_bln=""
	for _blf in "$_blh/../local/log/"skywire-*.log; do
		case $_blf in *crash*) continue ;; esac
		[ -f "$_blf" ] && _bln=$_blf
	done
	echo "$_bln"
}

# blackout_capture <out dir> <set name> <row>
blackout_capture() {
	[ "$BLACKOUT" = 1 ] || return 0
	_bo=$1; _bs=$2; _br=$3
	_bp="$_bo/$_bs.row$_br"
	# 1. the inbound loop, and what it is parked on. --full prints the raw
	# stacks; --filter keeps only the goroutines whose stack body matches. The
	# dump comes from the visor's pprof listener when there is one and over the
	# visor RPC (GoroutineDump) when there is not, so no --pprof-addr is needed.
	timeout "$BLACKOUT_STEP_S" $CLI cli visor goroutines --full --filter serveTransportManager \
		-o "$_bp.goroutines-serve.txt" >/dev/null 2>&1 || true
	timeout "$BLACKOUT_STEP_S" $CLI cli visor goroutines --full --filter handleDataPacket \
		-o "$_bp.goroutines-data.txt" >/dev/null 2>&1 || true
	# 2. the router's inbound view: route_group_queues is readCh depth/capacity
	# per group, and a group at capacity is the one the loop is blocked on.
	timeout "$BLACKOUT_STEP_S" $CLI cli visor state --select diag --json 2>/dev/null |
		jq -c '.diag.intake // .intake // {}' > "$_bp.intake.json" 2>/dev/null || true
	[ -s "$_bp.intake.json" ] || echo '{}' > "$_bp.intake.json"
	_bfull=$(jq -r '[.route_group_queues[]? | select((.capacity // 0) > 0 and .queue >= .capacity) |
		.desc + (if (.app // "") != "" then "(" + .app + ")" else "" end)] | join(",")' \
		"$_bp.intake.json" 2>/dev/null)
	[ -n "$_bfull" ] || _bfull=none
	# 3. the visor's own warnings, counted over the whole (per-boot) log.
	_blog=$(blackout_log)
	_brf=0; _bwt=0
	if [ -n "$_blog" ] && [ -f "$_blog" ]; then
		# one pass for both counters — `grep -c` exits 1 on zero matches, which
		# a `|| echo 0` would turn into the two-line string "0\n0".
		_bc=$(timeout 15 awk '/readCh full/ {a++} /write timeout|writeMx|i\/o timeout/ {b++} END {printf "%d %d", a+0, b+0}' "$_blog" 2>/dev/null)
		case $_bc in *' '*) _brf=${_bc%% *}; _bwt=${_bc##* } ;; esac
		tail -400 "$_blog" > "$_bp.visorlog.tail" 2>/dev/null || true
	fi
	# 4. one line beside the exit-on-fail line of the same row.
	_bgr=$(wc -l < "$_bp.goroutines-serve.txt" 2>/dev/null | tr -d ' ')
	[ -n "$_bgr" ] || _bgr=0
	printf '# blackout-capture %s\tgoroutines=%s\treadch_full=%s\twrite_timeout=%s\trg_full=%s\n' \
		"$_br" "$_bgr" "$_brf" "$_bwt" "$_bfull" >> "$_bo/$_bs.recovery.tsv"
	echo "$_bs row $_br: blackout capture — serve-loop stacks=$_bgr lines, readCh-full warnings=$_brf, write timeouts=$_bwt, queues at capacity: $_bfull"
}

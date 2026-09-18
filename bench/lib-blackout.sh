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
#   <set>.row<row>.visorlog.tail         the last 400 lines of the LIVE visor
#                                        log (chosen by mtime, not by name)
#   <set>.row<row>.r.hdr / .r.body       the failed transfer's own answer, when
#                                        the runner set BENCH_FAIL_PREFIX
#
# plus one summary line appended to <set>.recovery.tsv, in the same
# `# exit-on-fail`-style comment form the runners already write:
#
#   # blackout-capture <row>	goroutines=<n>	readch_full=<n>	write_timeout=<n>	write_timeout_other=<n>	rg_full=<desc,...|none>	upload_error=<reason|->
#
# The counters are whole-file counts of the visor log, which restarts with the
# visor, so they are "since this visor came up" and are read as a delta between
# rows. write_timeout counts only the lines that name THIS set's traffic (a
# route group, a leg, writeMx, a transport ID from <set>.legs.json); the rest —
# stcpr dials to unrelated public visors, which time out all day — are
# write_timeout_other and are not evidence of anything.
#
# Nothing here may abort a run: every step is `|| true` behind a `timeout`, and
# the whole capture is bounded by three BLACKOUT_STEP_S waits (~60 s by
# default) plus local file work. BLACKOUT=0 turns it off.

BLACKOUT=${BLACKOUT:-1}
BLACKOUT_STEP_S=${BLACKOUT_STEP_S:-20}

# blackout_log: the dev visor's LIVE log file. The repo-local log directory
# holds the file being written (skywire.log) beside the rotated ones
# (skywire-<stamp>.log), and the name does not say which is which: the sorted
# glob's last match is a FROZEN file, which is what the 2026-09-16 compose set
# captured — row 7 and row 14 came out byte-identical and both ended before
# either row. So the file is chosen by MTIME, over both shapes of name, with
# skywire-crash.log excluded. VISOR_LOG names one outright.
blackout_log() {
	if [ -n "${VISOR_LOG:-}" ]; then
		echo "$VISOR_LOG"
		return 0
	fi
	_blh=${BLACKOUT_HERE:-${here:-$(dirname "$0")}}
	set --
	for _blf in "$_blh/../local/log/skywire.log" "$_blh/../local/log/"skywire-*.log; do
		case $_blf in *crash*) continue ;; esac
		[ -f "$_blf" ] && set -- "$@" "$_blf"
	done
	[ $# -gt 0 ] || return 0
	# shellcheck disable=SC2012 # these are our own log names: no spaces, no newlines
	ls -t "$@" 2>/dev/null | head -1
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
	# 3. the visor's own warnings, counted over the whole (per-boot) log. A write
	#    timeout is counted as EVIDENCE only when the line names something of
	#    this set — a route group, a leg, writeMx, or a transport ID that appears
	#    in <set>.legs.json. Everything else (stcpr dials to public visors time
	#    out constantly: 25 of them were read as proof once) is counted apart as
	#    write_timeout_other.
	_blog=$(blackout_log)
	_bids=$(jq -r '[.. | objects | .transport_id? | select(type == "string")] |
		map([., .[0:8]]) | flatten | unique | join("|")' "$_bo/$_bs.legs.json" 2>/dev/null)
	_brf=0; _bwt=0; _bwo=0
	if [ -n "$_blog" ] && [ -f "$_blog" ]; then
		# one pass for every counter — `grep -c` exits 1 on zero matches, which
		# a `|| echo 0` would turn into a multi-line string.
		# shellcheck disable=SC2016 # $0 below is awk's line, not a shell variable
		_bc=$(timeout 15 awk -v ids="$_bids" '
			BEGIN { n = split(ids, id, "|") }
			/readCh full/ { a++ }
			/write timeout|writeMx|i\/o timeout/ {
				mine = ($0 ~ /writeMx|route[ _]?group|routeGroup|rg[=: ]|leg[=: #]|mux/)
				for (i = 1; !mine && i <= n; i++)
					if (id[i] != "" && index($0, id[i])) mine = 1
				if (mine) b++; else o++
			}
			END { printf "%d %d %d", a+0, b+0, o+0 }' "$_blog" 2>/dev/null)
		case $_bc in
		*' '*' '*)
			_brf=${_bc%% *}; _bcr=${_bc#* }; _bwt=${_bcr%% *}; _bwo=${_bcr##* } ;;
		esac
		tail -400 "$_blog" > "$_bp.visorlog.tail" 2>/dev/null || true
	fi
	# 4. what the failed transfer itself answered, when the runner kept it
	#    (bench.sh BENCH_FAIL_PREFIX). A striped upload's 502 names its own cause.
	_bue=$(tr -d '\r' < "$_bp.r.hdr" 2>/dev/null | awk 'tolower($1) == "x-upload-error:" {sub(/^[^:]*: */, ""); print; exit}')
	# 5. one line beside the exit-on-fail line of the same row.
	_bgr=$(wc -l < "$_bp.goroutines-serve.txt" 2>/dev/null | tr -d ' ')
	[ -n "$_bgr" ] || _bgr=0
	printf '# blackout-capture %s\tgoroutines=%s\treadch_full=%s\twrite_timeout=%s\twrite_timeout_other=%s\trg_full=%s\tupload_error=%s\n' \
		"$_br" "$_bgr" "$_brf" "$_bwt" "$_bwo" "$_bfull" "${_bue:--}" >> "$_bo/$_bs.recovery.tsv"
	_bmsg="$_bs row $_br: blackout capture — serve-loop stacks=$_bgr lines, readCh-full warnings=$_brf, write timeouts=$_bwt (other=$_bwo), queues at capacity: $_bfull"
	[ -n "$_bue" ] && _bmsg="$_bmsg; X-Upload-Error: $_bue"
	echo "$_bmsg"
}

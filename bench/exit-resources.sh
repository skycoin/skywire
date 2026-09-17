#!/bin/sh
# exit-resources.sh — one reading of the EXIT visor's memory and CPU, for the
# before/after gate every deploy has to pass.
#
#   bench/exit-resources.sh <out dir> <label> [exit pk]
#
# The goal text (2026-09-17): "Every deploy records exit RssAnon and CPU before
# and after each set and fails the run if either grows across it." This script
# takes ONE reading and appends it to <out dir>/exit-resources.tsv; the pair is
# scored by bench/exit-resources-check.sh, which owns the thresholds.
#
# Columns (tab separated, a '# ' header line first):
#   ts              reading time, EPOCH SECONDS on the local host (the check
#                   script divides by it, so it is a number, not a stamp)
#   label           whatever the caller named this reading, by convention
#                   <set>-pre, <set>-post and <set>-settled (plus one `warmup`
#                   reading at the head of a run)
#   rss_anon_kb     RssAnon of the exit's skywire process, kB, from
#                   /proc/<MainPID>/status — the number the OOM killer reads
#                   (Go's own runtime.sys_mb misses mmap'd bbolt pages entirely,
#                   which is how the 2026-09-16 cxds OOM hid for a day)
#   cpu_s           (utime+stime)/CLK_TCK of the same process, from
#                   /proc/<MainPID>/stat fields 14 and 15
#   load1           the exit's 1-minute load average
#   idle_cps        CPU seconds per wall second measured over IDLE_WINDOW
#                   seconds INSIDE this reading — the idle rate the check
#                   script's 0.5-core allowance is added to
#   sys_mb          runtime.MemStats.Sys, MB — everything the Go runtime holds
#   heap_alloc_mb   runtime.MemStats.HeapAlloc, MB
#   num_gc          completed GC cycles
#   goroutines      runtime.NumGoroutine()
# The last four are `diag.runtime` (pkg/visor/api_state_diag.go:68 DiagRuntime;
# the struct carries no heap_inuse/heap_idle/heap_released, so sys_mb is the
# only Go-side memory number there is). They are APPENDED at the end so a reader
# of the first six columns is unaffected, and they exist for one job: RssAnon
# climbing WITH sys_mb is Go heap high-water, RssAnon climbing while sys_mb is
# flat is non-Go anonymous memory — the mmap'd-bbolt class that OOM-killed this
# exit seven times on 2026-09-16 — and only the second is a real regression.
#
# They are read in the SAME pty exec as RssAnon, not over
# `visor state --via dmsg://<exit>`: the whole point is comparing the two
# numbers at one instant, and a second RPC over the wire would be a second,
# separately timed sample (and a second round trip per reading).
#
# A reading that could not be taken writes '-' in all value columns and still
# exits 0: a failed measurement is not a failed deploy, and the check script
# reports it as SKIP.
#
# The exit's /bin/sh is dash and `pty exec` runs ONE line there, so the remote
# script below uses no bashisms, no parentheses inside a quoted pattern, and
# prints a single "OK …" line. `pty exec` (never ssh) is how every exit-side
# number in this bench is read.
set -u
CLI=${CLI:-/home/d0mo/go/bin/skywire}
out=${1:-}; label=${2:-}
# The frozen campaign rig's exit, in full — public keys are never truncated.
exit_pk=${3:-${EXIT_PK:-022716fba0f9a5d46ae7b8a4412296847d63d96c763d49068e851e9be7542080b1}}
[ -n "$out" ] && [ -n "$label" ] || {
	echo "usage: bench/exit-resources.sh <out dir> <label> [exit pk]" >&2
	exit 2
}
UNIT=${EXIT_UNIT:-skywire}          # the systemd unit the visor runs as on the exit
IDLE_WINDOW=${IDLE_WINDOW:-5}       # seconds the reading itself samples, for idle_cps
mkdir -p "$out"
f="$out/exit-resources.tsv"
[ -f "$f" ] || printf '# ts\tlabel\trss_anon_kb\tcpu_s\tload1\tidle_cps\tsys_mb\theap_alloc_mb\tnum_gc\tgoroutines\n' > "$f"

# One dash line on the exit: MainPID of the unit, its CPU tick counters twice
# IDLE_WINDOW seconds apart, RssAnon after the second read, load1, CLK_TCK, and
# the visor's own diag.runtime as a compact array. The binary is the unit's own
# (/proc/<pid>/exe), so the reading never depends on what is on the exit's PATH;
# the jq filter carries no single quotes because this whole line is one.
# shellcheck disable=SC2016 # every $ in here is the EXIT's shell, not ours
remote='p=$(systemctl show -p MainPID --value '"$UNIT"'); [ "$p" -gt 0 ] 2>/dev/null || { echo ERR-nomainpid; exit 1; }; h=$(getconf CLK_TCK 2>/dev/null); [ -n "$h" ] || h=100; c1=$(cut -d" " -f14,15 /proc/$p/stat); sleep '"$IDLE_WINDOW"'; c2=$(cut -d" " -f14,15 /proc/$p/stat); r=$(grep RssAnon /proc/$p/status | tr -dc 0-9); l=$(cut -d" " -f1 /proc/loadavg); b=$(readlink /proc/$p/exe 2>/dev/null); [ -x "$b" ] || b=skywire; g=$($b cli visor state --select diag --jq ".diag.runtime|[.sys_mb,.heap_alloc_mb,.num_gc,.goroutines]" 2>/dev/null | tr -d " \n\r"); [ -n "$g" ] || g=-; echo "OK $r $c1 $c2 $l $h $g"'

ts=$(date +%s)
raw=$(timeout "$((IDLE_WINDOW + 70))" $CLI cli pty exec "$exit_pk" --timeout "$((IDLE_WINDOW + 40))s" -- /bin/sh -c "$remote" 2>/dev/null |
	tr -d '\r' | grep '^OK ' | tail -1)
if [ -z "$raw" ]; then
	printf '%s\t%s\t-\t-\t-\t-\t-\t-\t-\t-\n' "$ts" "$label" >> "$f"
	echo "exit-resources $label: reading FAILED (pty exec to $exit_pk) — recorded as unmeasured"
	exit 0
fi
# OK <rss_kb> <utime1> <stime1> <utime2> <stime2> <load1> <clk_tck> <[sys,heap,gc,goroutines]>
line=$(echo "$raw" | awk -v w="$IDLE_WINDOW" '{
	hz = $8 + 0; if (hz <= 0) hz = 100
	cpu1 = ($3 + $4) / hz
	cpu2 = ($5 + $6) / hz
	idle = (w > 0 ? (cpu2 - cpu1) / w : 0); if (idle < 0) idle = 0
	# the diag.runtime array, or "-" when the visor could not be asked: every
	# field stays "-" unless all four arrived and all four are numbers.
	sys = "-"; heap = "-"; ngc = "-"; gor = "-"
	gsub(/[][]/, "", $9)
	n = split($9, g, ",")
	if (n == 4 && g[1] ~ /^[0-9.]+$/ && g[2] ~ /^[0-9.]+$/ && g[3] ~ /^[0-9]+$/ && g[4] ~ /^[0-9]+$/) {
		sys = sprintf("%.1f", g[1]); heap = sprintf("%.1f", g[2]); ngc = g[3]; gor = g[4]
	}
	printf "%d\t%.2f\t%s\t%.3f\t%s\t%s\t%s\t%s", $2, cpu2, $7, idle, sys, heap, ngc, gor
}')
printf '%s\t%s\t%s\n' "$ts" "$label" "$line" >> "$f"
echo "exit-resources $label: RssAnon $(echo "$line" | cut -f1) kB, cpu $(echo "$line" | cut -f2) s, load1 $(echo "$line" | cut -f3), idle $(echo "$line" | cut -f4) core, sys $(echo "$line" | cut -f5) MB, heap $(echo "$line" | cut -f6) MB, gc $(echo "$line" | cut -f7), goroutines $(echo "$line" | cut -f8)"

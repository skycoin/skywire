#!/bin/sh
# exit-resources-check.sh — score one set's exit RssAnon / CPU pair.
#
#   bench/exit-resources-check.sh <out dir> <set>
#
# Reads the <set>-pre and <set>-post rows bench/exit-resources.sh wrote into
# <out dir>/exit-resources.tsv and prints one line:
#
#   EXITRES <set> rss=<pre>-><post> kB d_rss=<n> KiB cpu=<n>s wall=<n>s
#           cps=<n> idle=<n> allow=<n> verdict=PASS|FAIL|SKIP
#
# THRESHOLDS (both documented here because they are the gate, not a detail):
#   RSS_KB   (EXIT_RES_RSS_KB, default 65536 = 64 MiB) — FAIL when RssAnon grew
#            by more than this across the set. 64 MiB is well above the exit's
#            steady sawtooth between two readings minutes apart and well below
#            anything that walks toward the 3.28 GB cgroup ceiling that
#            OOM-killed this exit seven times on 2026-09-16.
#   CORES    (EXIT_RES_CPU_CORES, default 0.5) — FAIL when the CPU seconds the
#            visor burned per wall second ACROSS the set exceeded the idle rate
#            measured just before the set by more than this. The exit is a
#            2-core box, so half a core of extra burn is the point where a set
#            is costing the visor more than it is costing the wire. The
#            allowance is relative to the pre-set idle rate on purpose: a busy
#            exit is not a regression, a set that makes it busier is.
#
# Exit status: 0 on PASS or SKIP, 1 on FAIL. A caller that wants the results
# kept must not stop on this — run-mux.sh / run-compose.sh record the failure
# and exit non-zero only once every set has been measured.
#
# SKIP is a reading that could not be taken (a '-' value, or a missing row):
# a failed pty exec is not a failed deploy.
set -u
out=${1:-}; set_name=${2:-}
[ -n "$out" ] && [ -n "$set_name" ] || {
	echo "usage: bench/exit-resources-check.sh <out dir> <set>" >&2
	exit 2
}
f="$out/exit-resources.tsv"
rss_kb=${EXIT_RES_RSS_KB:-65536}
cores=${EXIT_RES_CPU_CORES:-0.5}
[ -f "$f" ] || { echo "EXITRES $set_name verdict=SKIP (no $f)"; exit 0; }

awk -F'\t' -v s="$set_name" -v lim="$rss_kb" -v cores="$cores" '
	/^#/ { next }
	$2 == s "-pre"  { pts=$1; prss=$3; pcpu=$4; pidle=$6; pre=1 }
	$2 == s "-post" { qts=$1; qrss=$3; qcpu=$4; post=1 }
	END {
		if (!pre || !post) {
			printf "EXITRES %s verdict=SKIP (missing %s row)\n", s, (pre ? "post" : "pre")
			exit 0
		}
		if (prss == "-" || qrss == "-" || pcpu == "-" || qcpu == "-") {
			printf "EXITRES %s verdict=SKIP (a reading could not be taken)\n", s
			exit 0
		}
		drss = qrss - prss
		wall = qts - pts
		cps  = (wall > 0 ? (qcpu - pcpu) / wall : 0)
		idle = (pidle == "-" ? 0 : pidle + 0)
		allow = idle + cores
		v = "PASS"
		why = ""
		if (drss > lim)  { v = "FAIL"; why = why " rss+" int(drss/1024) "MiB>" int(lim/1024) "MiB" }
		if (cps > allow) { v = "FAIL"; why = why " cpu" sprintf("%.2f", cps) ">" sprintf("%.2f", allow) }
		printf "EXITRES %s rss=%s->%s kB d_rss=%d KiB cpu=%.1fs wall=%ds cps=%.2f idle=%.2f allow=%.2f verdict=%s%s\n", \
			s, prss, qrss, drss, qcpu - pcpu, wall, cps, idle, allow, v, why
		exit (v == "FAIL" ? 1 : 0)
	}' "$f"

#!/bin/sh
# exit-resources-check.sh — score one set's exit RssAnon / CPU readings, or the
# whole campaign's RssAnon slope.
#
#   bench/exit-resources-check.sh <out dir> <set>
#   bench/exit-resources-check.sh <out dir> --slope
#
# Reads the rows bench/exit-resources.sh wrote into <out dir>/exit-resources.tsv
# and prints one line:
#
#   EXITRES <set> rss=<pre>-><post> kB d_rss=<n> KiB settled=<kB> d_settled=<KiB>
#           sys=<pre>-><settled> MB cpu=<n>s wall=<n>s cps=<n> idle=<n> allow=<n>
#           verdict=PASS|FAIL|SKIP
#   EXITRES-SLOPE <out dir> slope=<KiB/min> span=<min> n=<readings> verdict=…
#
# WHY THE SET GATE IS NOT `post - pre`: the exit's RssAnon is dominated by Go
# heap high-water, not by anything a set leaks. campaign21 and the dc23fd9ff
# smoke both failed their FIRST set by +66 / +76 MiB and passed every later one,
# and the smoke handed 52 MiB back in 29 idle seconds between two sets — a leak
# does not do that, Go's scavenger does. So the set is scored on what the exit
# still holds AFTER it settles, a warm-up transfer takes the cold-start
# high-water outside the first set, and the thing that actually walks toward the
# cgroup ceiling — a campaign-long upward drift — is gated separately by --slope.
#
# THRESHOLDS (documented here because they are the gate, not a detail):
#   RSS_KB   (EXIT_RES_RSS_KB, default 65536 = 64 MiB) — FAIL when RssAnon at the
#            SETTLED reading is more than this above the set's pre reading. When
#            no <set>-settled row exists (an old run, or EXIT_RES_SETTLE_S=0)
#            the post reading is used instead and the line says settled=absent,
#            which is exactly the pre/post rule this gate started as.
#   SYS_MB   (EXIT_RES_SYS_MB, default 96) — FAIL when the visor's own
#            runtime.MemStats.Sys grew by more than this across the set. This is
#            the discriminator, and it cuts both ways: RssAnon over the bar WITH
#            sys_mb is Go heap high-water, RssAnon over the bar while sys_mb is
#            flat is non-Go anonymous memory — the mmap'd-bbolt class behind the
#            seven OOM kills of 2026-09-16 — and the verdict text names which.
#   CORES    (EXIT_RES_CPU_CORES, default 0.5) — FAIL when the CPU seconds the
#            visor burned per wall second ACROSS the set exceeded the idle rate
#            measured just before the set by more than this. The exit is a
#            2-core box, so half a core of extra burn is the point where a set
#            is costing the visor more than it is costing the wire. The
#            allowance is relative to the pre-set idle rate on purpose: a busy
#            exit is not a regression, a set that makes it busier is.
#   SLOPE    (EXIT_RES_SLOPE_KB_MIN, default 2048 KiB/min, over a span of at
#            least EXIT_RES_SLOPE_SPAN_MIN, default 20 minutes) — the campaign
#            check. A least-squares fit of RssAnon against the reading
#            timestamps of the whole out dir. campaign21 drifted 347 -> 479 MB
#            in 43 minutes, about 3.1 MB/min: no single set of it failed the
#            per-set rule, and that drift is the conversation worth having.
#            A shorter span is SKIP, not PASS — two sets cannot show a trend.
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
	echo "usage: bench/exit-resources-check.sh <out dir> <set>|--slope" >&2
	exit 2
}
f="$out/exit-resources.tsv"
rss_kb=${EXIT_RES_RSS_KB:-65536}
sys_mb=${EXIT_RES_SYS_MB:-96}
cores=${EXIT_RES_CPU_CORES:-0.5}
slope_lim=${EXIT_RES_SLOPE_KB_MIN:-2048}
span_min=${EXIT_RES_SLOPE_SPAN_MIN:-20}

if [ "$set_name" = "--slope" ]; then
	[ -f "$f" ] || { echo "EXITRES-SLOPE $out verdict=SKIP (no $f)"; exit 0; }
	# Least squares of rss_anon_kb against ts over EVERY usable reading in the
	# out dir — warm-up, pre, post and settled alike: the campaign's drift is a
	# property of the run, not of any one set.
	awk -F'\t' -v lim="$slope_lim" -v spanmin="$span_min" -v dir="$out" '
		/^#/ { next }
		$3 == "-" || $3 == "" { next }
		{
			n++; x[n] = $1 + 0; y[n] = $3 + 0; sx += x[n]; sy += y[n]
			if (n == 1 || x[n] < lo) lo = x[n]
			if (n == 1 || x[n] > hi) hi = x[n]
		}
		END {
			span = (n ? (hi - lo) / 60 : 0)
			if (n < 3) {
				printf "EXITRES-SLOPE %s slope=- KiB/min span=%.1f min n=%d verdict=SKIP (fewer than 3 readings)\n", dir, span, n
				exit 0
			}
			mx = sx / n; my = sy / n
			for (i = 1; i <= n; i++) { num += (x[i] - mx) * (y[i] - my); den += (x[i] - mx) * (x[i] - mx) }
			if (den <= 0) {
				printf "EXITRES-SLOPE %s slope=- KiB/min span=%.1f min n=%d verdict=SKIP (readings share one timestamp)\n", dir, span, n
				exit 0
			}
			slope = num / den * 60   # kB per second -> KiB per minute
			if (span < spanmin) {
				printf "EXITRES-SLOPE %s slope=%.0f KiB/min span=%.1f min n=%d verdict=SKIP (span under %d min)\n", dir, slope, span, n, spanmin
				exit 0
			}
			v = (slope > lim ? "FAIL" : "PASS")
			printf "EXITRES-SLOPE %s slope=%.0f KiB/min span=%.1f min n=%d verdict=%s%s\n", dir, slope, span, n, v, \
				(v == "FAIL" ? sprintf(" drift+%.1fMB over the run at %.1f MB/min>%.1f MB/min", slope * span / 1024, slope / 1024, lim / 1024) : "")
			exit (v == "FAIL" ? 1 : 0)
		}' "$f"
	exit $?
fi

[ -f "$f" ] || { echo "EXITRES $set_name verdict=SKIP (no $f)"; exit 0; }

awk -F'\t' -v s="$set_name" -v lim="$rss_kb" -v syslim="$sys_mb" -v cores="$cores" '
	function num(v) { return (v != "" && v != "-" && v ~ /^[0-9.]+$/) }
	/^#/ { next }
	$2 == s "-pre"     { pts=$1; prss=$3; pcpu=$4; pidle=$6; psys=$7; pre=1 }
	$2 == s "-post"    { qts=$1; qrss=$3; qcpu=$4; qsys=$7; post=1 }
	$2 == s "-settled" { zts=$1; zrss=$3; zsys=$7; settled=1 }
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
		# The gated delta is settled - pre; with no settled row it degrades to
		# post - pre, which is what this gate did before the settle wait existed.
		if (settled && zrss != "-" && zrss != "") {
			sstr = zrss; dgate = zrss - prss; dstr = sprintf("%d", dgate); esys = zsys
		} else {
			sstr = "absent"; dgate = drss; dstr = "-"; esys = qsys
		}
		havesys = (num(psys) && num(esys))
		dsys = (havesys ? esys - psys : 0)
		sysstr = (havesys ? sprintf("%s->%s", psys, esys) : "-")
		v = "PASS"
		why = ""
		if (dgate > lim) {
			v = "FAIL"
			why = why " rss+" int(dgate / 1024) "MiB>" int(lim / 1024) "MiB"
			# Which memory grew decides what kind of failure this is.
			if (!havesys)
				why = why " (sys_mb absent: Go heap or not cannot be told apart)"
			else if (dsys * 1024 < dgate / 2)
				why = why sprintf(" (NON-Go anon: sys_mb only +%.0fMB while RssAnon +%dMiB — the mmap'"'"'d/bbolt class, not the heap)", dsys, int(dgate / 1024))
			else
				why = why sprintf(" (Go heap: sys_mb +%.0fMB with it)", dsys)
		}
		if (havesys && dsys > syslim) { v = "FAIL"; why = why sprintf(" sys+%.0fMB>%dMB", dsys, syslim) }
		if (cps > allow) { v = "FAIL"; why = why " cpu" sprintf("%.2f", cps) ">" sprintf("%.2f", allow) }
		printf "EXITRES %s rss=%s->%s kB d_rss=%d KiB settled=%s d_settled=%s sys=%s MB cpu=%.1fs wall=%ds cps=%.2f idle=%.2f allow=%.2f verdict=%s%s\n", \
			s, prss, qrss, drss, sstr, dstr, sysstr, qcpu - pcpu, wall, cps, idle, allow, v, why
		exit (v == "FAIL" ? 1 : 0)
	}' "$f"

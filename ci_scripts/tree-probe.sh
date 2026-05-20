#!/usr/bin/env bash
# ci_scripts/tree-probe.sh — drive cli visor ping tree-stream
# across a hop sweep + render per-cell CSV via cmd/treeprobe.
#
# Purpose: collect the latency / jitter / cache-hit-rate measurement
# matrix Synth's May-11 directive asked for. Each invocation produces
# one CSV per --hops value; the driver names files by hop so the
# downstream measurement harness (Beta's #2726-style assertion stack)
# can ingest them by glob.
#
# Usage:
#   ci_scripts/tree-probe.sh                    # default: hops 1..3, tries 5
#   ci_scripts/tree-probe.sh hops=1,2,3,4 tries=10
#   ci_scripts/tree-probe.sh outdir=/tmp/runX hops=1
#
# Output (one per hop in the sweep):
#   $outdir/tree-probe-hops<N>.ndjson   raw NDJSON capture
#   $outdir/tree-probe-hops<N>.csv      per-cell CSV
#   $outdir/tree-probe-hops<N>.log      treeprobe stderr summary
#
# Exit codes:
#   0  all hops captured + parsed cleanly
#   1  usage error
#   2  cli visor ping tree-stream RPC failed on at least one hop
#   3  treeprobe parser/CSV-writer failed on at least one hop
#
# The driver does NOT clear visor state between hops — successive
# tree-stream invocations may reuse the route cache and produce
# differing skipped_cached counts, which is the cache-warmth signal
# the harness wants surfaced. Operator runs the driver against a
# cold visor when they want a worst-case-first-run measurement.

set -euo pipefail

hops="1,2,3"
tries="5"
outdir="/tmp/tree-probe-$(date -u +%Y%m%dT%H%M%SZ)"
treeprobe_bin="${TREEPROBE_BIN:-treeprobe}"

for arg in "$@"; do
    case "$arg" in
        hops=*)   hops="${arg#hops=}" ;;
        tries=*)  tries="${arg#tries=}" ;;
        outdir=*) outdir="${arg#outdir=}" ;;
        -h|--help)
            sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'
            exit 0
            ;;
        *) echo "unknown arg: $arg" >&2; exit 1 ;;
    esac
done

case "$tries" in ''|*[!0-9]*) echo "tries must be int: $tries" >&2; exit 1 ;; esac
mkdir -p "$outdir"

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

# Verify treeprobe binary is on PATH.
if ! command -v "$treeprobe_bin" >/dev/null 2>&1; then
    echo "treeprobe binary not found (PATH or TREEPROBE_BIN). Build: go build -o ./bin/treeprobe ./cmd/treeprobe" >&2
    exit 1
fi

# Verify skywire cli has the tree-stream subcommand (post-#2732).
if ! skywire cli visor ping tree-stream --help >/dev/null 2>&1; then
    echo "cli visor ping tree-stream not available; need binary post-PR-#2732" >&2
    exit 2
fi

log "starting sweep: hops=$hops tries=$tries outdir=$outdir"

rpc_fail=0
parse_fail=0

IFS=',' read -r -a hops_arr <<< "$hops"
for h in "${hops_arr[@]}"; do
    case "$h" in ''|*[!0-9]*) echo "hops entry must be int: $h" >&2; exit 1 ;; esac
    log "hop=$h: running tree-stream..."
    ndjson="$outdir/tree-probe-hops${h}.ndjson"
    csv="$outdir/tree-probe-hops${h}.csv"
    sumlog="$outdir/tree-probe-hops${h}.log"

    if ! skywire cli visor ping tree-stream --hops "$h" --tries "$tries" > "$ndjson" 2> "$sumlog"; then
        log "hop=$h: tree-stream FAILED (see $sumlog)"
        rpc_fail=1
        continue
    fi

    nlines=$(wc -l < "$ndjson")
    log "hop=$h: captured $nlines NDJSON lines"

    if ! "$treeprobe_bin" < "$ndjson" > "$csv" 2>> "$sumlog"; then
        log "hop=$h: treeprobe parse FAILED (see $sumlog)"
        parse_fail=1
        continue
    fi

    nrows=$(($(wc -l < "$csv") - 1))  # subtract header row
    log "hop=$h: emitted $nrows CSV rows → $csv"
done

log "sweep complete: $(ls "$outdir"/tree-probe-hops*.csv 2>/dev/null | wc -l) CSV files in $outdir"

if [[ $rpc_fail -ne 0 ]]; then
    exit 2
fi
if [[ $parse_fail -ne 0 ]]; then
    exit 3
fi
exit 0

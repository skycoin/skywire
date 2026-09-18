#!/bin/sh
# dry-stub-bench.sh — a fake bench/bench.sh for SWEEP_DRY=1.
#
#   BENCH=bench/dry-stub-bench.sh SWEEP_DRY_STATE=<dir> bench/run-variants.sh ...
#
# Emits the same eight-column row bench/bench.sh emits — label, dir, bytes,
# speed_Bps, http, got, time_s, hash_ok — but the speed is PRICED FROM THE KNOBS
# the stub CLI is currently holding (bench/dry-stub-cli.sh), so an A/B pair in a
# dry run moves exactly as far apart as the knob under test is worth. That is
# what makes the interleaving and the ratio arithmetic testable without a rig.
#
#   DRY_BASE       base rate in B/s (5e6)
#   DRY_EFFECTS    "<knob>=<value>:<fraction> …" — each knob sitting at that
#                  exact value multiplies the rate by (1 + fraction). Default:
#                  upload.concurrency=8 is worth +30 %, leg.starve_ratio=12 is
#                  worth -20 %.
#   DRY_NOISE      peak relative noise (0.04). Deterministic: a counter in the
#                  state dir drives it, so a dry run is reproducible and a
#                  ratio computed from it is exact.
#   DRY_ROW_SLEEP  seconds a row pretends to take (0), so a self-test can
#                  interrupt a run mid-flight.
#   DRY_FAIL_EVERY hash_ok=0 on every Nth row (0 = never), to exercise the
#                  hash_ok column of the verdict.
set -u
_socks=$1; _sink=$2; n=$3; dir=$4; label=${5:-}
state=${SWEEP_DRY_STATE:-${TMPDIR:-/tmp}/sweep-dry}
mkdir -p "$state"
base=${DRY_BASE:-5000000}
noise=${DRY_NOISE:-0.04}
effects=${DRY_EFFECTS:-upload.concurrency=8:0.30 leg.starve_ratio=12:-0.20}
fail_every=${DRY_FAIL_EVERY:-0}

i=$(( $(cat "$state/counter" 2>/dev/null || echo 0) + 1 ))
echo "$i" > "$state/counter"
echo $(( $(cat "$state/bytes" 2>/dev/null || echo 0) + n )) > "$state/bytes"

knobs=$(cat "$state/local.knobs" "$state/exit.knobs" "$state/proxy.knobs" 2>/dev/null | sort -u)
rate=$(echo "$effects" | tr ' ' '\n' | awk -v base="$base" -v i="$i" -v noise="$noise" -v knobs="$knobs" '
	BEGIN {
		nk = split(knobs, kk, "\n")
		for (j = 1; j <= nk; j++) held[kk[j]] = 1
		mult = 1
	}
	NF {
		split($0, p, ":")
		if (p[1] in held) mult += p[2] + 0
	}
	END {
		# a deterministic zero-mean wobble, so a dry run reproduces exactly
		w = ((i * 7919) % 13) / 12 - 0.5
		printf "%d", base * mult * (1 + 2 * noise * w)
	}')
[ "${DRY_ROW_SLEEP:-0}" = 0 ] || sleep "$DRY_ROW_SLEEP"
secs=$(awk -v n="$n" -v r="$rate" 'BEGIN {printf "%.3f", (r > 0) ? n / r : 0}')
ok=1
[ "$fail_every" -gt 0 ] && [ $((i % fail_every)) -eq 0 ] && ok=0
printf '%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n' "$label" "$dir" "$n" "$rate" 200 "$n" "$secs" "$ok"

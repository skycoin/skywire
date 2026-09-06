#!/usr/bin/env bash
# run-rig.sh — the routing-policy measurement rig.
#
# Ties the three pieces together for a reproducible per-preset run:
#   1. install a preset on the carrying app (no restart),
#   2. drive STEADY controlled load so throughput deltas attribute to the
#      policy, not peer noise, and
#   3. sample the live per-leg telemetry to NDJSON while it runs.
#
# The NDJSON is rendered by mux-telemetry-chart.html (per-leg bandwidth + RTT,
# lifecycle events as markers). See README.md for the record shapes.
#
# Controlled far-end (gate 3): the load the harness measures MUST ride the
# policy app's route group. Point LOAD_URL at a steady sink you control that is
# reachable via the proxy's EXIT — e.g. a /dev/zero HTTP server on the exit
# visor's localhost, or a stable high-bandwidth file — so the far-end is never
# the bottleneck and throughput deltas attribute to the policy, not peer noise.
#   Exit-side sink (on the controlled exit visor):
#     while true; do nc -lp 8888 </dev/zero; done   # or an http /dev/zero handler
#
# (`skywire cli visor ping bandwidth <PK>` / `mux-bw <PK>` are a COMPLEMENTARY
#  router-level baseline to a controlled visor — steady send/receive, per-leg
#  telemetry — but they use their own ping/mux routes, NOT the policy app's
#  route group, so they are measured separately, not via this harness.)
#
# Usage:
#   LOAD_URL=http://host/big.bin ./run-rig.sh rotating-bw skysocks-client 300 2
#
# Args: <preset> [app=skysocks-client] [duration_s=300] [min_hops=0]
set -o pipefail
cd "$(dirname "$0")/../../../.." || exit 1 # repo root

PRESET="${1:?usage: run-rig.sh <preset> [app] [duration_s] [min_hops]}"
APP="${2:-skysocks-client}"
DUR="${3:-300}"
MINHOPS="${4:-0}"
CLI="${SKYWIRE_CLI:-./skywire cli}"
OUT="${OUT:-run-${PRESET}-$(printf %s "$DUR")s.ndjson}"

echo ">>> rig: preset=$PRESET app=$APP duration=${DUR}s min_hops=$MINHOPS out=$OUT"

# 1. (Re)start the app under the preset. min_hops>1 forces multi-hop so a mux
#    group forms even to a directly-connected peer (needed to exercise the
#    membership/size dimensions); min_hops=0 leaves the operator's floor.
startflags=(--routing-policy "preset:$PRESET")
[ "$MINHOPS" -gt 0 ] && startflags+=(--min-hops "$MINHOPS")
$CLI proxy start -n "$APP" "${startflags[@]}" >/dev/null 2>&1
sleep 20
$CLI proxy status 2>&1 | sed -n "/Name: $APP\$/,/Route:/p" | grep -E "Status|Route" | head -3

# 2. Steady controlled load in the background for the whole window. This MUST
#    ride the policy app's route group (i.e. go through the proxy), so the
#    harness samples the loaded legs.
if [ -n "$LOAD_URL" ]; then
	echo ">>> steady load: sustained fetch of $LOAD_URL through :1080"
	( timeout "$DUR" curl -sS --max-time "$DUR" -x socks5h://127.0.0.1:1080 -o /dev/null "$LOAD_URL" >/dev/null 2>&1 ) &
	LOADPID=$!
else
	echo ">>> no LOAD_URL set — sampling idle (leg formation/rotation only, no throughput)"
	LOADPID=""
fi

# 3. Sample per-leg telemetry to NDJSON for the window.
$CLI proxy mux info -n "$APP" --ndjson "$OUT" --watch 1s --duration "${DUR}s"

[ -n "$LOADPID" ] && kill "$LOADPID" 2>/dev/null
echo ">>> done. $(wc -l <"$OUT") records → $OUT"
echo ">>> open docs/examples/routing-policies/telemetry/mux-telemetry-chart.html and load $OUT"

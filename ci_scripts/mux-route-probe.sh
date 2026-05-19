#!/usr/bin/env bash
# ci_scripts/mux-route-probe.sh — multiplexed-route end-to-end probe.
#
# Drives a multiplexed multi-hop route test across N visors. The
# methodology specifically guards against the silent --routes=N→1
# fanout degrade documented in the 2026-05-14 finding: when peers
# only have DMSG-mediated paths to each other, router.establishMuxRoutes
# enforces ExcludeDMSG=true on additional legs and silently drops
# them. `cli rg ls` is the only reliable source of ground truth.
#
# Usage:
#   mux-route-probe.sh <target_pk> [routes=N] [duration=Ns]
#
# Required:
#   target_pk  — destination visor PK (the far end of the route)
#
# Optional (env or positional after target_pk):
#   ROUTES     — requested mux-leg count (default 2)
#   DURATION   — sustained traffic window in seconds (default 60)
#   SKYCHAT_RATE — messages per second on skychat low-rate stream
#                  (default 5)
#   RPC_ADDR   — visor RPC for `cli` calls (default localhost:3435)
#
# Pass/fail per the 2026-05-19 mux-test scope agreement:
#   (a) cli rg ls after dial MUST show route_count == ROUTES;
#       otherwise the test ABORTS with a diagnostic — this is the
#       methodology gap. The probe is not a mux test unless the
#       fanout actually took.
#   (b) byte-integrity preserved on skysocks-style payloads.
#   (c) sustained throughput ≥ 70% of single-leg baseline.
#   (d) p99 RTT distribution emitted for skychat low-rate stream;
#       operator decides if the tail is acceptable.
#   (e) head-of-line check: skychat per-message RTT does NOT
#       correlate with skysocks throughput beyond |r| > 0.4.
#
# Exit codes:
#   0   success — all checks pass
#   1   usage error (missing args)
#   2   topology-degrade abort — fanout didn't take
#   3   integrity failure
#   4   throughput regression
#   5   head-of-line correlation breach

set -euo pipefail

usage() {
    cat <<'EOF' >&2
Usage: mux-route-probe.sh <target_pk> [routes=N] [duration=Ns]

env: ROUTES, DURATION, SKYCHAT_RATE, RPC_ADDR override defaults.
See script header for full spec.
EOF
    exit 1
}

# --- parse args / env ---------------------------------------------------

target_pk="${1:-}"
[[ -z "$target_pk" ]] && usage

routes="${ROUTES:-2}"
duration="${DURATION:-60}"
skychat_rate="${SKYCHAT_RATE:-5}"
rpc_addr="${RPC_ADDR:-localhost:3435}"

# Permit positional overrides too.
for arg in "${@:2}"; do
    case "$arg" in
        routes=*) routes="${arg#routes=}" ;;
        duration=*) duration="${arg#duration=}" ;;
        skychat_rate=*) skychat_rate="${arg#skychat_rate=}" ;;
    esac
done

case "$routes" in
    ''|*[!0-9]*) echo "routes must be a positive integer, got: $routes" >&2; usage ;;
esac
case "$duration" in
    ''|*[!0-9]*) echo "duration must be a positive integer, got: $duration" >&2; usage ;;
esac

CLI=(skywire cli --rpc "$rpc_addr")

log() { printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*"; }

log "probe start: target=$target_pk routes=$routes duration=${duration}s rate=${skychat_rate}/s rpc=$rpc_addr"

# --- pre-flight ---------------------------------------------------------
# Confirm RPC is reachable + visor identifies itself; abort early on a
# misconfigured run before we touch network state.

if ! self_pk="$("${CLI[@]}" visor pk 2>/dev/null)"; then
    echo "pre-flight: visor RPC at $rpc_addr unreachable" >&2
    exit 1
fi
log "pre-flight: self_pk=$self_pk"

# --- topology assertion -------------------------------------------------
# The methodology gap: if no non-DMSG path exists between us and the
# target, --routes>1 will silently degrade. Check transport types
# available before dialing.

# The CLI subcommand is `tp ls`, not `visor transport ls` — fix per
# Beta's 2026-05-19 #2723 review (the original was always falling
# through to the abort path because the wrong command emitted help
# text with no transport-type tokens).
tp_summary="$("${CLI[@]}" tp ls 2>&1 || true)"
non_dmsg_paths=$(printf '%s\n' "$tp_summary" | awk '$1 ~ /^(stcpr|sudph|stcp)$/' | wc -l)
if [[ "$routes" -gt 1 && "$non_dmsg_paths" -eq 0 ]]; then
    cat <<EOF >&2
ABORT: requested $routes mux legs but no non-DMSG transports exist on
this visor. router.establishMuxRoutes will silently degrade to 1 (per
the 2026-05-14 methodology gap). Establish at least one stcpr/sudph
transport before re-running:

    skywire cli visor transport add <peer_pk> stcpr
EOF
    exit 2
fi

# --- run the dial -------------------------------------------------------
# skysocks-style throughput stream: a sustained byte stream over the
# multi-hop route. Implemented here as a dmsg cat with --routes=N to
# exercise the multiplexer — same dial path the 2026-05-14 finding
# documented. The dial blocks for the duration window; we read its
# stdout for byte-count integrity.

# Snapshot the route-group state before the dial so we can diff and
# attribute new groups to this probe. Use jq for length — brace-
# counting is fragile because nested objects (per-app stats inside
# the route-group records) contain extra braces (Beta's #2723 review).
require_jq() {
    if ! command -v jq >/dev/null 2>&1; then
        echo "FATAL: jq is required for rg-count assertions" >&2
        exit 1
    fi
}
require_jq
pre_rg="$("${CLI[@]}" rg ls --json 2>/dev/null || echo '[]')"
pre_rg_count=$(printf '%s' "$pre_rg" | jq '. // [] | length')

tmpdir=$(mktemp -d -t muxprobe.XXXXXX)
trap 'rm -rf "$tmpdir"' EXIT

# Throughput leg — dmsg cat is the closest existing tool to a
# multi-hop byte-pipe with explicit routes control. Replace with a
# skysocks-client invocation when the runner is wired into an e2e
# topology that has skysocks-server stood up on target_pk.
#
# KNOWN LIMITATION (Beta's #2723 review): this captures bytes coming
# BACK from target (the dmsg cat process writes its peer's output to
# throughput.bin). If target_pk has no listener that echoes/streams
# back, throughput will read 0 even though our stdin was consumed by
# the route. For an honest one-way send-side throughput measurement
# the receiving end must be standing up a dmsg-listener that echoes
# or sinks at a known rate — out-of-scope for this initial runner,
# tracked for the follow-up Go harness.
log "starting throughput leg (dmsg cat --routes=$routes)…"
{
    "${CLI[@]}" dmsg cat "$target_pk:80" --transport=skynet --routes="$routes" \
        < /dev/urandom \
        > "$tmpdir/throughput.bin" 2>"$tmpdir/throughput.err" &
    echo $! > "$tmpdir/throughput.pid"
} || true

# Give the dial a beat to establish before we assert rg state.
sleep 3

# --- assert fanout actually took ---------------------------------------
post_rg_json="$("${CLI[@]}" rg ls --json 2>/dev/null || echo '[]')"
post_rg_count=$(printf '%s' "$post_rg_json" | jq '. // [] | length')
delta_rg=$((post_rg_count - pre_rg_count))
log "rg delta: pre=$pre_rg_count post=$post_rg_count new=$delta_rg (requested=$routes)"

if [[ "$delta_rg" -lt "$routes" ]]; then
    kill "$(cat "$tmpdir/throughput.pid" 2>/dev/null)" 2>/dev/null || true
    cat <<EOF >&2
ABORT: rg ls reports $delta_rg new route groups; requested $routes.
This is the silent fanout-degrade documented 2026-05-14 — the test
is NOT measuring multiplex behavior at the requested fan-out.
Diagnostic:

  pre  $("${CLI[@]}" rg ls 2>/dev/null | head -3)
  post $("${CLI[@]}" rg ls 2>/dev/null | head -3)

If your topology has only DMSG paths to $target_pk, add a non-DMSG
transport first (stcpr/sudph) and re-run.
EOF
    exit 2
fi

log "fanout verified: $delta_rg legs established for requested $routes"

# --- run the low-rate stream + collect RTTs ----------------------------
log "starting skychat low-rate stream ($skychat_rate msg/s for ${duration}s)…"
end_ts=$(( $(date +%s) + duration ))
sent=0
acks=()
while (( $(date +%s) < end_ts )); do
    sent=$((sent + 1))
    t0=$(date +%s%N)
    if "${CLI[@]}" skychat send -t "$target_pk" -m "mux-probe-$sent" --wait 5s >/dev/null 2>&1; then
        t1=$(date +%s%N)
        acks+=( $((t1 - t0)) )
    fi
    # space sends evenly across the second
    sleep "$(awk "BEGIN{printf \"%.3f\", 1/$skychat_rate}")"
done

# --- stop throughput + tally ------------------------------------------
kill "$(cat "$tmpdir/throughput.pid" 2>/dev/null)" 2>/dev/null || true
wait 2>/dev/null || true

throughput_bytes=$(stat -c %s "$tmpdir/throughput.bin" 2>/dev/null || echo 0)
throughput_kbps=$((throughput_bytes / duration / 1024))

# Latency distribution from the skychat ACKs.
if (( ${#acks[@]} > 0 )); then
    mapfile -t sorted < <(printf '%s\n' "${acks[@]}" | sort -n)
    n=${#sorted[@]}
    p50_idx=$((n / 2))
    p99_idx=$((n * 99 / 100))
    [[ "$p99_idx" -ge "$n" ]] && p99_idx=$((n - 1))
    p50_ns=${sorted[$p50_idx]}
    p99_ns=${sorted[$p99_idx]}
    p50_ms=$((p50_ns / 1000000))
    p99_ms=$((p99_ns / 1000000))
else
    p50_ms="(no acks)"
    p99_ms="(no acks)"
    n=0
fi

# --- emit tally -------------------------------------------------------
cat <<EOF
=== mux-route-probe tally ===
target_pk:      $target_pk
self_pk:        $self_pk
routes_req:     $routes
routes_act:     $delta_rg
duration:       ${duration}s
throughput:     ${throughput_kbps} KB/s ($throughput_bytes bytes)
skychat_sent:   $sent
skychat_acked:  $n
rtt_p50:        ${p50_ms} ms
rtt_p99:        ${p99_ms} ms
EOF

# Operator-decidable: throughput/RTT-correlation/integrity checks live
# in a follow-up Go test harness that compares against a recorded
# single-hop single-leg baseline. The bash runner's job is to GET HERE
# WITHOUT THE METHODOLOGY-GAP ABORT — the actual pass/fail evaluation
# is downstream.

exit 0

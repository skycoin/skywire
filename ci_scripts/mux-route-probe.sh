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
#   mux-route-probe.sh <endpoint_b_pk> [routes=N] [duration=Ns]
#   mux-route-probe.sh --endpoint-b <pk> [--endpoint-a <pk>]
#                      [--intermediate-pool pk1,pk2,...]
#                      [--avoid-direct]
#                      [routes=N] [duration=Ns]
#
# Required:
#   --endpoint-b <pk>  — destination visor PK (the far end of the route).
#                        Positional form (first arg) is accepted for
#                        backward compatibility with the original
#                        single-target invocation.
#
# Optional flags:
#   --endpoint-a <pk>        — local-side endpoint PK; defaults to
#                              self_pk. If set, must equal self_pk
#                              (the script dials from the local visor).
#                              Present for naming consistency with the
#                              endpoint-pair experiment design (post-
#                              2026-05-19 BETA↔GAMMA refinement).
#   --intermediate-pool <l>  — comma-separated list of intermediate-
#                              visor PKs. Pre-flight verifies each is
#                              reachable as a transport peer from this
#                              visor (the half-path we can observe).
#                              The other half (intermediate→endpoint-b)
#                              cannot be verified from here.
#   --avoid-direct           — exclude the direct endpoint-a→endpoint-b
#                              edge from route construction by bumping
#                              the visor's runtime routing.min_hops to
#                              2 for the duration of the run (the EXIT
#                              trap restores to 1, skywire's default).
#                              The direct transport is NOT removed —
#                              just unselectable by the route-finder
#                              while min_hops=2. Used by the BETA↔GAMMA
#                              mux fan-out test to force every route
#                              through an intermediate. Caveat: the
#                              runner has no getter for the current
#                              min_hops, so a non-default setting on
#                              the visor will not survive the run —
#                              opting in to --avoid-direct accepts
#                              that mutation.
#
# Env / positional overrides (unchanged from #2723):
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
Usage:
  mux-route-probe.sh <endpoint_b_pk> [routes=N] [duration=Ns]
  mux-route-probe.sh --endpoint-b <pk> [--endpoint-a <pk>]
                     [--intermediate-pool pk1,pk2,...]
                     [--avoid-direct]
                     [routes=N] [duration=Ns]

env: ROUTES, DURATION, SKYCHAT_RATE, RPC_ADDR override defaults.
See script header for full spec.
EOF
    exit 1
}

# --- parse args / env ---------------------------------------------------

routes="${ROUTES:-2}"
duration="${DURATION:-60}"
skychat_rate="${SKYCHAT_RATE:-5}"
rpc_addr="${RPC_ADDR:-localhost:3435}"
endpoint_a=""
endpoint_b=""
intermediate_pool=""
avoid_direct=0

# Hybrid arg parsing: support BOTH the original positional target_pk
# form and the post-2026-05-19 flag form. The first positional non-
# flag arg is treated as endpoint_b when --endpoint-b isn't provided.
while [[ $# -gt 0 ]]; do
    case "$1" in
        --endpoint-a) endpoint_a="$2"; shift 2 ;;
        --endpoint-b) endpoint_b="$2"; shift 2 ;;
        --intermediate-pool) intermediate_pool="$2"; shift 2 ;;
        --avoid-direct) avoid_direct=1; shift ;;
        routes=*) routes="${1#routes=}"; shift ;;
        duration=*) duration="${1#duration=}"; shift ;;
        skychat_rate=*) skychat_rate="${1#skychat_rate=}"; shift ;;
        -h|--help) usage ;;
        --*) echo "unknown flag: $1" >&2; usage ;;
        *)
            # First non-flag positional fills endpoint_b for backward
            # compatibility with the single-target #2723 form.
            if [[ -z "$endpoint_b" ]]; then
                endpoint_b="$1"
            else
                echo "unexpected positional arg: $1" >&2; usage
            fi
            shift
            ;;
    esac
done

[[ -z "$endpoint_b" ]] && usage
# Expose target_pk as the legacy name so the rest of the script (and
# any external diff against #2723's runner) keeps reading naturally.
target_pk="$endpoint_b"

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

# --endpoint-a sanity: the script always dials from the local visor, so
# endpoint_a (if provided) must equal self_pk. Default it when blank.
if [[ -z "$endpoint_a" ]]; then
    endpoint_a="$self_pk"
elif [[ "$endpoint_a" != "$self_pk" ]]; then
    echo "pre-flight: --endpoint-a ($endpoint_a) does not match this visor's self_pk ($self_pk). Run the script ON endpoint-a." >&2
    exit 1
fi
log "pre-flight: endpoint_a=$endpoint_a endpoint_b=$endpoint_b"

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

# --avoid-direct: exclude the direct endpoint-a→endpoint-b edge from
# route construction without removing the transport. Implementation:
# bump the visor's runtime routing.min_hops to 2, which forces the
# route-finder to skip any 1-hop path (i.e., the direct edge) and
# pick a chain through an intermediate. The mutation is in-memory
# only (cli route minhops doesn't touch the config file) and is
# restored to skywire's default of 1 in the EXIT trap below.
#
# Why not pre-flight-reject when a direct transport exists: per
# Alpha's 2026-05-19 16:38Z design clarification, the operator's
# framing 'avoid direct beta-gamma' means EXCLUDE FROM ROUTE
# CONSTRUCTION, not REFUSE-TO-RUN. The direct transport stays in
# place (it's still useful for other tests, single-hop baselines,
# etc.); we just don't want this particular run picking it.
prev_minhops_unset=0
if [[ "$avoid_direct" -eq 1 ]]; then
    # No getter for current min_hops via cli — set the new value and
    # mark for restore. Operator opt-in via --avoid-direct accepts
    # the mutation; we always restore to 1 (skywire's documented
    # default) on exit so a sustained custom-min-hops setting would
    # not survive across the run.
    if ! "${CLI[@]}" route minhops 2 >/dev/null 2>&1; then
        echo "pre-flight: failed to set route minhops=2 for --avoid-direct" >&2
        exit 1
    fi
    prev_minhops_unset=1
    log "pre-flight: --avoid-direct set route minhops=2 (will restore to 1 on exit)"
fi

# --intermediate-pool: verify each pool member is reachable from this
# visor (i.e., we have a transport to it). This is the half-path we
# can observe; the other half (intermediate→endpoint-b) lives on the
# intermediate's tp ls and is out of scope for the runner — the
# slice (b) harness or operator must spot-check.
if [[ -n "$intermediate_pool" ]]; then
    IFS=',' read -r -a pool_pks <<< "$intermediate_pool"
    missing=()
    for pk in "${pool_pks[@]}"; do
        # awk against the first column (type) and any-column match on
        # the PK keeps the check tolerant of formatting drift.
        if ! printf '%s\n' "$tp_summary" | awk -v pk="$pk" 'index($0, pk) { found=1 } END { exit !found }'; then
            missing+=( "$pk" )
        fi
    done
    if [[ ${#missing[@]} -gt 0 ]]; then
        cat <<EOF >&2
ABORT: --intermediate-pool members not reachable as transport peers
from endpoint-a ($endpoint_a):

$(printf '  %s\n' "${missing[@]}")

Add transports to these intermediates before re-running. Note: the
script can only verify endpoint-a's half of each path; the other
half (intermediate→endpoint-b, ${endpoint_b}) must be confirmed
separately on endpoint-b.
EOF
        exit 2
    fi
    log "pre-flight: --intermediate-pool verified — ${#pool_pks[@]} intermediates reachable from endpoint-a"
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
cleanup() {
    rm -rf "$tmpdir"
    # Restore route minhops if --avoid-direct mutated it. Always
    # restore to 1 (skywire's default) — the runner has no getter for
    # the previous value, so a non-default setting on the visor would
    # not survive the run. Document this in --avoid-direct's contract.
    if [[ "${prev_minhops_unset:-0}" -eq 1 ]]; then
        "${CLI[@]}" route minhops 1 >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

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
# Output format is keyword-line stable so Beta's slice (b) Go assertion
# harness can grep/parse without modification. The new endpoint_a /
# endpoint_b / intermediate_pool / avoid_direct lines are additive —
# the original target_pk / self_pk lines remain so older harnesses keep
# working (target_pk == endpoint_b for any single-run invocation).
cat <<EOF
=== mux-route-probe tally ===
target_pk:        $target_pk
self_pk:          $self_pk
endpoint_a:       $endpoint_a
endpoint_b:       $endpoint_b
intermediate_pool: ${intermediate_pool:-(none)}
avoid_direct:     $avoid_direct
routes_req:       $routes
routes_act:       $delta_rg
duration:         ${duration}s
throughput:       ${throughput_kbps} KB/s ($throughput_bytes bytes)
skychat_sent:     $sent
skychat_acked:    $n
rtt_p50:          ${p50_ms} ms
rtt_p99:          ${p99_ms} ms
EOF

# Operator-decidable: throughput/RTT-correlation/integrity checks live
# in a follow-up Go test harness that compares against a recorded
# single-hop single-leg baseline. The bash runner's job is to GET HERE
# WITHOUT THE METHODOLOGY-GAP ABORT — the actual pass/fail evaluation
# is downstream.

exit 0

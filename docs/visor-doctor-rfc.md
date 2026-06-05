# `cli visor doctor` — proactive health checks (RFC)

## Status

Draft. Extends the existing `skywire cli visor doctor` (currently a rollup
of Summary RPC + skychat /status). This RFC proposes a second mode that
actively probes transport-type reachability, AR registration freshness,
stale-TPD detection, dmsg-server reachability, RSN reachability, and
config-sanity checks — each producing a structured finding so the
operator's alerting pipeline can act on it without parsing prose.

## Motivation

Over the past week three operator-visible bugs all turned out to be
diagnosable signals the visor itself already exposed — but only on
explicit query:

1. **Stale AR sudph registration** (PR #3003). Visor restarts → AR keeps
   serving the old random port → peers dial a closed socket → handshake
   times out indefinitely. The visor knew its current port was 58674 the
   whole time; the AR was serving 39726.
2. **Stale TPD null-PK orphan transports** (Beta's network-wide finding;
   PR #2970/#2971). Route-finder builds routes through ghost transports;
   RSN can't dial them at port 136. The TPD knew it had 3218 null-PK
   records; nobody was watching for them.
3. **RSN dmsg unreachability** (Alpha's diagnosis; led to PR #3006). RSN
   advertises 8 delegated dmsg servers via `/health-over-dmsg`, but every
   one fails with i/o deadline. The /health endpoint and the dmsg-ping
   results disagreed for hours; no alarm fired.

In every case the failure mode was **diagnosable in <1s of probing**, but
the diagnosis happened reactively (after a campaign measurement failed)
rather than continuously. The new doctor mode runs the probes proactively
and surfaces findings in a single structured report.

## Out of scope

- Continuous monitoring as a background daemon. This is a CLI invocation;
  operators (or hypervisor dashboards) drive cadence.
- Auto-remediation. Findings carry a suggested next step (in `next_step`),
  but doctor never restarts the visor or rewrites config on its own.

## Command shape

```
skywire cli visor doctor [--check name1,name2,...] [--probe-timeout 3s] [--json] [--explain]
```

| Flag                 | Purpose                                                                                            |
| -------------------- | -------------------------------------------------------------------------------------------------- |
| `--check`            | Comma-separated subset to run (default = all). Names: `summary`, `skychat`, `transports`, `ar-self`, `tpd-self`, `dmsg-servers`, `rsn`, `config`, `routing-policy`. |
| `--probe-timeout`    | Per-probe deadline (default `3s`).                                                                 |
| `--json`             | Structured output for alerting pipelines.                                                          |
| `--explain`          | For each YELLOW/RED finding, print the underlying probe data (not just the verdict).               |

Exit code mirrors the verdict: 0 GREEN, 1 YELLOW, 2 RED (unchanged from
today). The verdict is the max severity across all findings.

## Checks

Each check is independent. A timeout in one doesn't abort the rest —
each emits a `timeout` finding and the report continues.

### 1. `summary` (existing)

Composes the existing `Summary` RPC. Reachable / ready / uptime /
transport counts / service health. Already implemented; documented here
so the check list is complete.

### 2. `skychat` (existing)

Best-effort probe to the local skychat /status endpoint. Already
implemented.

### 3. `transports` (new)

For each transport type (`STCPR`, `SUDPH`, `DMSG`), pick one or two
canary peers — by default the configured `transport_setup_nodes`, or
operator-provided via `--probe-peer pk@type`. For each (peer, type) pair:

- Attempt to dial through `transport.Manager.CreateTransport(peer, type)`.
- Record: handshake duration, success/failure, error category
  (timeout, NAT-blocked, peer-refused).
- Tear down the transport (don't leave probe TPs lingering).

Finding levels:
- `green` — at least one transport type per direction succeeded under
  `--probe-timeout`.
- `yellow` — STCPR works but SUDPH/DMSG handshake-timed-out (typical
  symmetric-NAT / dmsg-relay degradation; non-blocking).
- `red` — all transport types failed (visor is effectively isolated).

### 4. `ar-self` (new)

Asks the AR `/resolve/<sudph|stcpr>/<our-PK>` for our own registration.
Compares to what the visor *thinks* it's listening on:

- Sudph port: AR-served port vs `transport.Manager`'s actual bound port.
- Sudph public IP: AR-served vs the visor's `/health public_ip`.
- Sudph protocol: registered or "no UDP target"?
- Stcpr address: same shape.

Finding levels:
- `green` — AR registration matches local state.
- `yellow` — drift detected (e.g., port mismatch: AR has X, visor bound Y).
  Suggested next step: restart visor; if persistent, indicates the
  publish path is failing (PR #3003 fixed one such class). 
- `red` — AR returns 404/empty for our PK (we're not registered at all).

This is the check that would have caught the bug PR #3003 fixed.

### 5. `tpd-self` (new)

Pulls the TPD entries for our own PK and inspects:

- Any transports with `a.sent + a.recv + b.sent + b.recv == 0` AND
  `live=false` (dangling, never used) — flag count.
- Any transports with a zero-PK on either edge (the null-PK orphan
  class PR #2971 fixed at the TPD-write side) — flag count.
- Any transports the visor doesn't recognize in its own
  `transport.Manager` registration (TPD has them but we don't) —
  flag count + IDs.
- Most recent registration timestamp; if oldest "registered" timestamp
  is > 1 day ago, recommend a `cli tp rm` sweep.

Finding levels:
- `green` — TPD-side state matches local transport manager.
- `yellow` — non-zero stale/orphan counts.
- `red` — count > some threshold (default 10) — drives route selection
  through ghost paths; campaign measurements will fail.

This is the check that would have caught the cli-tp-rm orphan situation
Beta diagnosed.

### 6. `dmsg-servers` (new)

For each delegated dmsg server in our discovery entry, do a low-cost
liveness probe — open a yamux session, no app traffic, record handshake
time, then close. Report:

- Number of delegated servers vs number reachable.
- Per-server handshake duration (sorted) — outliers might be flapping.

Finding levels:
- `green` — at least 4 of N reachable (configurable).
- `yellow` — fewer than 4 of N (degraded dmsg overlay; expect intermittent
  unreachability symptoms).
- `red` — none reachable.

### 7. `rsn` (new)

For each route-setup-node in `route_setup_nodes` (and embedded RSN if the
visor runs one), dmsg-ping the PK. Record reachability + RTT.

Finding levels:
- `green` — at least one RSN reachable.
- `yellow` — RSNs configured but all currently unreachable (route setup
  will fail; this matches what Alpha hit before PR #3006).
- `red` — no RSNs configured AND no `user_route_setup_nodes` AND visor
  doesn't run an embedded RSN (route setup *cannot* work).

### 8. `config` (new)

Pure local — reads `skywire.json` and emits findings on:

- `transport.sudph_port == 0` → yellow finding "sudph port will rotate on
  every restart; stale AR registration is recoverable but expect
  reachability gaps each restart cycle."
- `transport.stcpr_port == 0` → same.
- `transport.address_resolver` empty AND scheme is not `dmsg://` → yellow
  (touched by Alpha's discovery during the PR #3003 thread — non-dmsg AR
  URL skips the sudph udp_address publish path).
- `dmsgpty.whitelist` empty AND `dmsgpty.ssh_listen` non-empty → yellow
  (TCP listener exposed with no peer allowed; either configure or unset).
- `survey_whitelist` empty AND visor is `public: true` → yellow (any
  reachable peer can pull `/visor.log`).
- `hypervisors` empty AND we're not running standalone → yellow (visor
  has no remote admin path).

### 9. `routing-policy` (new, optional)

If a routing policy is loaded (WASM module from PR #2913), walk it for:

- Rules that never match any candidate transport in the current state.
- Rules that match every candidate (no-op).
- Rules that ban every transport for a destination (the destination is
  effectively un-routable).

Finding levels:
- `green` — every rule has at least one matching candidate.
- `yellow` — at least one rule never matches (likely operator mistake).
- `red` — at least one destination is unreachable under the current
  policy.

## Output shape

```json
{
  "verdict": "yellow",
  "reasons": ["transports.sudph: handshake timeout to canary 02f9aa58…"],
  "findings": [
    {
      "check": "ar-self",
      "level": "yellow",
      "summary": "AR sudph entry is stale: registered 39726, visor bound 58674",
      "next_step": "restart visor; if still stale, see PR #3003 (liveness fix)",
      "data": {
        "ar_registered_port": 39726,
        "visor_bound_port": 58674,
        "registered_at": "2026-06-04T15:15:03Z"
      }
    },
    ...
  ]
}
```

`findings[].data` carries raw probe results — only printed with
`--explain` in human mode (avoids burying the operator). The `next_step`
field is the single most useful line for operators triaging in a hurry.

## Implementation sketch

- New file `cmd/skywire-cli/commands/visor/doctor_probes.go` with one
  function per check. Each takes `(ctx, rpcClient, conf, probeTimeout)`
  and returns a `[]finding`.
- `runDoctor()` in `doctor.go` dispatches to the selected checks
  (default-all) via a registry map.
- Probes that need direct AR / TPD / dmsg-disc HTTP calls reuse the
  existing `httputil.DMSGHTTPClient` helpers — no new transport code.
- For probes that need to drive the visor's transport manager
  (`transports` check), call existing RPC methods
  (`AddTransport` / `RemoveTransport`); doctor cleans up on exit.

Total LOC estimate: ~600 incl. tests. Each probe is independently
testable with a mocked RPC client.

## Open questions

- Should doctor cache its own findings? Some checks (`ar-self`,
  `tpd-self`) are mildly expensive; caching the last result with a 60s
  TTL would let operators alarming on it run doctor every 30s without
  amplifying load. Probably yes, default `--cache-ttl 0` for parity with
  today's behavior.
- Should hypervisor dashboards pull these findings? If yes, expose
  `/doctor` on the logserver alongside `/health` — but gate it carefully
  (it surfaces more than `/health` does today).
- Routing-policy walk (#9) is the most speculative — it could ship as a
  separate `cli rg policy lint` instead of folding into doctor.

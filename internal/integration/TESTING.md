# Integration Test Architecture

## Current Problems

1. **Each subtest restarts all containers** — 6 VPN subtests × ~45s setup = ~270s wasted
2. **DMSG reconnection is non-deterministic** — stale discovery entries, slow server connections
3. **No diagnostic capture** — when tests fail, we don't see visor debug logs
4. **Sleep-heavy** — fixed waits instead of event-driven readiness checks
5. **No configuration** — changing timeouts or behavior requires code edits

## Proposed Structure

### Test Phases (not subtests)

Instead of independent subtests that each restart the world, structure tests as phases
within a single test that shares state:

```
TestVPN:
  Phase 1: Setup (start containers, wait for DMSG ready)
  Phase 2: Test DMSG transport + VPN functionality
  Phase 3: Add STCPR transport, verify traffic routes through it
  Phase 4: Add SUDPH transport, verify traffic routes through it
  Phase 5: Kill VPN server, verify client detects disconnection
  Phase 6: Remove transport, verify client handles it
  Cleanup: Stop containers
```

Phases 2-4 share the same running containers — no restart needed.
Only phases 5-6 need disruption, and only the VPN server restarts.

### Readiness Checks (replace sleeps)

Replace all fixed `time.Sleep` with event-driven readiness:

- `WaitForVisorDmsgReady(visor, timeout)` — check actual DMSG server connections
- `WaitForVisorApp(visor, app, timeout)` — check app is running via RPC
- `WaitForVisorRPC(visor, timeout)` — check visor RPC is responding
- `WaitForTransport(visor, remotePK, type, timeout)` — check transport exists

### Diagnostic Capture

On any failure:
- Dump visor debug logs from all containers (filtered by relevant keywords)
- Dump DMSG discovery state
- Dump TPD state
- Dump transport list from each visor

### Configuration File

Optional `integration-test.yaml` for overriding defaults:

```yaml
timeouts:
  dmsg_ready: 60s
  app_ready: 30s
  transport_retry: 5s
  routing_rule_expiry: 15s

retries:
  dmsg_transport: 5
  sudph_transport: 3

behavior:
  shutdown_mode: hard  # hard | graceful
  dump_logs: on_failure  # always | on_failure | never
  log_filter: "dmsg|transport|error|handshake"

skip:
  - TestVPN/simulate_transport_deleted  # known flaky
```

### Estimated Time Savings

Current: ~5-8 minutes for VPN tests (6 subtests × restart cycle)
Proposed: ~2-3 minutes (1 startup + phases + 1 restart for disruption tests)

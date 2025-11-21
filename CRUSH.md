# Notes for Crush AI

## Files to Commit

**DO NOT commit files that the user did not explicitly ask for**, such as:
- Session summary files (e.g., SESSION_SUMMARY.md, VPN_TEST_ROOT_CAUSE.md, E2E_TEST_ANALYSIS.md)
- Analysis documents for session use only
- Log files (*.log)
- Temporary files

**DO commit:**
- Documentation that was missing (e.g., README.md for features/tests)
- Code changes the user requested
- Configuration files the user asked to modify
- Files explicitly mentioned by the user

When in doubt, ask the user before committing non-code files.

## Project-Specific Notes

### E2E Tests
- System setup requires sudo once: `sudo ./ci_scripts/setup-sudo-requirements.sh`
- User runs port forwarding setup once: `make set-forwarding` (requires sudo, user does this)
- **NEVER run `make e2e-build`** - it calls set-forwarding which requires sudo
- After setup, tests run without sudo: `make e2e-dock && make e2e-run && make e2e-test`
- Port conflicts resolved by commenting out port mappings in docker-compose.yml

### Known Issues (as of session)
- VPN test fails - server-side CONSUME rule for port 44 not being created
- TestRestart occasionally fails with EOF errors (similar race condition)

### Important Learnings

#### "routing table: rule not found" Error
This error means:
- Packets arrived for an app, but the router has no rule to route them
- For VPN: Server calls `Listen()` on port 44, but CONSUME rule isn't created until first connection attempt
- By then, client's connection frames arrive but server can't route them (no CONSUME rule)
- Solution attempt: Retry with transport cleanup didn't fix root cause

#### Route Setup Process (Verified via Logging)
When app connects:
1. Route finder returns routes: "Found routes Forward: [...] Reverse [...]"
2. Setup-node handles request: "handling setup request: setupPK(...)"  
3. Router creates rules: "Save new Routing Rule with ID X FWD/REV(...)"

For VPN test:
- ✅ Routes ARE found by route finder
- ✅ FWD/REV rules ARE created on client side (visor-c)
- ❌ CONSUME rule NOT created on server side (visor-a port 44)
- Result: Client frames arrive at server but "routing table: rule not found"

#### Test Execution Sequence
Correct order:
1. Start server apps (apps without VisorServerName)
2. Wait for "running" status + small delay for routing registration  
3. Create transports (now server can handle them)
4. Start client apps (apps with VisorServerName)

#### VPN Server Auto-Start Issue
- VPN server may report "app already started" but not be running
- Fix: Stop and restart app when this occurs (2s delay between)
- Ensures server actually calls Listen() and Accept()

#### CLI Command Structure
- `skywire cli visor --rpc host:port <command>` - for visor commands
- `skywire cli route` - does NOT support --rpc (not under visor)
- `skywire cli route --json` returns `{"output": null}` when no routes exist

## Current Status

### Proxy/Skysocks Bug - FIXED

**The Issue:**
Commit aec7c3715 ("Fix hardcoded issue on apps (#2066)") introduced a bug in skysocks-client where it dials the server using the wrong port.

**Root Cause:**
The commit added a `--port` flag to allow apps to configure their own routing port. However, skysocks-client conflated two different concepts:
1. **Client's own routing port** (for its identity) - should be port 13
2. **Destination port when dialing server** - should be port 3

The buggy code used the client's own port (13) for both purposes:
```go
// Lines 101-105: Set client's own port
port := appCl.Config().RoutingPort
if appPort != 0 {
    port = routing.Port(appPort)
}

// Line 127: WRONG - dials server using client's own port
conn, err := dialServer(ctx, appCl, pk, port)  // port = 13, should be 3
```

**The Fix:**
Restored hardcoded server port for dialing:
```go
const (
    netType    = appnet.TypeSkynet
    serverPort = routing.Port(3)  // skysocks server port
)

// Client dials server on correct port (3)
conn, err := dialServer(ctx, appCl, pk, serverPort)

// Client's own port (13) is set separately via setAppPort()
```

**Why It Failed:**
- Client connects to server but uses wrong destination port (13 instead of 3)
- Server has no listener on port 13 (that's the client port)
- Server logs: "ERROR [launcher]: no listener on port 13, skysocks-client offline"
- Connection fails with "routing table: rule not found"

**Testing Required:**
Build and test with manual visor setup or e2e tests to verify proxy client can now connect to proxy server.

### E2E Test Failures - INVESTIGATING (Nov 21, 2025)

**Current Status:**
Tests failing after upstream merge (commit a52c15b00). Multiple issues identified:

#### Issue 1: "Invalid Signature" on DMSG Discovery Registration

**Symptoms:**
```
ERROR [dmsgC:disc]: endpoint="http://dmsg-discovery:9090/dmsg-discovery/entry/" resp_body="invalid signature" resp_status=401
WARN [dmsgC]: Initial post entry failed error="invalid signature"
```

**Analysis:**
- Happens consistently on FIRST registration attempt for all visors
- Visors retry and successfully register seconds later
- Once registered, "Updating entry" shows valid signatures and delegated servers
- Current cipher implementation (CGO and no-CGO variants) verified as correct

**Possible Causes:**
1. Race condition: Discovery service not fully initialized when visors start
2. Clock skew between containers (though 5s tolerance should handle this)
3. Network transient during container startup

**Impact:** Appears to be harmless - visors eventually register successfully. May add startup latency.

#### Issue 2: Transports Created But Never Reach IsSetup=true

**Symptoms:**
```
DEBUG [TRANSPORT] Transport exists but not yet setup: IsSetup=false
ERROR [router]: Error dialing route group error="route setup: failed to instantiate route id reserver: a dial attempt failed with: dial X@136: i/o deadline reached"
```

**Analysis:**
- Transports are created successfully (tp add command succeeds)
- Transport shows in tp ls but IsSetup remains false indefinitely
- Route setup service (port 136) dial attempts timeout after 20 seconds
- Causes TestRestart to fail (all 4 subtests timeout)

**Root Cause:**
The route setup service on port 136 is not ready to accept connections when transports are created. Possible reasons:
1. Service hasn't started yet (initialization order)
2. DMSG connection not fully established (still in "invalid signature" retry phase)
3. Upstream changes to transportable/tpd_concurrency modules interfering

**Related Upstream Changes:**
- Commit 9c07ac096: Re-enabled "transportable" module
- Series of commits toggling transportable/tpd_concurrency modules
- Commit 340c1364f: Added CGO secp256k1 (later refactored)

#### Issue 3: TestVPN Fails with "No Delegated Servers"

**Symptoms:**
```
Failed to establish dmsg transport: save transport: mt.client.Dial: dmsg error 103 - client entry in discovery has no delegated servers
```

**Analysis:**
- TestVPN runs ~4 minutes after previous tests
- visor-c (031b80cd...) should have registered by then
- But when adding dmsg transport, discovery reports no delegated servers

**Possible Cause:**
- Discovery entry was deleted/expired between tests
- Registration retry loop interfered by container restart between test cases
- Timing issue specific to dmsg transport type

#### Proposed Fixes

1. **Add DMSG Registration Wait:**
   ```go
   func (env *TestEnv) WaitForDmsgRegistration(visor string, timeout time.Duration) error {
       // Poll dmsg discovery until visor entry has delegated servers
       // Ensure registration complete before creating dmsg transports
   }
   ```

2. **Add Route Setup Service Wait:**
   ```go
   func (env *TestEnv) WaitForRouteSetupReady(visor string, timeout time.Duration) error {
       // Try connecting to visor's route setup service (port 136)
       // Or check specific log message indicating service is ready
   }
   ```

3. **Increase Delays in Test Flow:**
   - Current: 3s delay after server apps start
   - Proposal: 10s delay + explicit service readiness checks
   - Apply before transport creation AND before client app start

4. **Investigate Upstream Changes:**
   - Test with transportable module disabled (line 183 in init.go)
   - Check if commit 9c07ac096 introduced regression
   - Compare behavior with v1.3.31 release

**Next Steps:**
1. Try disabling transportable module to isolate issue
2. Add detailed logging to route setup process
3. Implement explicit readiness checks before transport creation
4. Consider if upstream merge should be reverted pending fixes

---

## UPDATE: E2E Test Timing Fixes Implemented (Nov 21, 2025)

**Fixes Applied:**

1. **Added `WaitForDmsgRegistration()` Function** (env_test.go:888-917):
   - Polls visor logs for successful DMSG registration with delegated servers
   - Looks for "delegated servers:" + "Connected to the dmsg network" patterns
   - 30s timeout with 1s check interval

2. **Increased Server App Initialization Delay** (util_test.go:129):
   - Changed from 3s to 10s after server apps report "running"  
   - Allows route setup service (port 136) to be fully operational

3. **Added DMSG Registration Checks**:
   - Before starting test cases (util_test.go:110-118)
   - After container restarts (restart_test.go:142-149)
   - Ensures visors have delegated servers before transport creation

**Expected Results:**
- TestRestart: Transports should reach IsSetup=true reliably
- TestVPN: Visors will have delegated servers when creating dmsg transports
- All messaging tests: More stable with proper initialization order

**Testing:** Full e2e test suite run required to verify fixes.


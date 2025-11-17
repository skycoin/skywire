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

### VPN Test Root Cause - RESOLVED

**The Issue:**
Commit d3d014c38 ("Fix CLI RPC server race condition with external app launcher") added dependencies to the CLI module:
```go
cli = maker("cli", initCLI, &launch, &tr)  // Added &launch and &tr dependencies
```

This was intended to fix "no app launcher available" errors in e2e tests by ensuring CLI RPC server only starts after launcher and transport manager are ready.

**Why It Broke VPN Tests:**
- v1.3.31: CLI had NO dependencies → initialized concurrently with launcher → WORKED
- PR 2095: Internal launcher (no binary field) → WORKED
- PR 2096: External launcher + CLI depends on launcher → FAILED

Making CLI wait for launcher changed the module initialization timing in a way that affected routing setup. The vinit module system waits for ALL dependencies to fully complete before a module can start.

**The Fix:**
Reverted CLI module to have no dependencies (matching v1.3.31):
```go
cli = maker("cli", initCLI)  // No dependencies
```

This allows CLI RPC server to start concurrently during initialization. The "no app launcher available" errors mentioned in commit d3d014c38 haven't been observed in practice - the RPC server handles requests properly as long as the visor has finished initializing before tests make RPC calls.

**Testing Required:**
Run e2e tests with this fix to verify VPN test passes and no "no app launcher available" errors appear.

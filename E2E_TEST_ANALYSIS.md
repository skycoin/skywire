# E2E Test Analysis - Session Notes

## Current Status (Branch: fix-e2e)

### Commits Made (4 total):
1. `885a91063` - Fix local e2e test environment setup  
2. `a376f96a6` - Handle duplicate transport key constraint violations in TPD
3. `16af376dc` - Clear app error state before starting proc
4. `1d1e5d55e` - Separate sudo requirements from e2e test execution

### Test Results Evolution

**Baseline (from initial summary):**
- 12/15 PASS (80%)
- Failures: TestEnv_SendSkyMessage, TestEnv_SendSkyMessage_second, TestVPN

**After TPD duplicate key fix:**
- 14/15 PASS (93.3%)
- ✅ Fixed: Both SendSkyMessage tests
- Remaining: TestVPN

**Latest run (all fixes applied):**
- 13/15 PASS (86.7%)
- ✅ Fixed: Both SendSkyMessage tests  
- ❌ TestVPN: Still failing (errored status on 2nd subtest)
- ❌ TestRestart: New intermittent failure (1/4 subtests)

## Key Fixes Implemented

### 1. Duplicate Transport Key Handling (`pkg/transport/handshake.go`)

**Problem**: Transports persist in TPD after visor crashes but not locally. Re-registration hits database constraint.

**Solution**:
```go
if strings.Contains(err.Error(), "duplicate key value violates unique constraint") {
    // Delete stale transport from TPD
    dc.DeleteTransport(ctx, entry.ID)
    // Retry registration
    dc.RegisterTransports(ctx, recvSE)
}
```

**Impact**: ✅ Completely fixed SendSkyMessage tests

### 2. Proc Manager Error Clearing (`pkg/app/appserver/proc_manager.go`)

**Problem**: Old app errors persist when restarting apps, showing incorrect "errored" status.

**Solution**: Move `delete(m.errors, conf.AppName)` to BEFORE `proc.Start()` instead of after.

**Impact**: ⚠️ Partial - doesn't fully fix VPN test failures

### 3. Sudo Requirements Separation

**Problem**: Tests required sudo prompts during execution, making automation difficult.

**Solution**:
- Created `setup-sudo-requirements.sh` for one-time system config
- Created `check-e2e-requirements.sh` to verify setup without sudo
- Updated Makefile targets to check instead of set
- Matches CI behavior more closely

**Impact**: ✅ Cleaner developer experience, no sudo during test execution

## Remaining Issues

### Issue #1: TestVPN - Second Subtest Fails

**Symptoms**:
- First VPN test passes (vpn_is_functional_DMSG) 
- Second test fails during app startup with "errored" status
- Error: `env_test.go:134: Received unexpected error: errored`

**Test Flow**:
1. Start VPN server app
2. Start VPN client app  
3. Run tests
4. Stop apps (cleanup)
5. Repeat for next transport type
6. **FAILS** on second iteration

**Hypothesis**:
- App cleanup not complete before next test
- Error state persisting despite proc manager fix
- Race condition in app stop/start sequence

**Evidence Needed**:
- Actual error message from the app (not just "errored" string)
- App logs showing what causes the error state
- Timing of cleanup vs. next test start

### Issue #2: TestRestart - Intermittent EOF Errors

**Symptoms**:
- Subtest "r: ac, s: a->c" failed (1/4 subtests)
- HTTP EOF errors despite 5 retries with 10s delays
- Other 3 subtests passed
- Same error pattern as original SendSkyMessage issue

**Test Flow**:
1. Remove all transports
2. Restart visor containers
3. Re-add transports
4. Wait 10s for stabilization
5. Try to send message (with retries)

**Error**: `Post "http://visor-a:8001/message": EOF`

**Hypothesis**:
- Transport cleanup not complete before restart
- New transport registration happens before old one expires in TPD
- Skychat app not fully ready despite visor being up
- Intermittent race condition (explains why 3/4 passed)

**Evidence from logs**:
```
[2025-11-15T16:47:27.373Z] DEBUG [tp:031b80]: Performing settlement handshake...
```
Visor started successfully and began establishing transports, but something still failed.

## Critical Analysis of Test Suite

### What Tests Are Trying to Validate

1. **Basic Functionality**: Container setup, CLI, app management
2. **Transport Layer**: Creating/removing transports, different transport types
3. **Messaging**: Multi-hop routing, app-to-app communication
4. **Resilience**: Restart scenarios, transport persistence
5. **VPN Features**: Client/server, killswitch, multiple transport types

### Test Environment Complexity

**16 Docker containers:**
- 3 visors (test nodes)
- 13 services (dmsg, TPD, route finder, SD, etc.)
- 3 custom networks (srv, visors, intra)

**Challenges**:
- Complex startup ordering dependencies
- State persistence across tests
- Cleanup between test cases
- Timing/synchronization issues

### Problem Areas in Current Tests

#### 1. State Management
- **Issue**: Tests share visor instances across subtests
- **Impact**: Previous test state can affect next test
- **Example**: VPN test fails on 2nd subtest but 1st passes

#### 2. Timing Assumptions
- **Issue**: Fixed delays don't account for variable container startup
- **Example**: `time.Sleep(10 * time.Second)` after transport add
- **Better**: Poll for ready state with timeout

#### 3. Error Visibility  
- **Issue**: Generic error messages like "errored" don't help debug
- **Example**: VPN test just says "errored" without actual cause
- **Better**: Log the actual app error message

#### 4. Cleanup Procedures
- **Issue**: Incomplete cleanup between tests
- **Example**: TestRestart removes transports but TPD might still have stale entries
- **Better**: Verify cleanup completed before proceeding

## Recommended Improvements

### Short-term (Current Session)

1. **Add detailed error logging**:
   ```go
   if appState.Status == "errored" {
       return false, fmt.Errorf("%s (detail: %s)", appState.Status, appState.DetailedStatus)
   }
   ```

2. **Poll for readiness instead of fixed sleeps**:
   ```go
   func waitForAppReady(app, expectedStatus string, timeout time.Duration) error {
       deadline := time.Now().Add(timeout)
       for time.Now().Before(deadline) {
           status, err := getAppStatus(app)
           if err == nil && status == expectedStatus {
               return nil
           }
           time.Sleep(1 * time.Second)
       }
       return fmt.Errorf("app not ready after %v", timeout)
   }
   ```

3. **Add TPD cleanup verification**:
   ```go
   func ensureTransportsCleared(pk string) error {
       // Query TPD to verify no transports exist for this visor
       // Return error if any found
   }
   ```

4. **Capture and log actual app errors**:
   - Modify test helpers to include `appState.DetailedStatus`
   - Log app error messages before failing test

### Medium-term (Future Work)

1. **Test Isolation**: Each test should clean up completely
2. **Health Checks**: Wait for services to be fully ready
3. **Better Fixtures**: Dedicated test data/state per test
4. **Parallel Testing**: Run independent tests concurrently
5. **Failure Forensics**: Auto-capture logs on failure

## Next Steps (For This Session)

1. ✅ Commit and push sudo separation changes
2. 🔄 Add detailed error logging to test helpers
3. 🔄 Investigate actual VPN app error (not just "errored" string)
4. 🔄 Add polling-based readiness checks
5. 🔄 Re-run tests with improved logging
6. 🔄 Debug remaining failures with better diagnostics

## Notes for Resuming

**What works well:**
- TPD duplicate key fix is solid
- Basic messaging tests now reliable
- Environment setup improved

**Still investigating:**
- VPN second subtest "errored" - need actual error message
- TestRestart intermittent EOF - race condition in transport setup?

**Key insight**: Tests are sensitive to state from previous runs. Need better isolation and cleanup verification.

**Best practices learned:**
- Always check for existence before assuming state
- Log actual errors, not just status codes
- Poll for readiness, don't assume timing
- Verify cleanup completed before next test

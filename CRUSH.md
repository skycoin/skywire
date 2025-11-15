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
- After that, tests run without sudo: `make e2e-build && make e2e-run && make e2e-test`
- Port conflicts resolved by commenting out port mappings in docker-compose.yml

### Known Issues (as of session)
- VPN test fails even after startup sequencing fixes
- TestRestart occasionally fails with EOF errors (similar race condition)
- Need deeper investigation of VPN-specific issues

### Important Learnings

#### "routing table: rule not found" Error
This error means:
- Packets arrived for an app, but the app hasn't registered with the router yet
- App shows "running" when process starts, but routing rules register slightly later during Accept()
- Solution: Add 2-3s delay after "running" status before creating transports

#### Test Execution Sequence
Correct order:
1. Start server apps (apps without VisorServerName)
2. Wait for "running" status + small delay for routing registration  
3. Create transports (now server can handle them)
4. Start client apps (apps with VisorServerName)

#### CLI Command Structure
- `skywire cli visor --rpc host:port <command>` - for visor commands
- `skywire cli route` - does NOT support --rpc (not under visor)
- `skywire cli route --json` returns `{"output": null}` when no routes exist

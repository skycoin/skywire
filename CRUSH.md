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
- VPN test has race condition in cleanup (second test starts before first fully stops)
- TestRestart occasionally fails with EOF errors (similar race condition)
- Need to implement `waitForAppStopped()` helper to verify cleanup completion

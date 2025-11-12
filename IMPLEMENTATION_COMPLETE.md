# In-Process App Execution - Implementation Complete ✅

## Summary

Successfully converted Skywire apps from external process execution to in-process function calls.

### What Works Now

✅ **All 5 apps refactored**:
- skychat
- vpn-client
- vpn-server
- skysocks
- skysocks-client

✅ **All code compiles successfully**

✅ **Ready to test with**: `go run . visor`

### Configuration

**Internal mode (no binary field)**:
```json
{
  "name": "skychat",
  "args": ["--addr", ":8001"],
  "port": 1
}
```
→ Runs as goroutine in visor process

**External mode (binary specified)**:
```json
{
  "name": "custom-app",
  "binary": "/path/to/app",
  "args": [...],
  "port": 50
}
```
→ Runs via exec.Command() as before

### Next Steps

1. **Test**: Run `go run . visor` without installing binaries
2. **Optional**: Update config generators to emit empty `binary` field
3. **Verify**: All apps start, stop, and handle shutdown gracefully

### Files Changed

- `pkg/app/launcher/registry.go` - NEW (app registry)
- `pkg/app/appserver/app_state.go` - Made `binary` optional
- `pkg/app/appcommon/proc_config.go` - Added `RunFunc` field
- `pkg/app/launcher/launcher.go` - Check empty binary → use registry
- `pkg/app/appserver/proc.go` - Split into startInProcess()/startExternal()
- All 5 app commands - Extracted to RunAppName() functions

**Total: ~500 lines changed across 10 files**

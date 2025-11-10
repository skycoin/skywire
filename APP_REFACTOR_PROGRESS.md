# In-Process App Execution Implementation - Progress Report

## Completed Changes

### 1. Core Infrastructure ✅

**Created `pkg/app/launcher/registry.go`**:
- App function registry for mapping app names to in-process functions
- `RegisterApp(name, fn)` - registers an app function
- `GetApp(name)` - retrieves an app function
- Type: `AppFunc func(ctx context.Context, args []string, client *app.Client) error`

**Modified `pkg/app/appserver/app_state.go`**:
- Added `Mode string` field to `AppConfig` (values: "internal" | "external")
- Made `Binary` field optional with `json:",omitempty"`

**Modified `pkg/app/appcommon/proc_config.go`**:
- Added `RunFunc interface{}` field to `ProcConfig` (not serialized to JSON)

**Modified `pkg/app/launcher/launcher.go`**:
- Updated `makeProcConfig` to check `Mode == "internal"` and set `RunFunc` from registry

**Modified `pkg/app/appserver/proc.go`**:
- Added `appCtx` and `appCancelCtx` fields to `Proc` struct for context management
- Modified `NewProc` to skip creating `exec.Cmd` when `RunFunc != nil`
- Only sets up command pipes/stdout/stderr for external processes

### 2. Example App Refactoring ✅

**Refactored `cmd/apps/skychat/commands/skychat.go`**:
- Extracted app logic into `RunSkychat(ctx, args, client)` function
- Registered with `launcher.RegisterApp("skychat", RunSkychat)` in init()
- Modified Cobra `Run` to call `RunSkychat` with proper context
- Added graceful shutdown handling via context cancellation
- Maintains backward compatibility - can still be run as CLI

## Remaining Work

###  3. Complete Proc.Start() Method ⏳

**File**: `pkg/app/appserver/proc.go` (lines 198-290+)

Need to add logic to handle in-process execution:

```go
func (p *Proc) Start() error {
    // ... existing setup ...
    
    if p.conf.RunFunc != nil {
        // IN-PROCESS EXECUTION PATH
        // 1. Create context with cancel
        p.appCtx, p.appCancelCtx = context.WithCancel(context.Background())
        
        // 2. Create app.Client connected to this proc's manager
        appClient := p.createInProcessClient()
        
        // 3. Cast RunFunc to proper type
        runFunc, ok := p.conf.RunFunc.(func(context.Context, []string, *app.Client) error)
        if !ok {
            return errors.New("invalid RunFunc signature")
        }
        
        // 4. Run app in goroutine
        go func() {
            err := runFunc(p.appCtx, p.conf.ProcArgs, appClient)
            if err != nil {
                p.err = err.Error()
            }
            // Cleanup...
        }()
        
        // 5. Wait for connection setup (app calls appClient methods)
        // 6. Handle lifecycle same as external process
    } else {
        // EXISTING EXTERNAL PROCESS PATH
        if err := p.cmd.Start(); err != nil { ... }
        // ... rest of existing logic ...
    }
}
```

**Key challenges**:
- Creating `app.Client` that connects back to proc manager without exec
- Handling the connection handshake in-process
- Proper lifecycle management (start, stop, wait)

### 4. Implement createInProcessClient() ⏳

**File**: `pkg/app/appserver/proc.go` (new method)

```go
func (p *Proc) createInProcessClient() *app.Client {
    // Option A: Use TCP loopback (simpler)
    // - Client connects to m.Addr() via TCP
    // - Same handshake as external process
    
    // Option B: Use net.Pipe() (more efficient)
    // - Create in-memory pipe
    // - Manually inject connection via InjectConn()
    // - Client uses pipe instead of TCP
    
    // Recommend Option A for simplicity
}
```

### 5. Update Stop() Method ⏳

**File**: `pkg/app/appserver/proc.go` (line 300+)

```go
func (p *Proc) Stop() error {
    if p.appCancelCtx != nil {
        // Cancel context for in-process app
        p.appCancelCtx()
    }
    
    if p.cmd != nil && p.cmd.Process != nil {
        // Existing external process stop logic
        ...
    }
}
```

### 6. Refactor Remaining Apps 📋

Need to refactor 4 more apps following skychat pattern:

- [ ] **vpn-client** (`cmd/apps/vpn-client/commands/vpn-client.go`)
- [ ] **vpn-server** (`cmd/apps/vpn-server/commands/vpn-server.go`)  
- [ ] **skysocks** (`cmd/apps/skysocks/commands/skysocks.go`)
- [ ] **skysocks-client** (`cmd/apps/skysocks-client/commands/skysocks-client.go`)

Each requires:
1. Extract logic into `RunAppName(ctx, args, client)` function
2. Register in `init()` with `launcher.RegisterApp(name, RunAppName)`
3. Update Cobra command to call the function
4. Handle context cancellation for graceful shutdown

### 7. Update Config Generation 📋

**File**: Config generators need updating to use new mode field

Example config with `mode: "internal"`:
```json
{
  "launcher": {
    "apps": [
      {
        "name": "skychat",
        "mode": "internal",
        "args": ["--addr", ":8001"],
        "auto_start": true,
        "port": 1
      }
    ]
  }
}
```

### 8. Testing 🧪

- [ ] Test in-process app startup
- [ ] Test external app startup (backward compat)
- [ ] Test mode switching
- [ ] Test graceful shutdown
- [ ] Test error handling
- [ ] Test on Android (if applicable)

## Config Examples

### Internal Mode (Function Call)
```json
{
  "name": "skychat",
  "mode": "internal",
  "args": ["--addr", ":8001"],
  "auto_start": true,
  "port": 1
}
```

### External Mode (System Call)
```json
{
  "name": "custom-app",
  "mode": "external",
  "binary": "/usr/local/bin/custom-app",
  "args": ["--config", "/etc/app.conf"],
  "auto_start": false,
  "port": 50
}
```

### External Mode (Backward Compatible - no mode field defaults to external if binary present)
```json
{
  "name": "vpn-client",
  "binary": "skywire",
  "args": ["app", "vpn-client", "--dns", "1.1.1.1"],
  "auto_start": false,
  "port": 43
}
```

## Estimated Remaining Effort

- **Proc.Start() modifications**: 2-4 hours
- **createInProcessClient()**: 2-3 hours  
- **Stop() modifications**: 1 hour
- **Refactor 4 remaining apps**: 3-4 hours (45min each)
- **Config generation updates**: 1 hour
- **Testing & debugging**: 3-4 hours

**Total**: ~12-17 hours of focused work

## Next Steps

1. Complete `Proc.Start()` with in-process path
2. Implement `createInProcessClient()` using TCP loopback approach
3. Update `Stop()` method
4. Test with skychat
5. Refactor remaining apps
6. Update config generators
7. Full integration testing

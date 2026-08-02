# Skywire App Implementation Guide

This document provides a comprehensive guide on how to build a decentralized application (App) within the Skywire ecosystem. It covers the correct architectural patterns, lifecycle management, and integration with the `visor` (the core Skywire node) to leverage Skywire's privacy-first, encrypted mesh networking.

---

## 1. Project Structure

Skywire apps are typically Go binaries built using the [Cobra CLI framework](https://github.com/spf13/cobra). To ensure your app can be managed by the Visor's internal launcher, it should follow this standard directory layout:

```text
cmd/apps/
└── myapp/
    ├── myapp.go          # Entry point (main package), initializes Cobra
    ├── commands/
    │   └── myapp.go      # Core logic, Cobra command definitions, and Run function
    └── static/           # (Optional) Embedded web UI files using go:embed
```

### 1.1 Entry Point (`myapp.go`)
The `main` package should only handle CLI formatting and execution. All business logic belongs in the `commands` package.

```go
package main

import (
    "github.com/ivanpirog/coloredcobra"
    "github.com/skycoin/skywire/cmd/apps/myapp/commands"
)

func main() {
    cc.Init(&cc.Config{
        RootCmd:       commands.RootCmd,
        Headings:      cc.HiBlue + cc.Bold,
        Commands:      cc.HiBlue + cc.Bold,
        CmdShortDescr: cc.HiBlue,
        NoExtraNewlines: true,
    })
    commands.Execute()
}
```

---

## 2. The App Client (`pkg/app.Client`)

The bridge between your app and the Visor is the `app.Client`. It handles logging, configuration, and secure networking.

### 2.1 Initialization
When the Visor launches your app, it injects configuration via environment variables (specifically `PROC_CONFIG`). The `app.NewClient(nil)` function reads this automatically.

```go
import "github.com/skycoin/skywire/pkg/app"

appCl := app.NewClient(nil)
defer appCl.Close()
```

**Key Benefits:**
- **Unified Logging:** Use `appCl.Log()` to write logs that the Visor will capture and display in its own UI/logs.
- **Configuration:** Access your assigned routing port, the Visor's Public Key (PK), and working directory via `appCl.Config()`.
- **Lifecycle Control:** Report your app's status (Running, Stopped, Error) back to the Visor.

### 2.2 Standalone Mode
Some apps (like `skychat`) can run without a Visor for debugging or long-running HTTP services. You can implement a `--standalone` flag that bypasses `app.NewClient` and falls back to standard Go logging and local TCP networking.

```go
var standalone bool

if standalone {
    chatLog = logrus.New() // Standard logger
} else {
    appCl = app.NewClient(nil)
    defer appCl.Close()
    chatLog = appCl.Log()  // Visor-proxied logger
}
```

---

## 3. Lifecycle Management & Status Reporting

The Visor needs to know when your app is healthy, busy, or crashed. You must implement a graceful shutdown sequence.

### 3.1 Status Updates
Use the helper methods to update the Visor. These are "best-effort" and safe to call even if `appCl` is nil (in standalone mode).

```go
import "github.com/skycoin/skywire/pkg/app/appserver"

// Tell the Visor you are ready
appCl.SetStatusOrLog(appserver.AppDetailedStatusRunning)

// Tell the Visor you are processing something specific
appCl.SetDetailedStatus("Processing Trade #1234")

// Tell the Visor you are stopping
appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
```

### 3.2 Graceful Shutdown
Listen for OS interrupts (SIGINT/SIGTERM) or context cancellations. When a signal is received, clean up your resources (close listeners, flush databases) and set your status to `Stopped`.

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

termCh := make(chan os.Signal, 1)
signal.Notify(termCh, os.Interrupt)

go func() {
    select {
    case <-termCh:
        appCl.SetStatusOrLog(appserver.AppDetailedStatusStopped)
        cancel() // Trigger shutdown in your main loop
    case <-ctx.Done():
        return
    }
}()
```

---

## 4. Secure Mesh Networking (`Dial` & `Listen`)

The most powerful feature of a Skywire app is the ability to open direct, encrypted `net.Conn` connections to other Visors across the mesh, bypassing the public internet.

### 4.1 Listening for Connections
To accept connections from other nodes, use `appCl.Listen()`. You must specify the network type (e.g., `appnet.TypeSkynet`) and the routing port assigned to your app.

```go
import (
    "github.com/skycoin/skywire/pkg/app/appnet"
    "github.com/skycoin/skywire/pkg/routing"
)

port := appCl.Config().RoutingPort
lis, err := appCl.Listen(appnet.TypeSkynet, routing.Port(port))
if err != nil {
    appCl.SetErrorOrLog(err)
    return err
}

for {
    conn, err := lis.Accept()
    if err != nil {
        break
    }
    go handleSecureConnection(conn)
}
```

### 4.2 Dialing Other Nodes
To connect to a remote Visor, you need their Public Key (PK) and the port their app is listening on.

```go
remoteAddr := appnet.Addr{
    Net:    appnet.TypeSkynet,
    PubKey: remotePublicKey,
    Port:   45, // The remote app's routing port
}

conn, err := appCl.Dial(remoteAddr)
if err != nil {
    appCl.Log().Errorf("Failed to dial remote: %v", err)
}
defer conn.Close()

// conn is a standard net.Conn. You can now read/write encrypted data.
```

---

## 5. Advanced RPC & Visor Integration

Your app can communicate directly with the Visor's internal RPC server to perform advanced actions.

### 5.1 Port Management
If your app needs to change its routing port dynamically (e.g., to match a port configured in a UI), use `SetAppPortOrLog`.

```go
import "github.com/skycoin/skywire/pkg/routing"

newPort := routing.Port(45)
appCl.SetAppPortOrLog(newPort)
```

### 5.2 Error Reporting
If your app encounters a fatal error, report it to the Visor so the UI can alert the user.

```go
err := db.Connect()
if err != nil {
    appCl.SetErrorOrLog(fmt.Errorf("database connection failed: %w", err))
    return err
}
```

### 5.3 User Notifications

When something happens that deserves the user's attention while they are *not* looking at your app, publish a notification:

```go
// Best-effort and nil-safe: no-ops in standalone mode and on a headless visor.
appCl.NotifyOrLog("MyApp", "Transfer finished", "myapp-transfers")
```

That is the entire API. No OS-specific code, no platform checks, no `runtime.GOOS`.

#### The split: you decide *whether*, the visor decides *where*

This division is the whole design, and getting it wrong is the main way apps misbehave.

**Your app decides whether.** Only you know what the user has muted, which screen they have open, and whether this event matters. Nobody else can make that call for you.

**The visor decides where.** It routes to the first delivery path that can actually reach the user:

| | Sink | Wins when |
|---|---|---|
| a | An attached UI holding a capability lease | that UI will surface it itself |
| b | A subscribed host app (`skywire-cli hv notify`) | one is connected |
| c | The host OS notification centre (`pkg/osnotify`) | there is a desktop session |
| d | Dropped | headless — the normal case for most of the fleet |

First match wins, so an event surfaces **exactly once**. You never think about this — but knowing it exists explains why you must not double up at your end.

#### The rules

**1. Never notify about something the user is already looking at.** If your app has a UI and it is open and focused, that UI should show the event and your app should stay quiet. Otherwise the user gets it twice.

**2. Notify sparingly.** A result the user is waiting for, on screen, is noise. A failure, or a result arriving long after they walked away, is not. When unsure, don't — a notification the user didn't want costs far more trust than one they didn't get.

**3. Never set the app name.** The visor stamps it from your proc's identity and overwrites whatever you send. This is deliberate: an app must not be able to publish under another app's name.

**4. Bodies may be untrusted, and are never logged.** If the body contains peer-supplied text, that is fine — it is passed to every sink as data, never as anything executable. Do not log it yourself either.

**5. Use a tag for anything that supersedes.** Events sharing a tag *replace* one another in sinks that support it (an Android notification tag) instead of stacking. Status transitions for one connection should share a tag; independent events should not. `""` means no coalescing.

#### Two worked patterns for rule 1

Your app knows *something* about whether the user is watching. Use whatever signal you actually have:

**If you own your UI — ask it.** skychat serves its own UI, so the UI heartbeats "I can show notifications myself" and the Go side stays silent while that lease is fresh:

```go
// cmd/apps/skychat/commands/osnotify.go
func shouldNotifyOS(enabled, uiCapable bool) bool { return enabled && !uiCapable }
```

The lease must expire (skychat: 45s) so a UI that dies without saying goodbye can't silence notifications forever, and it must mean *"a UI that will really surface this"* — not merely "a UI is connected". A browser tab with notifications blocked, or a backgrounded mobile WebView, can show nothing and must stop claiming capability.

**If you don't own your UI — use time.** skydex-client's UI belongs to a vendored engine it cannot query. But a market dial is always user-initiated, so for the first few seconds the user is still watching the form where the engine already renders the result:

```go
// cmd/apps/skydex-client/commands/skydex-client.go
const notifyAttentionWindow = 3 * time.Second

func (d appnetDialer) notifyIfUnwatched(waited time.Duration, body string) {
    if waited < notifyAttentionWindow {
        return
    }
    d.appCl.NotifyOrLog("SkyDEX", body, notifyTagMarket)
}
```

#### Don't promise what you can't observe

Before adding a notification, confirm the event actually reaches your Go code. skydex-client would love to announce "order filled", but the market protocol is strictly request/response with no unsolicited frames and order status is discovered only by the browser polling — so no Go layer ever learns it. A notification for an event you cannot observe is dead code that reads like a feature.

Equally, check the *trigger* is real. An `if err != nil` guard is worthless if the function it wraps can only ever return `nil`.

#### Testing

Notifications are invisible on a headless box and in CI, so don't test through the OS. Two options:

- **Unit-test the decision, not the delivery.** The interesting logic is your "should I notify?" gate — a pure function is trivial to pin exhaustively.
- **Watch the stream.** `skywire-cli hv notify --print` subscribes to the visor and prints every event, which works headlessly and in containers:

```
$ skywire-cli hv notify --print
20:17:49  [skychat] 024ec474…58c7 — hello there
```

Keep a global disable flag (skychat's `--os-notify`) that your `TestMain` can switch off, so a test suite never posts real notifications on a developer's desktop.

### 5.4 Advanced Dialing Options
For high-bandwidth or high-latency scenarios, you can use `DialWithOptions` to request multi-path routing (`muxRoutes`) or enforce a minimum number of hops for privacy (`minHops`).

```go
// Force traffic through at least 2 intermediate nodes for extra privacy
conn, err := appCl.DialWithOptions(remoteAddr, 1, 2, 0, 0, 0, 0, false)
```

---

## 6. Registration with the Visor

To make your app discoverable and launchable by the Visor, you must register it in the `launcher` package.

In your `init()` function inside the `commands` package:

```go
import "github.com/skycoin/skywire/pkg/app/launcher"

func init() {
    // "myapp" is the name users will type or see in the UI
    launcher.RegisterApp("myapp", RunMyApp)
    
    // Define your CLI flags here
    RootCmd.Flags().StringVar(&myConfig, "config", "", "Path to config")
}
```

---

## 7. Summary Checklist for New Apps

1. [ ] **Cobra Structure:** Set up `cmd/apps/myapp` with `main` and `commands` packages.
2. [ ] **Client Init:** Call `app.NewClient(nil)` and defer `appCl.Close()`.
3. [ ] **Logging:** Replace `log.Println` with `appCl.Log().Info()`.
4. [ ] **Networking:** Use `appCl.Listen()` to accept secure connections and `appCl.Dial()` to initiate them.
5. [ ] **Status:** Call `SetStatusOrLog(Running)` on startup and `Stopped` on shutdown.
6. [ ] **Notifications:** Use `NotifyOrLog` for events the user should see when they aren't watching your app (see 5.3).
6. [ ] **Graceful Exit:** Listen for `os.Interrupt` to cancel your context and close listeners cleanly.
7. [ ] **Registration:** Call `launcher.RegisterApp()` in `init()`.

By following these patterns, your application will seamlessly integrate into the Skywire mesh, inheriting the robust encryption, routing, and process management provided by the Visor.

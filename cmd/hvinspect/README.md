# hvinspect

Headless inspector for the Skywire hypervisor UI — both the wasm-visor harness
(`skywire cli hv serve --tls --harness`) and the native HV UI (`:8000`). Drives
the installed Brave/Chromium via [chromedp](https://github.com/chromedp/chromedp)
(pure Go, no cgo) to capture, for any hash route:

- console output (log/info/warn/error + uncaught exceptions)
- the rendered DOM (`document.documentElement.outerHTML`)
- a full-page screenshot

This is a **separate Go module** (its own `go.mod`): chromedp's dependency tree
is deliberately kept out of the main skywire module so the visor binary, CI,
`vendor/`, and the TinyGo build path never see it.

## Build & use

```sh
cd cmd/hvinspect && go build -o hvinspect .
./hvinspect 'https://localhost:8443/#/nodes/list/1' 12 /tmp/nodelist
```

Args: `<url> [waitSeconds] [outPrefix]`. Writes `<outPrefix>.html`,
`<outPrefix>.console.txt`, `<outPrefix>.png` and echoes the console to stdout.
Self-signed certs (the harness) are accepted.

## Future: `skywire cli hv inspect`

The main module already vendors `coder/websocket`, so this could be promoted to
a first-class `skywire cli hv inspect` command driving the Chrome DevTools
Protocol directly over that dependency (launching the browser as a subprocess) —
no new dependency in the main module.

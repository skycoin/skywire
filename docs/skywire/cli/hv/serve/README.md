# skywire cli hv serve

[← skywire cli hv](../README.md)

Serve the keyless standalone wasm-VISOR desk over HTTP.

Serves the desk: the tab as a Linux host, whose terminal runs the ONE skywire
command module (/skywire.wasm — the desk host, the in-tab visor and every
'skywire' command) and whose nested browser opens the hypervisor UI that visor
serves. The module is --exec-wasm, or the one kept beside this binary (skywire.wasm.gz),
or in the user cache directory when that directory is not writable. Packages
do not ship it: with none yet, the first desk open fetches the release's and
checks it against its manifest. This command has no visor to refresh it
through after that. Restart the process after an update (e.g. wire it to a
systemd service that restarts on auto-update) and put a reverse proxy (Caddy)
in front on a subdomain.

The same surface can be hosted BY the visor itself (one binary, one process):
set hypervisor.wasm_serve.addr in the visor config. This command is the
standalone equivalent, sharing the implementation (pkg/visor.ServeWasm).

Keyless: no key is baked in; each visitor's browser mints + persists its own
ephemeral key (localStorage). That is what makes serving from a domain safe — the
page never asks anyone to type a secret key.

## Usage

```
skywire cli hv serve
```

## Flags

```
  -a, --addr string             HTTP listen address (default ":7999")
      --boot-reports string     keep the reports of desks that did not start in this file as JSON lines, moved to <file>.1 at 1 MiB (they are also logged). The default is under $STATE_DIRECTORY when systemd sets one (StateDirectory=), else the temp dir. Empty = log only (default "/tmp/skywire-desk-boot-reports.jsonl")
      --browse-origin string    ALSO serve the browse-origin SW bootstrap on this second addr (e.g. 127.0.0.1:7998), for the hosted real-origin browser's B origins. Caddy routes *.<browse-suffix> here; this same process serves V on --addr and B here. Empty = off (V host-routes B on --addr, local mode)
      --browse-suffix string    browse-origin domain suffix for the real-origin browser (leading dot). Empty = .mesh.localhost (local); when --browse-origin is set (hosted mode) and this is empty it defaults to the deployment's browse_origin_suffix (".theskywirenetwork.net" from services-config.json)
      --desk-docs-port int      run 'skywire doc serve' on this desk vnet port (0 = off) — same cost as above
      --desk-help-terminal      open a desk terminal that has already run 'skywire --help' — costs a whole extra Go/wasm runtime of the full binary, and that memory is never returned
      --exec-js-tinygo string   path to TinyGo's wasm_exec.js (targets/wasm_exec.js in the TinyGo tree that built --exec-wasm), served at /wasm_exec_tinygo.js. The desk picks it for a module TinyGo built and keeps Go's loader for the rest. Empty = none
      --exec-wasm string        path to the full skywire CLI wasm module to serve at /skywire.wasm — the desk host, the tab's visor and the terminal's 'skywire' command (build: GOOS=js GOARCH=wasm go build -tags "withoutsystray withoutgotop" -o build/skywire.wasm .). Empty = the module kept beside this binary (skywire.wasm.gz), or in the user cache directory when that directory is not writable; when there is none yet, the release's is fetched the first time the desk is opened
      --harness                 mount the /ctl/* operator control bridge (drive the in-tab visor from a shell); DEV ONLY — never expose publicly
      --link stringArray        link another site under the boot message, as name=url (repeatable), so crawlers find it in the page, e.g. --link status=https://status.skywire.dev
      --lot string              serve LinuxOnTab at /lot/ from a shell directory, or proxy it from a site that publishes it (e.g. https://next.linuxontab.com), and make the desk cross-origin isolated, so LinuxOnTab can run inside it with the tab visor as its network: /lot/wasm.html?disk=full&wisp=vnet:6001. See docs/guides/linuxontab.md
      --lot-open                open LinuxOnTab when the desk starts, unless the page link asks for something else. Needs --lot
      --password string         gate the served PWA behind an access password (cookie login). Empty = open. Use over --tls / behind TLS so the password isn't sent in clear
      --tls                     serve over HTTPS with a self-signed localhost cert (a real https origin for local testing — wss works, ws:// is mixed-content-blocked exactly as in prod). Accept the browser cert warning once; the cert is persisted across restarts
      --tls-cert string         PEM cert to serve TLS with instead of the self-signed localhost cert — e.g. a locally-trusted *.mesh.localhost cert (mkcert) so real-origin browse iframes load without a per-host accept. Requires --tls-key
      --tls-key string          PEM key paired with --tls-cert
      --v-origin string         the PUBLIC origin(s) of the visor app V that B's bootstrap postMessages to, e.g. https://theskywirenetwork.net. Comma-separated for several apps sharing one browse domain, or "*" to accept any parent. Only needed with --browse-origin behind a proxy; empty = derive from --addr (local)
```

## Global Flags

```
  -h, --help              show help menu
      --jq string         filter JSON output through a jq/gojq expression (implies --json)
      --json              print output as JSON
      --shape             print the output schema skeleton (zero values, all fields) instead of data
      --timeout int       RPC timeout in seconds (0 = unlimited) (default 30)
      --tui               browse commands and help interactively
      --via dmsg://<pk>   remote visor target — dmsg://<pk> or `skynet://<pk>`
```

---
_Generated by `skywire doc` — do not edit by hand._

---
name: skywire-utilities-cli
description: The small utility command groups of the skywire CLI. got (HTTP client that also speaks skynet and dmsg), gotop, util (foreach, nc, serve, jq, edit), hv (hypervisor and wasm visor tools, browser rigs), log (survey and transport log collection), plus pv, survey, version and completion. Load for HTTP over the mesh, fleet fan out, netcat, static serving, JSON filtering or log collection.
---

# skywire-cli utilities

Use `--help` for flags and `skywire doc` for the generated reference.

## JSON
- `--jq '<expr>'` is a global flag on every command and implies `--json`. Use it to filter a command's own output.
- `skywire cli util jq '<filter>' [file...]` is a standalone gojq for any JSON on stdin. It takes `-r`, `-n`, `-s`, `-c`. There is no bare `skywire cli jq`.

## got
- `got dl <URL>...` downloads in chunks with range requests and falls back to one stream. `got req <METHOD> <URL>` sends any method. `got head <URL>` shows headers. A bare `got <URL>` is `dl`.
- `http(s)://` is native. `skynet://<pk>[:port]/path` and `dmsg://<pk>[:port]/path` go through the local visor, as a single GET with no chunking, and need a running visor.
- Flags include `-o`, `-d`, `-c`, `-H 'K: V'`, `-x socks5h://host:port` (the `h` lets the proxy resolve the name, as `<pk>.dmsg` needs), `-r` resume for `dl`, `-D '<body>'|@file` for `req`.

## gotop
A terminal monitor. It needs a real TTY. By default it uses the local visor and falls back to the local machine. `--local` forces local, `--remote <pk>` reads a remote visor over dmsg, `--once` prints one text snapshot, `-x :8080` exports Prometheus metrics.

## util
- `util foreach <targets> '<cmd template>'` runs a command per target in parallel. Targets are a comma list or `@file` (one per line, `#` comments). Placeholders are `{pk}` and `{i}`. Flags are `-p`, `-t`, `--shell`, `--stop-on never|first-error|first-success`, `-q`. Exit is 0 when all succeed, 1 when any fail, 2 when any time out. `--json` emits NDJSON.
- `util nc [host] [port]` is a plain TCP and UDP netcat with `-l -u -w -k -z -v`. It uses no skywire transport. For mesh streams use `skywire cli dmsg cat`.
- `util serve <dir>` serves static files, default on a random localhost port that it prints. Expose it over the mesh with `skywire cli serve add 80 --to <addr>`.
- `util edit [file]` is a terminal editor and needs a TTY.

## hv
Tools for the hypervisor UI and the standalone wasm visor.
- `hv serve` serves the keyless wasm visor desk over HTTP. Flags include `-a`, `--tls`, `--tls-cert`, `--tls-key`, `--password`, `--harness` (dev only), `--browse-suffix`, `--browse-origin`. Restart it after an update.
- `hv notify` shows a remote visor's app notifications on this machine over SSE. `--print` leaves the desktop alone.
- Browser rigs need a browser started with `--remote-debugging-port`. `hv probe` watches one page load and streams console output and crashes. `hv eval` evaluates JavaScript in a tab. `hv shell` drives the visor shell in a tab. `hv input` sends trusted mouse input. `hv drive` holds one WebDriver BiDi session, which matters on Firefox that allows only one.
- The `visor hv` group is different. It manages the hypervisor of a running visor (`ls`, `map`, `add`, `rm`, `pair`, `enable`, `disable`).

## log
- Bare `skywire cli log` collects surveys and transport logs for every visor in the uptime tracker into per visor folders. Flags include `-l` (transport logs only), `-v` (surveys only), `-k <pk;pk>`, `-d <dir>`, `--minv`, `-n <days>`, `--all`, `--batch`, `--cleanup`, `--proxy host:port` (fetch through a warm resolving proxy to avoid cold start timeouts).
- Single visor fetches go over dmsghttp and need the CLI key to be in the target's survey whitelist. They are `log info <pk>`, `log stats <pk>`, `log uptime <pk>`, `log reward <pk>`, `log pprof <pk> <profile>`, `log file <pk>` (needs the remote to save its log), `log level <pk>` and `log heapdump <pk>`.
- `log st` renders collected surveys as a tree. `log tp` shows collected transport bandwidth.

## pv, survey, version, completion
- `pv` lists public visors from service discovery. Flags include `-t`, `-y`, `-n`, `-s`, `-c`, `-v`, `-o`.
- `survey` prints this machine's survey JSON. `-c`, `-p` and `-u` read a visor config.
- `version` shows the local build and the latest release and branch tip. `-l` skips the network.
- `completion bash|zsh|fish|powershell` prints a completion script.

## Traps
- gotop, edit and every `--live` view need a TTY and fail when headless.
- `util serve`, `util nc` and `got` use real sockets. Use throwaway localhost ports.
- `visor hv add|rm` change the hypervisor. The browser rigs and `hv serve` are safe on a running host.

# Visor logging — access paths and authentication

This page documents every way to read a Skywire visor's runtime logs, the
filters available on each path, and the trust model (which whitelist gates
which surface). Operator-facing — if you're trying to debug a misbehaving
visor on your own host or a peer's host, start here.

## Quick reference

| Goal                                              | Command                                        | Transport                              | Gate                          |
| ------------------------------------------------- | ---------------------------------------------- | -------------------------------------- | ----------------------------- |
| Tail the local visor with structured filters      | `skywire cli visor log --follow --min-level …` | Local RPC (`localhost:3435`)           | filesystem (loopback)         |
| Same, but against a *remote* visor                | `skywire cli visor log --via dmsg://<pk> …`    | Local visor → dmsg/skynet RPC bridge   | remote `hypervisors` whitelist or `dmsgpty_whitelist` |
| Download a remote visor's persistent log file     | `skywire cli log file <pk>`                    | dmsghttp / skynet → `/visor.log`       | remote `survey_whitelist`     |
| Filtered tail of remote `/visor.log` (server-side)| `skywire cli log file <pk> --min-level warn …` | dmsghttp / skynet → `/visor.log?…`     | remote `survey_whitelist`     |
| Pull a runtime pprof profile from a remote visor  | `skywire cli log pprof <pk> heap`              | dmsghttp / skynet → `/debug/pprof/heap`| remote `survey_whitelist`     |
| Fetch `/stats[/path]` / `/uptime[/path]` / `/node-info` from a peer | `skywire cli log {stats,uptime,info} <pk>` | dmsghttp / skynet                  | remote `survey_whitelist`     |
| Public health check, no auth                      | `curl dmsg://<pk>:80/health`                   | dmsghttp                               | **public** (no gate)          |

The runtime log and the persistent file are two different things. The
runtime log is a fixed-size in-memory ring buffer kept by every visor
(default 5000 entries); the persistent file `/visor.log` is the on-disk
sink that only exists when the visor is started with `-s` / `--save-log`.

---

## 1. Local visor — `cli visor log`

Speaks net/rpc to the running visor on `localhost:3435` (configurable via
`SKYWIRE_RPC` or `--rpc`). Reads from the **in-memory ring buffer**, so it
works without `--save-log`.

```
skywire cli visor log [--follow] [--min-level <lvl>] [--module <regex>] [--json] [--interval <dur>]
```

| Flag           | Purpose                                                                  |
| -------------- | ------------------------------------------------------------------------ |
| `--follow`     | Stream new entries as they arrive (polls `RuntimeLogsSince` every `--interval`, default 500ms). |
| `--min-level`  | Drop entries below this severity. `trace<debug<info<warn<error<fatal<panic`. |
| `--module`     | Regex on the `_module` field (matches whatever the logger tagged itself with). |
| `--json`       | Keep the raw structured JSON instead of pretty-printing.                 |
| `--interval`   | Poll cadence in follow mode.                                             |

If the buffer overflows between two polls a `[N entries dropped]` marker is
printed once. The buffer is bounded by `runtimeLogMaxEntries` (5000 by default).

### Reaching a remote visor with the same command

Add `--via dmsg://<pk>` or `--via skynet://<pk>`. The CLI dials the *local*
visor's RPC port, which proxies the call to the remote over a dmsg or
skywire transport. **The local visor's PK must be on the remote visor's
`hypervisors` list or `dmsgpty_whitelist` for the bridge to accept it.**
This is independent of `survey_whitelist`.

```
skywire cli visor log --via dmsg://03d1d78e7323…:44 --follow --min-level warn --module sudph
```

This gives you the same structured, filtered, follow-able view you have
locally, just routed through dmsg/skynet — the filtering happens on the
remote side, so you don't pull the whole buffer just to grep it.

---

## 2. Remote persistent log — `cli log file <pk>`

Streams the on-disk `/visor.log` over dmsghttp (or skynet, with `--via`).
Requires the remote visor to have been started with `-s`/`--save-log`; if
the file doesn't exist the endpoint returns 404 with a hint.

```
skywire cli log file <pk> [--min-level <lvl>] [--module <regex>] [--grep <regex>] \
                          [--since-line <N>] [--limit <N>] [--follow]
```

| Flag             | Maps to            | Purpose                                                  |
| ---------------- | ------------------ | -------------------------------------------------------- |
| `--min-level`    | `?min-level=`      | Keep lines `>=` severity (logrus-formatted lines only).  |
| `--module`       | `?module=`         | Regex on the `[module]` tag.                             |
| `--grep`         | `?grep=`           | Regex on the full line text.                             |
| `--since-line`   | `?since-line=`     | Skip the first N lines (1-based) — useful for resuming after a disconnect. |
| `--limit`        | `?limit=`          | Stop after N matching lines.                             |
| `--follow / -f`  | `?follow=1`        | Keep streaming new appends after EOF (tail -f mode).     |

Filtering happens **server-side** on the remote visor — over a slow dmsg
or skynet hop you don't want to ship the whole multi-MB log just to grep
it locally. When no filter flag is set the endpoint serves the raw file
via `c.File` (same behavior as before; backwards-compatible).

### Strict-level mode

When `--min-level` is set, lines that don't match the standard logrus
format (`[ts] LEVEL [module]: msg…`) are dropped — so free-form lines
from libraries that emit unstructured text can't sneak past a strict
filter. When `--min-level` is unset, unparseable lines pass through.

---

## 3. Remote pprof — `cli log pprof <pk> <profile>`

```
skywire cli log pprof <pk> {cpu|heap|goroutine|threadcreate|block|mutex|allocs|trace|cmdline|symbol} [--seconds N]
```

Streams the binary profile to stdout — pipe to `go tool pprof`:

```
skywire cli log pprof <pk> heap > heap.pprof
go tool pprof heap.pprof
```

For sampling profiles (`cpu`, `profile`, `trace`), `--seconds` controls
the sample duration. The visor caps this at 30s by default.

---

## 4. Other remote endpoints

| Endpoint                       | CLI                          | Purpose                                                                  |
| ------------------------------ | ---------------------------- | ------------------------------------------------------------------------ |
| `/health`                      | (any HTTP client)            | Public liveness + transport counts. **No auth gate.**                    |
| `/services`                    | (any HTTP client)            | Public list of forwarded ports. **No auth gate.**                        |
| `/feeds`                       | (any HTTP client)            | Public list of CXO feeds the visor publishes. **No auth gate.**          |
| `/node-info`                   | `cli log info <pk>`          | Survey (host info, geolocation, etc.).                                   |
| `/node-info/checksum`          | (HTTP)                       | SHA256 of the survey — collectors use this to skip unchanged surveys.    |
| `/stats[/path]`                | `cli log stats <pk> [path]`  | Per-visor telemetry rollup (bbolt-backed).                               |
| `/uptime[/path]`               | `cli log uptime <pk> [path]` | Three-tier uptime bitmaps + session history.                             |
| `/reward.txt`                  | `cli log reward <pk>`        | Reward address (file only present if configured).                        |
| `/visor.log`                   | `cli log file <pk>`          | On-disk log (requires `-s`/`--save-log`). Filterable; see §2.            |
| `/pty`, `/pty/*`               | (browser, dmsgpty UI)        | Web terminal — distinct auth path (see §6).                              |
| `/`                            | (browser)                    | Landing page; hides authed links from non-whitelisted PKs.               |

All `/stats/*` and `/uptime/*` handlers degrade gracefully to **503** when
the visor hasn't wired its `statsReader`/`uptimeRecorder` (e.g., the bbolt
store didn't open).

---

## 5. Auth model — which whitelist gates which surface

The visor has **three** distinct PK whitelists. They overlap in practice
but don't have to.

| Whitelist                | Config field                 | Gates                                                                                 |
| ------------------------ | ---------------------------- | ------------------------------------------------------------------------------------- |
| `survey_whitelist`       | `survey_whitelist`           | HTTP endpoints on the logserver: `/visor.log`, `/debug/pprof/*`, `/stats/*`, `/uptime/*`, `/node-info`, `/reward.txt`. **Empty list = publicly accessible.** |
| `dmsgpty_whitelist`      | `dmsgpty.whitelist`          | `/pty` (web terminal) and the sftp subsystem on the dmsgpty mux (PR #3002). **Empty list = pty/sftp disabled.** |
| `hypervisors`            | `hypervisors[]`              | RPC over the `--via dmsg://` / `--via skynet://` bridge. Local visor accepts the bridge dial only from PKs in the configured hypervisor set (and itself). |

`EffectiveSurveyWhitelist()` returns the union of the deployment-supplied
list (`survey_whitelist`, refreshed from conf service) and operator-added
entries (`user_survey_whitelist`, persisted across config refresh). The
same pattern exists for `user_route_setup_nodes` and `user_transport_setup`.

### Important quirks

- **An empty `survey_whitelist` means *everyone* can read `/visor.log`** —
  any peer who can reach you over dmsg/skynet can pull the file. Operators
  who don't want that should populate the whitelist with the PKs they
  trust (their own + maintenance hosts + survey collectors).
- **`dmsgpty_whitelist` is independent from `survey_whitelist`.** A peer
  on the survey list cannot necessarily open `/pty`; a peer with pty
  access cannot necessarily fetch `/visor.log`. This is deliberate — pty
  is a shell, survey is read-only telemetry.
- **The RPC bridge auth is *neither* of the above** — it's the
  `hypervisors` list (or `dmsgpty_whitelist` as fallback in some
  configurations). Don't assume "I'm on the survey whitelist" gets you
  `cli visor log --via dmsg://...`.

### Adding yourself to a remote's `survey_whitelist`

Whitelists in this repo can be updated at runtime via:

```
# Append a PK
skywire cli config update --user-survey-whitelist add <pk>

# Remove
skywire cli config update --user-survey-whitelist remove <pk>
```

The change writes to `user_survey_whitelist`, which is preserved across
the periodic conf-service refresh.

---

## 6. The web terminal at `/pty`

Distinct from logging but worth knowing because it shares the same HTTP
listener. `/pty` and `/pty/*` are gated by `dmsgpty_whitelist` separately
from the survey-whitelist — that's by design, since a shell is much more
powerful than read-only telemetry. The route returns 404 (not 403) when
no pty handler is wired, so a misconfigured deployment can't accidentally
expose a shell to a peer on the wrong whitelist.

---

## 7. Common operator recipes

```bash
# Tail the local visor for everything sudph-related, just warnings+.
skywire cli visor log --follow --min-level warn --module 'sudph'

# Same, but on Beta's prod02 visor (works when this host is on Beta's
# `hypervisors` whitelist).
skywire cli visor log --via dmsg://02f9aa58…:44 --follow --min-level warn --module sudph

# Pull the last 200 lines containing "BindSUDPH" from a peer's visor.log,
# filtered at the source so you don't ship the whole 50MB file.
skywire cli log file 02f9aa58… --grep 'BindSUDPH' --limit 200

# Watch for new error-level entries on a peer in real time.
skywire cli log file 02f9aa58… --follow --min-level error

# Heap snapshot of a remote visor:
skywire cli log pprof 02f9aa58… heap > peer-heap.pprof
go tool pprof peer-heap.pprof
```

---

## 8. Diagnosing access failures

| Symptom                                                 | Likely cause                                                                   |
| ------------------------------------------------------- | ------------------------------------------------------------------------------ |
| `cli visor log` fails: `RPC connection failed`          | Visor not running, or `--rpc`/`SKYWIRE_RPC` points at the wrong address.       |
| `cli visor log --via` fails: `bridge: write header`     | Local visor not running. The bridge needs a *local* visor to proxy through.    |
| `cli log file <pk>` returns `visor.log not found`       | Remote was started without `-s`/`--save-log`. The runtime ring buffer is still readable via the `--via` RPC path. |
| `cli log file <pk>` returns 403 / hangs                 | You're not on the remote's `survey_whitelist`. Ask the operator to add your PK or check `cli config view`. |
| `cli visor log --via` returns 403                       | You're not on the remote's `hypervisors` list. Survey-whitelist doesn't gate the RPC bridge. |
| `cli log pprof <pk> trace` truncates                    | Visor caps trace duration at 30s. Drop `--seconds` or pull a smaller window.   |

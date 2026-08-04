# Skywire reward system — operations & deployment

How the daily reward system is built, deployed, and run on the reward host. See
[mainnet_rules](mainnet_rules.md) for the eligibility ruleset; this document covers
the moving parts and how to operate them.

Everything below is implemented as subcommands of the single `skywire` binary
(`skywire cli reward*`), so the reward system is reproducible from source — no
separate repo or host-only scripting is required.

## Components

| Concern | Command / unit | Notes |
|---|---|---|
| User-facing | `skywire cli reward` | set / check a visor's reward address |
| Survey + tp-log collection | `skywire cli log` | fetch `/health`, `/node-info`, tp logs from visors |
| Bandwidth collection | `skywire cli rewards bw-collect` | per-visor bandwidth from TPD |
| Reward calculation | `skywire cli rewards` (calc) / `skywire cli rewards run` | see **Calculation** |
| Web UI / API / survey ingest | `skywire cli rewards ui` (`server.go`) | dmsg + HTTP server; `/stats`, `/skycoin-rewards*`, `/log-collection*`, `/transport-graph`, `POST /node-info` |
| Reward-eligibility floor | auto-derived | latest GitHub release whose `published_at` ≤ 14 days ago (see mainnet_rules) |

### Data flow (one daily cycle)

```
collect surveys + tp logs        ──► log_collecting/<pk>/{health.json,node-info.json,<date>.csv}
  (skywire cli log)                     │  prune (min-version, age-out, empty/404, invalid-json)
                                        └► rsync/stage ──► log_backups/<pk>/…   (authoritative for the calc)
collect bandwidth (bw-collect)   ──► bandwidth data
fetch uptime (TPD /uptimes?v=v2) ──► hist/<date>_ut.txt   (PKs meeting the daily uptime bar)
calculate                        ──► hist/<date>_{ineligible,shares,rewardtxn0,stats}.csv
broadcast (operator/reward UI)   ──► hist/<date>.txt      (marks the day done; calc bails if present)
```

The calc reads **`log_backups`**, so surveys must be staged there before it runs.

## Survey collection: two mechanisms

### 1. PULL — `skywire cli log` (the collector)

Fetches from every visor the TPD-integrated uptime tracker saw **today** (not just
those flagged online — a visor can meet the daily uptime bar yet read offline at the
instant of a poll; gating on that flag silently dropped it, earning uptime but no
reward). The `/health` fetch is both the reachability gate and the authoritative
version source (`build_info.version`) — survey collection **is** the version-
eligibility gate (version is not re-checked at calc time), so an ineligible-version
visor is skipped without its survey being stored.

Two transport modes:

- **default** — `cli log` opens its own dmsg client and dials visors as
  `dmsg://<pk>:80/…`. First contact to a never-dialed peer is a *cold* session that
  can exceed the per-request timeout, so collection runs in **two passes** (pass 1
  warms sessions, pass 2 retries the misses).
- **`--proxy host:port`** — fetch through a **dmsgweb SOCKS5 resolving proxy** (a
  running `skywire dmsg web` with a survey-whitelisted key) that already holds *warm*
  dmsg sessions; visors are reached as `http://<pk>.dmsg/…` and the proxy resolves +
  dials them. This is the reward host's mode (it runs such a proxy) and avoids
  cold-session first-contact timeouts entirely.

`--cleanup` (default on) prunes both `log_collecting` and `log_backups`
(below-min-version surveys, >`--max-age` json, empty/404-sized/invalid-json files,
CSV-header strip) and then stages `log_collecting → log_backups` (Go-native, additive
merge). So:

```
skywire cli log --proxy 127.0.0.1:4443 --cleanup --minv auto --prune-below-version auto
```

is a complete replacement for the legacy host-only `fetch_surveys.sh` + `getlogs.sh`
`_getlogs`/`_cleanup` bash — collect + version-gate + retry + prune + stage-to-backups,
all in Go. `--minv auto` / `--prune-below-version auto` resolve the dynamic 14-day
reward floor from GitHub releases (the Go port of `getlogs.sh`'s `_compute_minversion`),
so the collector enforces the floor itself — **required**, because the calc does not
re-gate on version (survey collection is the sole version-eligibility gate).

### 2. PUSH — visor → reward server (preferred, low-load)

Visors push their own survey to the reward system over dmsg, so collection no longer
depends on a pull snapshot:

- Visor side (`pkg/visor/reward_push.go`): a loop that GETs the checksum the reward
  system already holds for its PK (`GET /node-info/stored-checksum`, scoped to the
  dmsg-authenticated sender) and `POST /node-info`s the full survey only when it
  differs (conditional PUT). No-op until a reward address **and** `reward_system_dmsg`
  are set.
- Server side (`server.go` `POST /node-info`): stores the survey under the
  **dmsg-authenticated** source PK (a visor can only write its own), version-gated
  (`--survey-min-version`), atomically into `log_backups/<pk>/node-info.json`. Returns
  `{"eligible":bool,"reason":...}` so the visor can show reward-eligibility in the
  hypervisor UI (a rejected push → red mark, distinct from the hyphen for no address).

Push makes the reward host's PK (`reward_system_dmsg` in the deployment services
config) the ingest point; the PULL collector remains the fallback for visors that
don't push yet.

## Calculation

Two entry points share the same reward math (`countFrequency`, `computePoolShares`
→ `calcPresenceShare` + `applyRegionalSaturation`, `computePoolRewards`), so per-IP
share (8/IP), MAC-dedup, and regional saturation are identical between them:

- **Legacy / production** — `skywire cli rewards -b -B 0 … -p log_backups` (the
  `reward.sh` template printed by `skywire cli rewards script reward | bash`). Proven;
  currently live.
- **Integrated (Go one-shot)** — `skywire cli rewards run [--proxy host:port]`
  (`runday.go`): collect + bw-collect + fetch-UT + calc + write, in-process.
  `--skip-collect` reuses on-disk data. **Validate before switching the live service**
  (see below) — the core share math is shared, but confirm the pool composition matches
  production `-b -B 0` on a known day.

## Reward-host deployment (magnetosphere)

systemd units (host `WorkingDirectory` = the reward working dir containing
`log_collecting/`, `log_backups/`, `hist/`):

- **`dmsgweb-surveys.service`** — `skywire dmsg web -q 4443` with a survey-whitelisted
  key (`DMSGWEB=dmsgweb-survey-wl.conf`). The SOCKS5 resolving proxy at
  `127.0.0.1:4443` that collection fetches through. Keep the SK in an env file outside
  the unit.
- **`skywire-reward.timer`** → **`skywire-reward.service`** — hourly. Runs the daily
  cycle: collect → bw-collect → calc.
- **`fiberreward.service`** — `skywire cli rewards ui -W <dir>` — the web UI / API /
  survey-ingest server.

### Prerequisite: the host binary must have the code

The reward host builds `skywire` from `develop` via `skywire-update`. The collector
`--proxy`/`--cleanup`-with-sync and `rewards run --proxy` require that build. Confirm
before deploying:

```
skywire cli log --help | grep -- --proxy      # must list --proxy
```

### Live swap (retire the host-only bash collector)

Change `skywire-reward.service`'s `ExecStart` from the legacy bash pipeline:

```
/bin/bash -c 'source getlogs.sh && _getlogs && _cleanup && skywire cli rewards bw-collect && skywire cli rewards script reward | bash ; exit 0'
```

to the Go collector plus the (proven) calc:

```
/bin/bash -c 'skywire cli log --proxy 127.0.0.1:4443 --cleanup --minv auto --prune-below-version auto && skywire cli rewards bw-collect && skywire cli rewards script reward | bash ; exit 0'
```

`systemctl daemon-reload`, then confirm the next hourly run populates
`log_backups/<pk>/node-info.json` and writes `hist/<date>_*.csv`. This retires
`fetch_surveys.sh` and `getlogs.sh` while keeping the proven calc.

Later, once `rewards run` is validated (below), the whole `ExecStart` collapses to:

```
/usr/bin/skywire cli rewards run --proxy 127.0.0.1:4443 --minv auto
```

### Validating `rewards run` before switching the calc

On a **scratch copy** of a past day's inputs (never the live `hist/`, and never for a
date already broadcast — the calc bails when `hist/<date>.txt` exists), run:

```
skywire cli rewards run --date <YYYY-MM-DD> --skip-collect -H <scratch-hist> -p <scratch-log_backups>
```

and diff `<scratch-hist>/<date>_rewardtxn0.csv` against the known-good
`hist/<date>_rewardtxn0.csv` produced by the live `script reward | bash`. Matching
outputs are the go/no-go for switching the live `ExecStart` to `rewards run`.

## Non-payment diagnosis (operator checklist)

A visor that met uptime but was not paid is, in order of likelihood:

1. **Survey not collected** — no `log_backups/<pk>/node-info.json`. The calc now emits
   a `survey not found` row in `hist/<date>_ineligible.csv` instead of silently
   dropping it. Check the collector logs / whether the visor was reachable.
2. **Ineligible** — see `hist/<date>_ineligible.csv` reason (arch, IP shape, missing
   UUID/MAC, VM hypervisor, invalid reward address, version below floor).
3. **Per-IP share** — visors sharing a public IP scale down only past the 8/IP limit;
   they are never zeroed. "Only N of my M visors paid" behind one IP is expected only
   if a survey/uptime gap dropped the rest (see #1), not the per-IP rule.

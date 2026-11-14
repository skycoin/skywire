---
name: skywire-rewards-cli
description: Reference for the skywire rewards CLI. Two groups, `reward` (node operator, set or read the reward address, payout history, rules) and `rewards` (reward system operator, calculate and distribute rewards, collect transports and bandwidth, run the hourly cycle, the metrics UI, systemd units, telegram bot). Load for reward addresses, uptime to reward calculation, or the reward system service.
---

# skywire rewards CLI, reward vs rewards

Binary: `skywire cli ...` (or a built `skywire-cli` directly). RPC default `localhost:3435`.

## The two groups (NOT redundant, different audiences)
- **`reward` (singular)** = the **node-operator** command. Manage THIS visor's reward
  address + look up its payout history. This is what a normal operator uses.
- **`rewards` (plural)** = the **reward-system operator** toolchain. The machinery that
  RUNS daily reward distribution (calculate shares, collect transports/bandwidth, serve
  the UI, systemd/telegram). This is what the deployment host runs.

They intentionally coexist; a full merge would break the two systemd units + scripts that
call `skywire cli rewards ...`. Cross-referenced in each `--help`. Both live under the
"rewards" CLI group. Source: `cmd/skywire-cli/commands/{reward,rewards}/`.

## Reward system model (uptime → reward)
Daily distribution. Eligibility = uptime (from the uptime tracker, now TPD-integrated so
>=2 transports is gated inherently) + a valid reward address set on the visor + a hardware
survey that passes service-config checks + minimum skywire version. Two reward **pools**
split by CPU architecture (pool 1 = SBC/ARM-ish, pool 2 = amd64/386), each with a yearly
budget (`--year`, default 408000). Regional **saturation** derating (`--sat-exp`) prevents
one country dominating. Optional **bandwidth** pool (`--require-bw`) credits sender-side
bytes. See `reward rules` for the human eligibility rules.

## `reward` (singular), node operator
- **`reward`** (no args) → prints the currently-set reward address (RPC first, then the
  local `<localPath>/reward.txt`). `reward -r/--read` = same, explicit. `--json` wraps it.
- **`reward <address|xpub>`** → SET the address: a BIP44 **account-level** xpub (path
  `--path=0` depth 3, NOT the `0/0` external-chain xpub). A single skycoin address needs
  `--accept-risk` (`SetSingleRewardAddress` / `HVSetSingleRewardAddress`); the visor asks the
  skycoin node first and refuses one that has sent coins, or when the node cannot answer.
  From reward day 2026-11-15 a single address that has sent coins earns nothing. Writes via
  visor RPC (no sudo) + mirrors to the local file. MUTATING.
- Hidden flags (show with `reward --all`): `-a/--address`, `-o/--out <file>`,
  `-r/--read`, `-d/--delete` (opt out, removes the file / RPC-deletes). Visible bulk flags:
  `--pks pk1,pk2` and `--all-visors` set the address on many visors **via a hypervisor**
  (needs hypervisor RPC; uses `HVSetRewardAddress`/`HVListVisors`). `--all` unhides+exits.
- **`reward lookup [pk...]`** → query the reward SERVER over **dmsg** for a PK's per-day
  payout history (date, amount SKY, share, sent, txid). Reads the server's already-published
  results (unlike `rewards -k` which recomputes locally). Flags: `-n/--days N` (1-90, default
  7), `-S/--server dmsg://<pk>:<port>` (default = `deployment.Prod.RewardSystemDmsg`),
  `-f/--file <pks.txt>` (one PK per line; first whitespace field, `#` comments ok),
  `--rewarded-only`, `-s/--loglvl` (default **error** so dmsg bootstrap chatter doesn't bury
  the table, raise to `debug` to diagnose dmsg reachability). Spins up an ephemeral dmsg
  client each call (~5-30s). `--json` gives structured per-PK results with a per-PK `error`
  field (one bad key never aborts the batch).
- **`reward rules`** → mainnet eligibility rules (embedded markdown). `-r/--raw` = markdown
  source, `-l/--html` = HTML fragment, default = terminal-rendered.

## `rewards` (plural), reward-system operator
- **`rewards`** (bare) → calculate ONE day's rewards from data already on disk. Needs
  `--lpath` surveys (default `log_collecting`) + `--utfile` (default `ut.txt`). Key flags:
  `-d/--date` (default yesterday), `-k/--pk` (check one PK), `-y/--year`, `-p/--lpath`,
  `-u/--utfile`, pool arch lists `-w/--a1` / `-x/--a2`, `-n/--noarch`, `--sat-exp`/`-S`,
  `--require-bw`/`-b` + `--min-bw`/`-B` + `--bw-sat-exp` + `--no-bw-pool`,
  `--require-tp`/`-t` (DEPRECATED, UT data now gates transports) + `--tp-hist`/`-T`,
  output-hiding `-0/-1/-2`, `-e/--err` (each prints one section of the same calculation).
  `-s/--loglvl`. Also writes the per-pool files to `--tp-hist` unless `-k`.
- **`rewards run`** → the hourly cycle of `skywire-reward.service` IN-PROCESS, through the
  same calculation as the bare command: `cli log --no-fetch --prune-below-version auto`
  (clean pushed surveys) → `bw-collect` (today; a failure is logged and the cycle goes on, pool 2 then repeats pool 1 for a date with no bandwidth file) → stop if `hist/<date>.txt` exists (paid) →
  UT fetch as `cli ut tpd --since/--until <date>` (`hist/<date>_ut.txt`, `_ut.json`,
  `hist/uptimes.json`) → calculate → write
  `hist/{date}_{ineligible.csv,shares.csv,rewardtxn0.csv,stats.txt}` + per-pool files, byte
  compatible with the four `rewards -b -B 0 ... -e/-20/-10/-12` calls of the reward script.
  Takes the bare command's calculation flags with the script's defaults (`-b` true, `-B 0`,
  `-p log_backups`), plus `-H/--hist`, `-u/--utfile` (skip the UT fetch), `--skip-collect`,
  `--prune-below-version` (default `auto`), and `--fetch` (fetch surveys with `cli log --survey`
  instead, with `--minv` and `--proxy host:port`). Date defaults to yesterday UTC.
- **`rewards tp-collect`** → fetch per-key transport stats from TPD
  (`/all-transports/per-key-stats`, cached 5min in `/tmp/tpd.json`), append PKs with
  >= `-m/--min` (default 2) p2p transports (dmsg excluded) to `hist/YYYY-MM-DD_transports.txt`.
  `-a/--all` = print all qualifying (no write). `-n/--no-cache`, `-p/--hist`, `--rpc`, plus
  fetch flags `--no-cxo/--no-rpc/--no-dmsg/--sk/--config`.
- **`rewards bw-collect`** → fetch per-transport bandwidth from TPD
  (`/metrics?days=1&bandwidth=true&edges=true`), exclude same-LAN sybil edges (PK→IP map from
  surveys), record sender-pays bytes → `hist/YYYY-MM-DD_bandwidth.json` (v2 schema, cache
  `/tmp/tpd_bandwidth_v2.json`). Flags `-b/--min-bw` (default 64), `-l/--lpath`, `--days`
  (<=35), `--date`, `-n/--no-cache`, `-p/--hist`, `--rpc` + fetch flags. **Always writes**
  (no `--all`); run from a scratch dir if you don't want the file.
- **`rewards svc`** → verify a visor's survey service-config against the prod deployment
  (`node-info.json` under `--lpath/<pk>/` vs `services-config.json`; detects http vs dmsghttp;
  stun_servers omitted). Flags `-k/--pk`, `-p/--lpath`, `-s/--loglvl`.
- **`rewards ui`** → the reward-system **web UI + metrics server**
  (served by the fiber based reward service). `--bind host:port` (default `:80`; `-p/--port` deprecated),
  dmsg serve `-d/--dport`/`-e/--dsess`/`-D/--dmsg-disc`/`-s/--sk`, `-w/--wl` broadcast
  whitelist, `-W/--wd` workdir, `--login-node`/`--skycoin-node`/`--login-chain-flags`,
  `--canonical`, `--survey-min-version`, `--health-only`. (Also embeddable in the visor via
  `ConfigureAndBuild`.)
- **`rewards loginchain`** → start the 2-node login chain (block publisher :6001/:6421 +
  peer :6002/:6422) for wallet-ownership verification. Wipes chain data each start; preserves
  `login_genesis.json`. UI connects with `--login-node http://127.0.0.1:6422`.
- **`rewards systemd`** → write `skywire-reward.service` (oneshot `rewards run`) +
  `.timer` (hourly) + `fiberreward.service` (the `ui`) to `-o/--out` (default
  `/etc/systemd/system`; use `-o /dev/stdout` to preview, note it double-prints via `.Tee()`).
  `-u/--user`, `-p/--path`, `-s/--skyenv` (default `fr.conf`).
- **`rewards script {getlogs|reward}`** → print the legacy bash scripts (getlogs.sh /
  reward.sh) to pipe to bash. `getlogs -m <ver>` / `reward -d <date>` prepend a var. Superseded
  by `rewards run`; kept for reference.
- **`rewards bot`** → telegram notifier. Watches `-w/--watch` (reward txid file); posts
  per-day stats on each new broadcast. Needs env `TG_BOT_TOKEN` + `TG_CHAT_ID`.

## Gotchas
- `reward` `Long` help dials the visor RPC to show the current address, now done **lazily**
  (only on `reward --help`), previously ran on every skywire-cli invocation.
- `reward lookup` defaults `--loglvl error`; the dmsg per-request client is ephemeral so
  transient `dmsg error 100/202 - entry not found / cannot connect to delegated server` can
  happen, just retry (a later attempt succeeded and returned the table).
- Flag-letter inconsistency across the plural group (backward-compat, do NOT "fix"): `-p` =
  `--lpath` in `rewards`/`svc` but `--hist` in `tp-collect`/`bw-collect`; `--lpath` is `-l` in
  `bw-collect`. `--lpath` default is `log_collecting` in `rewards` and `svc`, but `log_backups` in `run` and `bw-collect`.
- `rewards run` and the collect commands do REAL network fetches and write to
  `./hist` (gitignored). Run from a scratch dir when testing.

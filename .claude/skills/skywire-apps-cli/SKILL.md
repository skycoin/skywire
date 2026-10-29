---
name: skywire-apps-cli
description: >-
  Verified surface of the skywire "apps" CLI groups, `skywire cli skychat`
  (messaging: send/listen/events/history/status/alias/chat-TUI/pair/group/
  voice/send-file), `skywire cli skycoin` (skycoin-daemon instances), and
  `skywire cli mail` (embedded SMTP→skywire bridge). Load when driving or
  debugging any of these subcommands. Source: cmd/skywire-cli/commands/
  {skychat,skycoin,mail}/.
---

# skywire apps CLI (skychat / skycoin / mail)

Three command groups wired into the main CLI tree. All examples use
`skywire cli <group> …` (the installed binary; `skywire-cli <group> …`
is equivalent).

## Two transport backends, know which one a command uses

- **HTTP → local skychat app on `--addr` (default 127.0.0.1:8001)**:
  `skychat send / listen / events / history / status / pair* / chat /
  send-file`. These need the **skychat app running**; if it is down you get
  `dial tcp 127.0.0.1:8001: connect: connection refused`. `--addr` overrides
  the app address. The global `--via dmsg://<pk>` / `--json`-jq plumbing does
  NOT reach these (they hand-roll their own `--json`; see Gotchas).
- **net/rpc → local visor (clirpc default addr)**: `skychat group* /
  voice*`, all of `skycoin daemon*`, all of `mail`. These reach the **visor
  RPC** and work whenever the visor is up (independent of the :8001 app).
  Note: the visor RPC default is NOT `localhost:3435` over IPv6, probing
  `--rpc localhost:3435` can be refused while the default clirpc path works.

## skychat, messaging (~parent flags: `--addr`, plus global `--json/--jq/--shape/--via`)

| Subcommand | Purpose | Key flags | Mutating? |
|---|---|---|---|
| `send` | Send a 1:1 message (chat-msg envelope, waits for ack) | `-t/--to` (pk or alias), `-m/--msg` (req), `-n/--net skynet\|dmsg` (def skynet), `-w/--wait` (def 5s; 0=fire-and-forget), `-r/--retries` (def 1), `--verbose`, `--via tcp://<pk>@host:port`, `--sk`, `-c/--config` | yes (sends) |
| `listen` | Stream inbound messages via SSE | `-n/--net`, `--from`, `--raw` (vs global `--json`; mutually exclusive) | no |
| `events` | NDJSON structured event feed (`/events`) with lossless seq resume | `--channel dm,group,pair,system,all`, `--since <seq>` | no |
| `history` | Print persisted 1:1 history (`/history`) | `--peer`, `--limit` (1-1000, def 100), `--since <dur>` | no |
| `status` | skychat app health snapshot (`/status`) | (global `--json`) | no |
| `alias` | Local PK addressbook (`$XDG_CONFIG_HOME/skywire/skychat-aliases.json`) | `add NAME PK`, `ls`, `rm NAME` | local file only |
| `chat` | Interactive bubbletea TUI (picker or `-t` 1:1) | `-t/--to`, `-n/--net` | TTY |
| `send-file <path>` | Send a file P2P over transport | `-t/--to`, `-g/--group` | yes |
| `pair …` | CXO-backed 1:1 (needs app `--pair-enable`) | see pair table | yes (add/send/…) |
| `group …` | D1 owner-centric group chat over CXO | see group table | mixed |
| `voice …` | 1:1 voice calls | see voice table | yes (call) |

**alias resolution**: any `-t/--to/--peer/--from` accepts a 66-hex PK *or* an
alias name. A full hex PK always resolves directly (never shadowed by a
same-named alias). Alias names must not start with a digit. `alias ls --json`
prints the raw `{name:pk}` map.

**send `--via tcp://<pk>@host:port`**: bypasses the local app entirely, the
CLI dials the remote chat-app's noise-TCP listener directly (survives visor /
dmsg outages). Identity from `--sk` → `DMSGCURL_SK` → `-c skywire.json`. When
set, `-t` is optional (PK comes from `--via`). NOTE this shadows the global
`--via dmsg://<pk>` for `send` only.

### skychat pair (needs skychat app started with `--pair-enable`; 503→"pair-rpc-disabled")

`add -t <pk>` · `accept -f/--from <pk>` · `decline -f/--from <pk>` ·
`list` · `send -t <pk> -m <msg> [--verbose]` · `poll [--since RFC3339] [--limit 1-1000]` ·
`remove -t <pk>`.

### skychat group (all via visor RPC)

Read-only: `list`, `info <id>`, `address <id>`, `resolve <addr|invite>`,
`catalog [host-pk]` (omit host = own listing), `history <id> [-n/--limit] [--list-groups] [--raw]`,
`listen [--since RFC3339] [--interval 1s] [--raw]`.
Mutating: `create -n/--name (req) [-k/--kind public|private|channel] [-m/--mode (deprecated alias for --kind)] [--member <pk>…] [--listed] [--no-peer-backfill]`,
`invite <id>`, `publish <id> on|off`, `join <invite|addr>`, `ask-again <id>`,
`add <id> <pk>`, `promote <id> <pk>`, `demote <id> <pk>`,
`peer-backfill <id> on|off`, `rotate-key <id>`, `send <id> <text…>`,
`unsend <id> <ts-unixnano>`, `leave <id>`, `delete <id>` (leaves a `revoked`
tombstone in `group list`).

### skychat voice (all via visor RPC)

`call <pk>` · `hangup <id>` · `list` · `incoming` · `answer <id>` ·
`decline <id>` · `dialing` · `ringback` · `spectrogram [--monitor] [--call <id>]` (tcell TUI, Linux
audio, TTY).

## skycoin, skycoin-daemon instances (visor RPC)

`skycoin daemon list [--json]` · `add <instance-name> [--fiber-toml] [--port]
[--data-dir] [--api-sets] [--autostart] [--start]` · `start <name>` ·
`stop <name>`. Instances are named `skycoin-daemon-<instance-name>`; port
auto-allocates to `6420 + 2*N`. `add`'s success line goes to **stderr**.

## mail, the visor mailbox and the SMTP bridge (visor RPC)

Every visor has a mailbox that receives mail for `<anything>@<base32-pk>.skynet` (or `.dmsg`) in a Maildir. No MTA or certificate is needed, because the transport authenticates the sender key. Bare `mail` prints the state of the mailbox and the bridge.

- Mailbox: `mail inbox`, `mail read <id>`, `mail attachment`, `mail rm`, `mail send <addr> -s <subject> -m <text>`, `mail sent`, `mail settings` (show or change live, including `enable=false`).
- Bridge for a co-located Postfix: `mail up` and `mail down`. The bridge state is runtime only and returns to the config default on restart. The Postfix transport_map line is `.skynet  relay:[127.0.0.1]:1025`. `mail --json` dumps the status of the embedded proxies and the bridge.

## Gotchas / known limitations

- **`--jq` / `--shape` are ignored by the hand-rolled-JSON commands**:
  `alias ls`, `history`, `status`, `listen`, `pair list/poll`,
  `group history/listen`. They read the `--json` bool directly and marshal
  their own shape, so `--jq`/`--shape` silently fall through to the human
  table. Commands that route through `internal.PrintOutput` (`group list/info/
  catalog/resolve`, `skycoin daemon list`, `mail`, `voice list`) honor `--jq`.
- **`group catalog` with no host arg** resolves the local visor's own PK via
  an `Overview()` RPC first, a bare zero `cipher.PubKey` cannot be
  gob-encoded over net/rpc ("Invalid public key"). (Fixed; pre-fix builds
  error on the no-arg form.)
- **`listen` / `events` / `group listen` loop forever** (auto-reconnect with
  1s→30s backoff). Wrap in a never-timeout supervisor for agent use; a
  short-lived task wrapper silently drops messages after it expires.
- Default text output of `listen` / `group listen` / `group history` escapes
  `\n`/`\r` to keep one line per event (log-aggregator safe); pass `--raw`
  for verbatim multi-line, or `--json` (NDJSON, natively multi-line-safe).
  `--raw` + `--json` together is an error.

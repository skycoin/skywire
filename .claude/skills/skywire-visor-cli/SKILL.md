---
name: skywire-visor-cli
description: The `skywire cli visor` command group. Query and control a running visor over its local RPC or a remote visor with --via, with a read only versus mutating classification so you know what is safe against a live node. Load when driving a visor from the CLI.
---

# skywire cli visor

Every command here talks to the visor RPC (`--rpc`, `SKYWIRE_RPC`, default `localhost:3435`). Add `--via dmsg://<pk>` or `skynet://<pk>` for a remote visor. Output flags are `--json`, `--jq <expr>` and `--shape`. `skywire cli visor --help` lists the groups, and `skywire doc` has the full reference.

R means read only and safe on a live visor. M means it changes or loads the visor.

## Lifecycle
- `halt` (M), `suspend` (M, tears down networking and keeps the RPC), `resume` (M), `reinit -m <module>` (M), `start` (M).
- `ready` waits for startup to finish. `suspended` prints true or false (R).
- `cli halt`, `cli pk` and `cli status` are top level aliases.

## Identity and network state (R)
- `info` (also `-L` for a live view), `pk`, `pk dnslabel [key]`, `ver`, `user`, `ports`, `dmsg-servers`, `ip`.
- `state` is the one call runtime snapshot. See skywire-visor-state.
- `services` lists the deployment services this visor embeds. `services start|stop|restart <name>` (M) controls a service that has its own key. A service mounted on the visor runs with it.
- `network show` (R) prints the public IP and how it was learned, the NAT class, the LAN addresses after the virtual interface filter, the addresses each transport type last bound with, and the last network move. `network moved [--reason r]` (M) asks for the refresh a move triggers: dmsg reconnect, STUN probe, public IP lookup, and a fresh address resolver bind of every type. It also runs on its own when the address watcher (`transport.network_watch_interval`, default 12s) or the public IP recheck (`transport.public_ip_check_interval`, default 12m) sees a change, and when the Android app posts `/api/dmsg/reconnect`. Both intervals change live with `config set`, and a negative value turns one off.
- `skyenv [flag=value ...]` shows or edits the visor's `skywire.conf`, the settings every config regen is built from. `--unset <flag>` restores a default. Edits apply at the next regen. With `--via` it edits a remote visor's file, so treat it as M.

## Apps
- R: `app ls`, `app conns <name>`, `app log <name> <RFC3339 time|beginning>`.
- M: `app start|stop|restart|rm <name>`, `app add`, `app env`, `app args`, `app register|deregister`.
- `app start` takes `--internal`, `--external`, `--existing-tp`, `--local-route`, `--min-hops`, `--mux-mode` and `--routing-policy`. The tunnel count is a `proxy start` option.
- `app arg autostart|killswitch|secure|whitelist|netifc|pk|routing-policy <name> <value>` changes one setting at runtime.

## Hypervisor, CXO, pair, reward
- `hv`. R: `pk`, `cpk`, `status`, `ls` (`--flat`, `--load`), `map`. M: `enable`, `disable`, `add <pk>`, `rm <pk>`, `pair`, `passwd`. `ui` opens the browser and `tui` opens a terminal UI.
- `cxo status`, `cxo feeds ls` (R). `cxo refresh`, `cxo fetch`, `cxo feeds add|rm` (M).
- `pair ls` (R). `pair add|rm|send|delete|poll` change state.
- `reward` shows reward history over the reward system (`-d`, `-k`, `-a` for all hypervisor visors).

## Diagnostics
- R: `go` (runtime memory stats), `goroutines` (summary of the pprof goroutine dump, works even if the RPC hangs), `log` (`-f`, `-l`, `-m`), `doctor` (green, yellow, red rollup), `whois <pk>`, `uptime` (session history).
- M: `ping`. It creates routes and load, so it is not read only. Subcommands include `test`, `bandwidth`, `mux-bw`, `mux-bw-tui`, `tree`, `stop-all`.

## Quick read only health look
```
skywire cli visor info
skywire cli visor doctor
skywire cli visor state --select health,services
skywire cli visor hv ls --load
skywire cli visor goroutines --top 10
```

## Traps
- `cli route` and `cli tp` are top level groups, not under `visor`. There is no `visor top`.
- A subcommand missing from the group map in `cmd/skywire-cli/commands/visor/zgroups.go` still works but is hidden from `visor --help`.

---
name: skywire-cli
description: Core reference and index for the skywire CLI. Global flags and output, visor and network inspection (visor, tp, rg, hv ls, sd, proxy list, log, survey), and pointers to the focused skywire skills. Load for general skywire CLI work.
---

# skywire CLI, core and index

The CLI is `skywire cli ...` in the single `skywire` binary (build it with `go build .` at the repository root). The visor RPC defaults to `localhost:3435` and is set with `--rpc` or `SKYWIRE_RPC`. The full generated command reference is `skywire doc`, and `<cmd> --help` is always current. These skills only cover what the help does not explain.

## Global conventions
- Most commands take `--via dmsg://<pk>` or `--via skynet://<pk>` to run against a remote visor.
- Output flags are `--json`, `--jq '<expr>'` (implies `--json`) and `--shape` (prints the field skeleton, so use it before writing a `--jq`).
- Run a command group with no arguments to see its subcommands.
- Parse `--json` output with `--jq`. Do not grep the text tables.

## Other skills
- skywire-visor-state, one call runtime snapshot of a visor.
- skywire-visor-cli, the `visor` group and which commands are safe on a live node.
- skywire-routing, skywire-route-cli, mux, routing policy and route inspection.
- skywire-transports, skywire-transport-setup, listing and creating transports.
- skywire-forwarding, proxy, skynet, serve, resolver, vpn.
- skywire-pty, remote commands and shells.
- skywire-deployment-services, skywire-discovery-cli, service health, pprof and logs.
- skywire-dmsg-cli, skywire-config-cli, skywire-rewards-cli, skywire-apps-cli, skywire-utilities-cli, skywire-devflow.

## Visor and identity
- `skywire cli visor ver` (not `version`), `visor info`, `visor pk`, `visor halt`, `visor ready`, `visor suspend`, `visor resume`.
- Top level aliases exist for `halt`, `pk` and `status`.
- `visor log` streams runtime logs. `visor app ls`, `visor app log <name> <RFC3339 time>` needs a start time, for example `date -u -d '10 min ago' +%Y-%m-%dT%H:%M:%SZ`.
- `visor app start|stop|restart <name>`, `visor app arg ...` for runtime app arguments.
- `visor dmsg-servers`, `visor ports`, `visor ip`, `visor ping`.

## Transports and routes
- `skywire cli tp` is a top level group and lists this visor's live transports. There is no `tp ls`. `tp --remote <pk>` asks a remote visor, `tp all` is the whole network from Transport Discovery.
- `tp add`, `tp rm`, `tp disc`, `tp auto`, `tp id`. See skywire-transports and skywire-transport-setup.
- `skywire cli rg ls` lists active route groups.

## Fleet and discovery
- `skywire cli visor hv ls` lists the visors of a hypervisor in one call. Use `--shape` to see the fields, then `--jq`. Rows carry the key, hostname, version, public IP, uptime and country. Prefer it to a per visor `--via` fan out.
- `skywire cli sd`, `pv`, `ut`, `svc`, `mdisc` are the discovery queries. `proxy list` shows keys that advertise a public proxy.
- `skywire cli log` and `survey` collect survey and transport logs. `reward` and `rewards` are the reward address and the reward operator tools.

## Traps
- `proxy stop --name <n>` takes `--name`, not `-n`. `route calc` takes `--min`/`-n`, not `--min-hops`.
- Endless mesh streams, such as a proxied download, are measured with `curl --max-time N -w '%{size_download}'`, not `head -c | wc -c`.

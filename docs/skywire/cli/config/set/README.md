# skywire cli config set

[← skywire cli config](../README.md)

Set one or more config fields on the RUNNING visor.

The edit goes through the visor, so the visor's own flush carries it instead of
overwriting it — which is what happens to a hand-edit of skywire-config.json
while a visor is running.

Paths are the config's JSON field names joined by dots. A launcher app is
addressed by NAME rather than array position:

  skywire cli config set is_public=true
  skywire cli config set 'launcher.apps[skysocks].auto_start=false'
  skywire cli config set transport.transport_port=7777 routing.min_hops=1
  skywire cli config set log_level=debug --json
  skywire cli config set --via dmsg://<pk> is_public=true

Values are JSON. A bare word that is not valid JSON is taken as a string, so
log_level=debug and log_level='"debug"' are the same thing. Lists and objects
need real JSON: hypervisors='["<pk>","<pk>"]'.

Every field is validated against the config struct before ANY is applied, so a
typo in the third argument leaves the first two untouched. Unknown paths, type
mismatches and edits to sk/pk (or any *_sk / *secret_key field) are refused.

Output is one line per field:

  <path>: <old> -> <new> (live|restart-required)

"live" means a running subsystem took the value immediately. "restart-required"
means it was written to the config file and applies at the next visor start;
--restart halts the visor so the service manager (or the dev loop) restarts it.

Fields applied LIVE today — everything else is restart-required:

  dmsg_web.enable                    run the .dmsg resolving proxy
  dmsg_web.proxy_addr                SOCKS5 bind address for the .dmsg resolving proxy — "" is loopback (also enables it)
  dmsg_web.upstream_rules            per-domain upstreams for the .dmsg proxy — [{suffix,upstream}], upstream is host:port or "direct"
  dmsg_web.upstream_socks            SOCKS5 upstream the .dmsg proxy forwards non-matching CONNECTs to
  flight_recorder                    keep the last seconds of execution trace for stall diagnosis (allocates heavily)
  hypervisor.enable                  run the hypervisor: DMSG-RPC listener, managed-visor tracking and the web UI
  hypervisor.enable_auth             require a login on the hypervisor web UI (rebuilds the UI router; open tabs must reload)
  hypervisor.ui_disable              stop the hypervisor web UI, keeping DMSG-RPC, tracking and `hv ls`
  hypervisors                        configured hypervisor PKs (connects/disconnects the difference)
  is_public                          advertise this visor in service discovery (same path as `cli tp public`)
  launcher.apps[*].args              replace the named app's argument list
  launcher.apps[*].auto_start        start the named app when the visor starts
  launcher.apps[*].env               replace the named app's environment (KEY=VALUE entries)
  log_level                          visor log level (error|warn|info|debug|trace)
  persistent_transports              transports the visor re-dials until they exist
  pty.whitelist                      PKs allowed to use this visor's pty, sftp and RPC besides its hypervisors
  reward_address                     skycoin address rewards are paid to (durable store is <local_path>/reward.txt, not this json)
  routing.calculate_routes           calculate routes locally instead of asking the route finder
  routing.min_hops                   minimum intermediate hops per route
  routing.mux_fec                    forward error correction on mux route groups
  routing.router_settings[*]         one router knob from the live catalog (see `cli route settings`)
  routing.transport_preference       transport-type priority order, most-preferred first
  skynet_web.enable                  run the .skynet resolving proxy
  skynet_web.proxy_addr              SOCKS5 bind address for the .skynet resolving proxy — "" is loopback (also enables it)
  skynet_web.upstream_rules          per-domain upstreams for the .skynet proxy — [{suffix,upstream}], upstream is host:port or "direct"
  skynet_web.upstream_socks          SOCKS5 upstream the .skynet proxy forwards non-matching CONNECTs to
  transport.public_autoconnect       auto-dial transports to public visors

Read a value back with `config show`, which reads the running visor and
honors the global --jq filter:

  skywire cli config show --jq .transport.transport_port

## Usage

```
skywire cli config set <dotted.path>=<value> [<dotted.path>=<value>…]
```

## Flags

```
      --restart      halt the visor after a restart-required change (the service manager restarts it)
      --rpc string   RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
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

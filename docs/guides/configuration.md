# Skywire Configuration

The skywire visor requires a JSON-formatted config file to run,
produced by `skywire cli config gen`.

The `skywire-autoconfig` script included with the skywire package
handles config generation and config updating for the user who
installed the package, as well as restarting the skywire systemd
service.

Detailed command flag documentation lives at
[/docs/skywire/cli/config/](../skywire/cli/config/README.md) and
[/docs/skywire/visor/](../skywire/visor/README.md). The most important
flags are noted below.

## Config gen

To run skywire, first generate a config:

```
skywire cli config gen -birx
```
* `-b --bestproto` use the best protocol (dmsg | direct) to connect to the skywire production deployment — recommended
* `-i --ishv` create a local hypervisor configuration (optional)
* `-r --regen` regenerate a config which may already exist, retaining the keys
* `-x --retainhv` retain any remote hypervisors set in the config (optional)

More options for configuration are displayed with `skywire cli config gen --all`.

NOTE: If you have installed skywire as a package or via the windows .msi
or mac installer, include the `-p` flag — and `skywire cli config gen`
must be run as root.

## Hypervisor web UI

In order to expose the hypervisor UI, generate a config file with `--is-hypervisor` or `-i` flag:

```
skywire cli config gen -i
```

After starting up the visor, the UI will be exposed by default on `localhost:8000`.

## Hypervisor terminal UI

A terminal-based hypervisor that mirrors the web UI's read and write actions
without requiring a browser:

```
skywire cli visor hv tui
```

Lists all visors connected to the running hypervisor and (when one is selected)
shows transports, apps, route groups with full multi-hop paths, DMSG servers,
and a row of hotkey-driven actions: start/stop apps, set min_hops/mux_routes,
manage transports/routes, toggle resolving proxies, register skynet/forwarded
ports, run dmsg connect-all, view services-health, reload or shutdown.

When a hypervisor has another hypervisor in its `hypervisors` config, the
parent hypervisor transparently sees and manages the child's connected visors —
write actions are routed through the child automatically.

## Add remote hypervisor

Every visor can be controlled by one or more hypervisors. To allow a hypervisor
to access a visor, the PubKey of the hypervisor needs to be specified in the
configuration file. You can add a remote hypervisor to the config with:

```
skywire cli config update --hypervisor-pks <public-key>
```
OR:
```
skywire cli config gen --hvpk <public-key>
```

## Network Visualization UI

Skywire includes a network visualization and visor control interface that can run in two modes.

**Visor-embedded mode** (recommended when visor is running):
```
skywire cli tp viz --visor
```
This starts the UI as part of the visor, with direct access to local transport and route data.

**Standalone mode** (network visualization only):
```
skywire cli tp viz
```
This runs a standalone visualization server using transport discovery data.

The web UI (default `localhost:8080`) provides:

* **Real-time network graph**: Visual representation of visors and their connections in the Skywire network
* **Transport information**: View active transports with details on type (STCPR, SUDPH, DMSG), remote public keys, and connection status
* **Geographic clustering**: Visors are grouped by country and IP subnet for easier network topology understanding
* **Click-to-copy**: Easily copy public keys by clicking on nodes in the graph
* **Visor control** (visor mode): Direct interface for managing the local visor

Note: This is a separate UI from the hypervisor interface and caches transport data locally.

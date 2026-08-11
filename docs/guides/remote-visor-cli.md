# Controlling a Remote Visor from the CLI

Every `skywire cli` command normally talks to the visor on the same
machine over local RPC (`localhost:3435`). The same commands can drive
a visor on **another** machine over the skywire network itself — full
typed API: start and stop apps, change routing-session settings
(min-hops, mux), manage transports and routes, and read the runtime
log ring.

The motivating workflow: diagnosing a device you cannot shell into.
Instead of reproducing a VPN/proxy failure by tapping through a
phone's GUI, attach that visor to a machine you control and run the
client from your desk with the logs in front of you — the difference
between "it doesn't connect" and an actual trace.

Two independent mechanisms exist, and they differ in direction:

| | Direction | What you get | Target needs |
|---|---|---|---|
| **`--via` CLI bridge** | controller dials target | the full `skywire cli` surface against the target | controller's PK in the target's trust list, `cli_addr` set, restart |
| **Hypervisor attach** | target dials controller | web UI, `hv ls`, `hv tui` (apps, routing knobs, transports, logs) | controller's PK in the target's `hypervisors`, restart |

Listing a controller PK in `hypervisors` enables **both** — it puts
the PK in the trust list *and* makes the target dial out to it.

## Who may control a visor

A visor's management surfaces all consult one live whitelist
(`pkg/visor/peer_whitelist.go`): the visor's **own PK**, every PK in
the config's **`hypervisors`** list, and every PK in
**`dmsgpty.whitelist`**. A PK on that list has *full* control —
including a remote shell where dmsgpty is enabled. Only list keys you
own.

Two consequences worth knowing before you troubleshoot:

- **The inbound listeners only start when the boot config trusts
  someone.** With no hypervisor or pty-whitelist PKs at startup the
  visor logs `No hypervisor PKs or dmsgpty whitelist; dmsg visor-RPC
  server disabled` and nothing listens. A visor nobody configured to
  be managed is unreachable by construction.
- **The whitelist and listeners are built at boot.** `skywire cli
  visor hv add <pk>` connects out to the new hypervisor immediately
  and persists the PK, but *inbound* access for that PK (the `--via`
  path below) starts on the target's next restart.

Trust is transitive upward: when a visor connects to its hypervisor,
the hypervisor pushes its *own* hypervisors into the visor's live
whitelist — so a hypervisor-of-hypervisors reaches every visor down
the chain without being listed on each one.

## Set up

**On the controller** — a normal visor. Note its PK:

```
skywire cli visor pk
```

Running a hypervisor (`config gen -i`, or `skywire cli visor hv
enable`) is only needed for the web UI / `hv ls` / `hv tui` views.
The `--via` CLI bridge works visor-to-visor without one — being
*listed* by the target is enough.

**On the target** — put the controller's PK in the config, then
restart the visor:

```
# at generation
skywire cli config gen --hvpks <controller-pk> ...

# or on an existing config file
skywire cli config update hv --add-pks <controller-pk>

# or on a running visor (persists; inbound access needs the restart)
skywire cli visor hv add <controller-pk>
```

The target also needs `cli_addr` set (the default config has it;
API-only builds that blank it skip every RPC surface — see the mobile
section).

## Driving the target

Add `--via dmsg://<target-pk>` to any command. The local visor opens
a dmsg stream to the target's visor-RPC port and bridges bytes; the
CLI speaks its normal protocol over it, using the **local visor's
identity** — which is exactly the PK the target was told to trust. No
separate CLI keypair.

```
skywire cli --via dmsg://<pk> visor info
skywire cli --via dmsg://<pk> tp                # transports
skywire cli --via dmsg://<pk> route groups      # active route groups
skywire cli --via dmsg://<pk> visor app ls
skywire cli --via dmsg://<pk> visor app log skysocks-client beginning
```

`--via skynet://<pk>` does the same over a skywire transport
(stcpr/sudph, auto-created) instead of the dmsg relay — lower latency
when a direct path exists; prefer `dmsg://` for NAT-bound targets.
`--rpc dmsg://<pk>` / `--rpc skynet://<pk>` are accepted as aliases.

### The diagnosis session

The shape that replaces poking at a device's GUI — one terminal
following the target's runtime log ring, one driving the client:

```
# terminal 1: the target's own account of what happens
skywire cli --via dmsg://<pk> visor log --follow --min-level debug

# terminal 2: run the client on the target
skywire cli --via dmsg://<pk> proxy start <server-pk> --min-hops 3
```

`visor log` reads the target's in-memory ring — route-setup,
transport, and router events arrive tagged with the app session, so a
route-finder refusal or a dead intermediate shows up as itself rather
than as a hung connect. `--module <regex>` and `--min-level` narrow
it. `proxy start` / `vpn start` flags (`--min-hops`, `--mux`,
`--existing-tp`…) apply to the *target's* routing session, so a
"fails at min_hops 3, works at 1" report reproduces exactly.

One caveat: `proxy start --verbose` opens its scoped log stream over
local gRPC and does **not** follow `--via` — with a remote target it
would stream the *local* visor's logs. Use the `visor log --follow`
terminal instead.

### Hypervisor views

When the controller runs a hypervisor, every visor listing it
connects inbound and can be driven through:

```
skywire cli visor hv ls     # all connected visors, one row each
skywire cli visor hv tui    # interactive: apps, min_hops/mux, transports, routes
```

plus the web UI on `localhost:8000`. These ride the connection the
*target* dialed, so they work even for targets whose inbound RPC
surfaces are off.

## Testing the mobile app's visor from a desktop

The Android app generates and owns its visor's config
(`android/…/core/ConfigManager.kt`), with two pins that matter here:
`hypervisors` is empty and `cli_addr` is `""` — so out of the box the
phone neither trusts anyone nor runs any RPC surface. Deliberate:
nothing on the phone listens unless its owner asked.

**What works today (debug builds):** hand the phone's config a
`hypervisors` entry and restart the app. The phone dials the desktop
hypervisor over dmsg and serves its full RPC over that outbound
connection — it appears in `hv ls`, and `hv tui` / the web UI can
start/stop its apps, set min-hops and mux, and read its runtime logs.

```
# desktop: hypervisor running; note its PK
skywire cli visor pk

# phone (debug build): add the PK to the visor config
adb shell run-as com.skycoin.skywire sh -c \
  'cat files/skywire/skywire-config.json' > phone-config.json
# edit: "hypervisors": ["<desktop-pk>"]
adb push phone-config.json /data/local/tmp/
adb shell run-as com.skycoin.skywire sh -c \
  'cp /data/local/tmp/phone-config.json files/skywire/skywire-config.json'
# then fully stop and relaunch the app (the visor reads the list at boot)
```

The edit survives normal launches — the app's per-launch profile pass
preserves keys it does not own — but is dropped by an identity reset
or key replacement, and is impossible while config sealing is enabled
(the file on disk is then `skywire-config.json.enc`; disable sealing
in the app first). The phone's PK is on the app's identity screen and
in the diagnostics export.

**What does not work yet:** the full `--via` bridge against the
phone. That needs the two profile pins to open up — `cli_addr` set
(loopback listener plus the dmsg RPC surfaces it gates) and a
user-facing way to grant a remote-management PK instead of an adb
edit. Both are app changes, tracked as follow-up work to the mobile
logging/diagnosis items; until then the hypervisor views above are
the remote-control surface for phones, and their app/routing/log
coverage is enough for the VPN-and-proxy diagnosis workflow.

Expect a phone visor to be reachable only while the app's core is
running (foreground service); Android may still throttle the radio in
deep Doze, which shows up as dmsg session drops, not as auth errors.

## Troubleshooting

- **Dial fails / times out on `--via dmsg://<pk>`** — the target's
  listener is off (no trusted PKs at its last boot, or `cli_addr`
  blank), the target restarted since you were added but *you* typo'd
  the PK, or the target is offline in dmsg. Check the target's boot
  log for `Dmsg visor-RPC server listening`.
- **Connection accepted, then dropped immediately** — the connecting
  visor's PK is not on the target's live whitelist (added at runtime
  without a restart, or you are dialing from a different visor than
  the one that was whitelisted).
- **`bridge needs the local visor RPC at localhost:3435`** — `--via`
  bridges through the *local* visor; start one.
- **A visor is missing from `hv ls`** — the target dials out only if
  the controller's PK was in its `hypervisors` at boot; `hv add` on
  the target (or a restart after a config edit) fixes it.

Related: [configuration.md](configuration.md) (hypervisor setup,
`hv tui`), [VISOR_CONFIG_RUNTIME.md](../../VISOR_CONFIG_RUNTIME.md)
(runtime vs config-file changes),
[manual-routing.md](manual-routing.md) (what to do with the routes
you can now see).

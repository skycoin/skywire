# skywire cli route settings

[← skywire cli route](../README.md)

Show or set the visor's runtime router knobs.

With no arguments every knob is printed: the routing choices (minimum hops,
existing-transports-only, local route calculation, transport type preference)
followed by the whole mux/dataplane catalog — send windows, RACK and reorder
terms, leg liveness and failover timers, SACK and delayed-ack cadence, TLP and
HoL retransmit, shared-bottleneck detection, FEC geometry, the ECF selector,
the unidirectional flip controller, the mux event rings, and the capability
toggles. --json carries the same catalog as a stable map under "knobs", plus
each knob's compiled default and what the config holds for it under
"knob_detail", so a bench runner can save it and feed it back verbatim.

Knobs are set as key=value arguments, one or many:

  skywire cli route settings ecf.max_window_bytes=16MiB rack.ceil=800ms
  skywire cli route settings mux.sack=false
  skywire cli route settings --app skysocks-client leg.starve_ratio=3
  skywire cli route settings --reset

Values take the same spellings 'proxy settings' accepts: 8MiB / 64K for bytes,
250ms / 5s for durations, a plain float for ratios, true/false for toggles.
Every default is the value the binary compiled with, so a visor that sets
nothing behaves exactly as it did.

--app scopes the change to the route groups owned by that app, so a subject
client and its paired reference can run different values on one visor;
unspecified knobs fall back to the visor-wide value. --reset restores every
default (with --app, drops just that app's overrides).

Changes take effect at once: values read on a data path are live, and the ones
read when a route group is BUILT — the negotiated capabilities mux.per_frame_noise,
mux.sack, mux.hol_retx and fec.enabled — apply to NEW groups, leaving groups
already running untouched. Everything set here is written to
routing.router_settings and restored at the next start. The older per-knob flags
below remain for compatibility; they name the same knobs.

## Usage

```
skywire cli route settings
```

## Subcommands

- [dial](dial/README.md) — Show or set the visor's dial-time route-ranking knobs

## Flags

```
      --app string                      scope the key=value settings to the route groups owned by this app (default: visor-wide)
      --dead-route-hold duration        how long a route that died young is kept out of the next diversify search
      --dead-route-hold-max duration    ceiling on the doubling applied to that window on each repeat death
      --ecf-max-window string           per-leg send window ceiling (e.g. 8MiB)
      --ecf-min-window string           per-leg send window floor (e.g. 128KiB)
      --ecf-window-margin float         multiplier on proven delivery-per-RTT (e.g. 2.0)
      --existing-tp-only string         true|false: only route over transports that already exist
      --force-local string              true|false: calculate routes locally instead of the route finder
      --forward-spill string            true|false: let a FORWARD frame leave its confined leg when that leg is at its send window (default false — the writer waits instead)
      --forward-switch-margin float     how much lower a challenger leg must measure, for two consecutive samples, before the forward direction moves to it (e.g. 0.2)
      --leg-park-min-hold duration      how long an adaptive park holds before a leg may be re-admitted
      --leg-probe-bytes string          what a leg cut to probe-only may carry per window (e.g. 64KiB)
      --leg-starve-ratio float          delay-basis multiple AND inverse goodput fraction at which a leg is cut to a probe per window (e.g. 6.0)
      --min-hops int                    minimum hops for route calculation (default -1)
      --mux-fec string                  true|false: advertise FEC on NEW mux route groups
      --prefer string                   transport type order, most preferred first (e.g. squicr,stcpr,sudph); 'default' restores the built-in order
      --reset                           restore every knob to the value the binary compiled with (with --app: drop that app's overrides)
      --sbd-backoff duration            how long a pair whose park trial failed is exempt from shared-bottleneck merging (doubles per repeat)
      --sbd-demote string               true|false: let a shared-bottleneck ruling PARK a leg (default false — rulings are recorded as sbd_ruling mux events only)
      --sbd-min-evidence-rate string    aggregate goodput a group must carry before a shared-bottleneck ruling may park a leg (e.g. 64KiB)
      --sbd-min-samples int             per-leg delay samples a shared-bottleneck verdict needs before it may park a leg
      --sbd-sample-interval duration    minimum spacing between two per-SACK delay samples folded into a leg's shared-bottleneck window
      --sbd-trial-loss float            fraction of aggregate goodput a park may cost before it is undone (e.g. 0.15)
      --sbd-trial-window duration       how long a shared-bottleneck park is held as a trial before the aggregate goodput is re-read
      --send-window-wait-max duration   how long a writer parks when every ready leg is at its window
```

## Global Flags

```
  -h, --help              show help menu
      --jq string         filter JSON output through a jq/gojq expression (implies --json)
      --json              print output as JSON
      --rpc string        RPC server address (env: SKYWIRE_RPC) (default "localhost:3435")
      --shape             print the output schema skeleton (zero values, all fields) instead of data
      --timeout int       RPC timeout in seconds (0 = unlimited) (default 30)
      --tui               browse commands and help interactively
      --via dmsg://<pk>   remote visor target — dmsg://<pk> or `skynet://<pk>`
```

---
_Generated by `skywire doc` — do not edit by hand._

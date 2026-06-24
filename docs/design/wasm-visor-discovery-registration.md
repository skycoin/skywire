# wasm-visor route setup blocker: the tab isn't registered in dmsg discovery

## Symptom

A browser wasm-visor can now ORIGINATE routes (route-finder + setup-node dialer
wired, `MinHops=1`; see #3268/#3269). `rtr.DialRoutes(...)` gets past the
routing-disabled gate into route-id reservation, then fails:

```
route setup: failed to instantiate route id reserver: a dial attempt failed with:
dial <tab-pk>@136: dmsg error 100 - entry is not found in discovery
```

The setup node (RSN) tries to dial the **tab itself** at `<pk>@136` (to install
the route's source rule, the legacy setup path) and can't find the tab in dmsg
discovery — so route setup can't complete.

## Root cause (traced)

A browser tab uses a **seeded/direct** dmsg client (`StartDmsgSeeded`): it can
only WebSocket to one seed server, and bootstraps discovery from a preloaded
direct entry. The registration mechanism IS present and correct:

- `upgradeDiscovery` installs a `RegisteringFallbackDiscClient`
  (`fallback_disc.go`): READS resolve direct-first; WRITES (`PutEntry`) route to
  the real HTTP-over-dmsg discovery (`register: true`).
- The dmsg client's post-session `updateClientEntry` loop calls `PutEntry`
  (`entity_common.go:798`). On the first cycle `isDue` is true (`lastUpdate=0`),
  so it *does* attempt the write.

But the tab's entry never appears in discovery (`mdisc entry <pk>` → 404). So the
**`PutEntry` write to the dmsg-discovery is not succeeding** — and it's the
classic dmsg-bootstrap reachability problem: a client connected to a *single*
seed server can only reach peers/services that server can bridge to. The
route-finder query worked (so the tab reaches *some* dmsg services), but the
discovery write specifically isn't landing — likely the discovery isn't reachable
through the one seed server the tab holds, or the write errors and the update loop
swallows it.

This is exactly where the wasm-visor diverges from the non-wasm visor: a normal
visor dials **many** dmsg servers and registers cleanly; the browser tab holds one
WS session and hits the chicken-egg (needs discovery to learn more servers, needs
more servers to reach discovery). `MinSessions=2` is meant to help but only after
the first discovery contact.

## Why it matters

Route setup (legacy path) requires the RSN to dial **every hop including the
source** at `@136`. The source is the tab. If the tab isn't discovery-registered,
the RSN can't reach it. This blocks multihop routes from the tab — and therefore
the in-tab skysocks-client / clearnet browsing, which ride `DialRoutes`.

## Fix options (next focused work)

1. **Make the tab actually register** — ensure the seed server(s) the tab uses can
   bridge to the dmsg-discovery, and/or have the tab open multiple WS sessions
   (browsers can) so it reaches the discovery's server. The registration code is
   already correct; this is about reachability/topology. **Needs a diagnostic**
   that surfaces the `PutEntry` error (today it's a swallowed Debug log in the
   browser console) — e.g. a `skywireVisor.checkRegistered()` hook that GETs the
   tab's own entry from discovery over its dmsg-HTTP and reports found / the exact
   error.

2. **Source-inject cascade setup** — set `forceLegacy=false` so the route uses the
   cascade path where the SOURCE injects its own rule down its own transports and
   the RSN never dials the source (it's a signing oracle). A browser tab that's
   hard to reach inbound is the ideal case for this. Caveat: the cascade had a
   multihop data-plane bug (opt-in until fixed); may work for 1-hop.

3. **Both** — register for inbound reachability *and* prefer source-inject so route
   setup doesn't depend on the RSN reaching the tab.

## Status

Route origination is wired and initiates setup (#3268/#3269). The remaining
blocker is this discovery-registration / dmsg-bootstrap reachability for browser
tabs — the real prerequisite for the in-tab skysocks-client. A `checkRegistered`
diagnostic is the next step to see *why* the `PutEntry` write isn't landing.

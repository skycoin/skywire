# Gate G8 evidence — M8 Simulator release candidate (author's side)

Collected by the author on 2026-10-13 on the dev Mac (Xcode 27.0, iOS 27.0
runtime 24A434, Go 1.27.2) on `feat/skywire-mobile-ios` from `ef096a9fb` plus
this round's changes. The gate record (`ios/gates/G8.md`) is the reviewer's
to write; the checklist they run is `ios/gates/regression.md`.

The two Simulators were made for the run (`xcrun simctl create "M8 iPhone 18
Pro" "iPhone 18 Pro"`, and the same for the iPhone 17e), as fresh as an
erased device, so the bench's own Simulators kept their identities. The
desktop peer is `ios/scripts/desktop-peer.sh ../peer-m8`, built from the same
checkout (key `0284c698…2ef23506`), serving a SkyDEX market from 5.3 on
(`PEER_MARKET=1`).

## What M8 added

| Item | What | Where |
|---|---|---|
| 8.1 | the regression checklist, every flow M1 to M7 as numbered steps | `ios/gates/regression.md` |
| 8.1 | a desktop peer from one command, the market optional | `ios/scripts/desktop-peer.sh` |
| 8.1 | calls driven and measured from the Mac | `ios/scripts/peer-call.py` |
| 8.1 | a call placed from the chat page, whoever answers first | `SkywireUITests/CallChecks.swift` |
| 8.2 | the footprint tour and its sampler | `SkywireUITests/FootprintTour.swift`, `ios/scripts/footprint-table.py` |
| 8.3 | TestFlight and App Review notes, export compliance, privacy, screenshots | `ios/store/`, `ios/scripts/store-screenshots.sh` |
| 8.4 | the facts for the licence check | `ios/store/README.md` |
| 8.5 | the release check reads the extension's version too | `.github/workflows/ios-release.yml` |

## Results on the iPhone 18 Pro

Every step of `regression.md` ran on the 18 Pro, in the order it gives
(the first pass ran the UI suites alphabetically, which is how the order
problems below were found):

- 1.1 to 1.4: the Skywire scheme's tests `TEST SUCCEEDED`, SkywireTests 8 and
  SkywireAppTests 72 with 1 skipped; CoreClient 127, WalletCore 38 with 4
  skipped; the Go TUN test ok; the extension's device build.
- 2.1 to 2.5: first launch to PONG in 35 s, the profile's pins in the config,
  `GOMEMLIMIT set to 40MiB (was none)`, background and force-quit as written
  (2.4 rewritten, below), relaunch to PONG in 33 s.
- 3 to 8, the UI suites, 30 tests, all passing: ScreenTour 3, FlowChecks 5,
  ChatChecks 6, SocksChecks 4, DexChecks 1 (against the peer's market, so
  M4's deferred live connect now passes), FleetChecks 1, WalletChecks 3,
  WalletTour 4, MailChecks 3, CallChecks 1 (six runs). The first pass failed
  three on order: the chat and mail banners before anything had allowed
  notifications, and Fleet before FlowChecks turned it on.
- Hand steps: history after a relaunch, the deep link, remote management
  (`--via` printed the phone's key), a five-minute call with mute and a
  minute in the background (`recv` above 0 throughout, exactly 0 while muted),
  hang-ups from both sides, a call placed from the page, Export all, Export
  config behind Face ID, Encrypt config (after its fix), the config pins
  after the day's restarts, no recovery phrase in any log (the longest run of
  BIP-39 words in the unified log and the core's files was 4), the SOCKS exit
  (45.63.123.127 through the proxy against the Mac's 45.56.79.245), the
  version stamp (10.1).
- 9.4 (New identity) was left out: it would have cut the peer's grants
  before the measurements.

## Results on the iPhone 17e

Not run by the author this round; the time went to the memory regression
below. The reviewer runs the whole checklist on the iPhone 17e as on the
18 Pro.

## Bugs the run found, and their fixes

1. **A call answered before the app's poll left a white screen with no Hang
   up.** A call the chat page places reaches the app through its 2-second
   poll. When the peer answered inside that window, the app first saw the
   call in the active list, which carries ids only, so it had no name.
   `CallScreen` then drew its background and a spinner over everything, the
   whole call long, and the page's own call panel underneath could not be
   reached. Reproduced on the old build both times the peer answered
   within about two seconds of the dial. Android's screen draws nothing without
   a name (`state.peer ?: return`); the iOS port now does the same, and the
   page's panel ends the call (checked by hand on the fix). `CallChecks`
   places a call from the page and hangs it up from whichever surface is in
   front. The race decides which: in the four runs whose screenshots I
   checked, the app saw the ring each time, so the test mostly covers the
   named screen.
2. **Encrypt config did not seal on Disconnect.** The seal ran once, right
   when the stop returned, and checked `coreState`, which follows the core
   through a 250 ms poll and still read "stopping". The plaintext config,
   secret key included, stayed on disk while disconnected. It now seals on
   the finished stop. Before: `skywire-config.json` still there with the core
   stopped. After: only `skywire-config.json.enc`. Android seals in its
   service after the process exits and does not have this.
3. **FleetChecks looked for the key in the row's label**, which has been empty
   since the M6.5 redesign (the key is a text inside the row). The Fleet
   screen listed the peer within seconds; the test now matches a row that
   contains the key.
4. **The microphone prompt named only voice and video messages.** Calls use
   it too (guideline 5.1.1 wants the purpose stated). It now reads "for calls
   and to record voice and video messages", in all three languages.

5. **The core's Go memory had grown by about 10 MiB since G0** (found by
   the footprint table, item 8.2). In the app at 10 minutes connected,
   Go's share was 37.5 to 39 MiB, against the 40 MiB limit with 106
   collections in ten minutes (one every six seconds); without the limit it
   reached 59 to 67 MiB, where G1 measured 27 MB. A/B on the Mac, the lite
   core on the phone's own config, same toolchain (go1.27.2), 10 minutes,
   40 MiB limit, `footprint -p` every 30 s and pprof at the end:

   | build | process | Go (Untagged) | allocated in 10 min | live heap | goroutines |
   |---|---|---|---|---|---|
   | G0's commit, `3d6a5e669` | 32-34 MiB | 23-26 MiB | 67 MB | 20.0 MB | 173 |
   | this branch before the fix | 40-44 MiB | 32-36 MiB | 178 MB | 15.1 MB | 208 |
   | with the fix | 29-33 MiB | 20-24 MiB | 35 MB | 11.4 MB | 191 |

   Over 110 MB of the allocations were CXO feed snapshots decoded and
   walked again and again (`encoder.DeserializeRaw` 61 MB, the treestore
   subscriber's root filling 35 MB and cleanup walk 23 MB, the subscription
   manager's snapshot walks 16 MB). Since develop's #5446, #5449 and #5451
   (2026-10-06) every visor subscribes at start to dmsg discovery's
   clients-by-server feed and the address resolver's bindings feed, both
   network-wide, and the subscriber re-fetches whenever a root fails to
   fill ("no connections to fill from"). The mobile build now leaves both
   feeds alone and uses the HTTP lookups it used before
   (`pkg/visor/network_feeds_mobile.go`); desktops are unchanged. Android
   runs the same lite core and gets the same fix.

   A client polling the core as the app's Home does (summary and service
   health every 4 s, the three voice lists every 2 s, the notification
   stream) then lifted the fixed core from 20-24 to 32-34 MiB on the Mac:
   each `/api/service-health` probed every deployment service and dmsg
   server over dmsg, 67 of 144 MB. The mobile build now answers it from a
   probe up to 30 s old (`service_health_cache.go`): 28-29 MiB, 103 MB.

   In the app, 18 Pro, 10 minutes connected, outside XCUITest:

   | build | limit | Go's share (5-10 min) | whole app | collections | heap | Sys |
   |---|---|---|---|---|---|---|
   | before | 40 MiB | 37.8 MiB | 63.4 MiB | 106 | 20.7 MB | 45.8 MB |
   | before | none | 59.3 MiB | 86.7 MiB | 32 | 45.1 MB | 70.1 MB |
   | feeds cut | 40 MiB | 37.1 MiB | 62.7 MiB | 44 | 19.2 MB | 42.3 MB |
   | feeds cut | none | 44.6 MiB | 70.1 MiB | 15 | 27.3 MB | 53.9 MB |
   | both fixes | 40 MiB | 36.8 MiB | 62.5 MiB | 26 | 14.7 MB | 45.8 MB |

   **Still open: Go's share in the app stays near 37 MiB with the limit**,
   above G8's 33 MB, although the core now collects a quarter as often and
   its heap is 14.7 MB. Go keeps the freed pages resident up to its limit in
   the app, where the same core on the Mac returns them (20-24 MiB). Not
   chased further this round. Leads for the follow-up, in order: why the iOS
   process keeps them (the scavenger's goal under a memory limit), and the
   per-request session check, which gob-decodes the user record from bbolt
   on every API call (36.5 of the remaining 103 MB on the Mac).

## Seen on the bench, not fixed here

- **The Simulator no longer freezes a suspended app.** runningboard marks
  the app suspended about 30 s after it leaves (the background grace), but
  its process keeps running and answering on loopback; the core logged at
  14:55:05 and 14:55:09 after the 14:55:00 suspension. G1 and G3 saw it
  freeze. Checklist 2.4 now checks runningboard's states instead of a
  timeout.
- **A launch with `-core.wantsConnected YES` never asks for notifications.**
  The prompt comes with a Connect tap, which that launch skips, and the
  later connects are remembered. A user's first Connect is always a tap, so
  this is the bench's; the checklist makes the first Connect a tap.
- **A phone hang-up can reach the peer as a lost media transport.** Of 14
  calls between the phone and the peer, 3 hang-ups showed on the peer as
  "media transport failed — reconnecting", and the peer held the call for its
  60 s reconnect window before ending it. The voice stack is the shared Go
  core (Android too); worth its own issue.
- **The first remote-management calls after a start are reset by the
  peer.** `skywire-cli --via` failed twice with "connection reset by peer" in
  the minute after the phone's core started, then answered. G4's note 5 saw
  this once and could not reproduce it.

## Version rehearsal (8.5)

- The PR's own `iOS release` run at `ef096a9fb` (run 25, attempt 1) stamped
  the Simulator app `Info.plist: 0.1.0 (25.1)` and stopped at the
  missing-secrets notice. From this round it checks the extension's
  `Info.plist` as well.
- An unsigned Release archive for a device, stamped as `mobile-v0.1.0` and
  build 26.1 (`MARKETING_VERSION=0.1.0 CURRENT_PROJECT_VERSION=26.1
  CODE_SIGNING_ALLOWED=NO archive`): `com.skycoin.skywire` and
  `com.skycoin.skywire.tunnel` both `0.1.0 (26.1)`, minimum iOS 16.0, the Go
  runtime in both binaries, app 112 MB of which the extension 53 MB.
- Checklist 10.1 on the Simulator build: `0.1.0` and `42.1` in both bundles.

## The oldest runtime

Only iOS 27.0 is on the bench (7 GB free; a runtime is about 8 GB). CI runs
the Skywire scheme's tests on iOS 26.5 with Xcode 26.6 on every pull request
(`ios-app.yml`, run 38294325602: `OS:26.5, iPhone 17 Pro`). The deployment
target, iOS 16.0, is compile-checked only (every target links against
`arm64-apple-ios16.0`).

## Footprint (8.2)

`ios/scripts/footprint-table.py --device "M8 iPhone 18 Pro" --call-peer
http://127.0.0.1:18000 --dex-market <the peer>`, before the memory fixes,
under XCUITest (its accessibility machinery runs in the app process, so the
whole-app numbers read higher than G1's simctl launches):

| state | samples | phys_footprint median (MiB) | highest (MiB) | Go's share median (MiB) |
|---|---|---|---|---|
| idle | 12 | 43.3 | 43.4 | 10.5 |
| connected | 36 | 75.7 | 76.8 | 37.5 |
| chat | 12 | 76.3 | 77.1 | 37.4 |
| socks | 8 | 84.3 | 85.5 | 40.2 |
| dex | 12 | 87.5 | 88.1 | 38.9 |
| call | 12 | 75.4 | 76.4 | 38.8 |

Against G1: idle Go's share 10.5 MiB (G1 10.4 MB) holds. Connected Go's
share fails the 33 MB criterion before and after the fixes (memory section
above). The samples are `footprint-18pro-samples.tsv`; the limit runs and
the Mac A/B are the other `footprint-*` and `host-*` files.

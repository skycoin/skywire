# Gate G1 evidence — M1 iOS spike (author's side)

Collected by the author on 2026-09-29 on the dev Mac: Xcode 27.0 (27A266a),
iOS 27.0 Simulator runtime, `iPhone 18 Pro` Simulator, Go 1.26.4
darwin/arm64, branch `feat/skywire-mobile-ios`. Debug build, as §5's recipe
builds it. The gate record itself (`ios/gates/G1.md`) is the reviewer's to
write.

Every number here is **in-app on the Simulator**: the core runs inside the
app process, next to SwiftUI, on the Mac's kernel. It is not the
packet-tunnel extension and not a device (that is Lane D3).

| File | What it is |
|---|---|
| `corebridge-tests.log` | `xcodebuild … test`, filtered to the test cases and the summary |
| `lifecycle.log` | connect by relaunch, background 2 min, foreground, force-quit, relaunch, each with a ping from the Mac |
| `footprint-limit40.tsv`, `footprint-nolimit.tsv` | `footprint -p` every 30 s for 10 min after connect (phys_footprint, its peak, and a ping) |
| `footprint-breakdowns.txt` | `footprint -p`'s per-category tables: idle, 2 min and 10 min with the limit, 10 min without |

Screenshots are attached to the M1 PR, not committed (§5; `.gitignore`
keeps them out): `01-idle`, `02-limit40-2min`, `03-limit40-10min`,
`04-connected`, `05-after-background`, `06-after-relaunch`,
`02-nolimit-2min`, `03-nolimit-10min`.

## Link set (item 1.6)

Read statically before the first link, not found by trial: `nm -u` on each
slice's `libskywire-core.a`, minus the symbols the archive defines, leaves
168 external symbols, identical for the device and Simulator slices. Every
one of them was looked up in the SDK's `.tbd` stubs (both SDKs):

- libSystem (`usr/lib/libSystem.B.tbd`): 139 of them;
- libresolv (`usr/lib/libresolv.9.tbd`): `res_9_ninit`, `res_9_nsearch`, `res_9_nclose` (Go's resolver);
- CoreFoundation: 18 `CF*` functions and `__CFConstantStringClassReference`;
- Security: 7 `Sec*` functions (crypto/x509 verifying against the system roots).

Nothing unresolved. The three beyond libSystem are `linkerSettings` in
`ios/Packages/CoreBridge/Package.swift`; `Shared.xcconfig` records the same
in a comment. The first link succeeded, and `otool -L` on the app binary
lists `libresolv.9.dylib`, `CoreFoundation` and `Security` and no other
additions.

## Tests (item 1.5)

`xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire -destination
'platform=iOS Simulator,name=iPhone 18 Pro' test CODE_SIGNING_ALLOWED=NO`:
3 tests, 0 failures, in both runs (`corebridge-tests.log` is the second,
on the committed tree). The two ERROR lines in it are finding 2 below.

- `testStartPingStopThreeCycles`: three start → `/api/ping` = `"PONG!"` →
  stop → `127.0.0.1:8000` refuses (`connect()` gives `ECONNREFUSED`) cycles
  in one process (49 s and 190 s in two runs: each start waits on the
  network, mostly the visor's transport-discovery retries).
- `testStopDuringStart`: stop 200 ms into a start returns within 10 s,
  leaves the port refusing, and a normal start works afterwards.
- `testStartFailureCarriesTheReason`: a start with a missing config throws
  the core's reason, `lastError` (copied and freed by CoreBridge) returns the
  same string, and the state is `failed`.

The bundle has no host app, so the core runs in the xctest process and the
app is not launched (one core per process). The scheme marks it not
parallelizable: every test binds `127.0.0.1:8000`, the Mac's loopback.

## Lifecycle (the G1 steps)

`lifecycle.log`, 2026-09-29 12:42–12:46 UTC:

| Step | Result |
|---|---|
| install a new build over the connected app, launch | reconnects by itself (the remembered Connect); `PONG!` after 58 s |
| background (Settings to the front), ping at +10 s, +60 s, +120 s | each times out (curl rc 28): the app is suspended, its core frozen; the Mac's kernel still accepts the TCP connection |
| foreground | `PONG!` within 2 s; the listener survived the suspension, no restart needed |
| force-quit (`simctl terminate`) | ping refused (rc 7) |
| relaunch | reconnects by itself; `PONG!` after 31 s |

Start time (31–58 s to "Startup complete") is spent mostly on the visor's
transport-discovery retries at boot, the same on the host; it is network,
not iOS.

## Footprint (items 1.4, 1.7)

The gauge on the spike screen and `footprint -p` (the Mac's tool, same
`phys_footprint`) agree where compared: idle 36.5 MiB vs 36 MB, 10 min
61.8 MiB vs 62 MB. The app also logs the gauge once a minute
(`category == "footprint"`).

| When | memory_limit | phys_footprint | Go's share | The rest |
|---|---|---|---|---|
| idle: app up, core never started | — | 35 MB | 10.4 MB (9 MB heap/runtime + 1.4 MB core `__DATA`) | 25 MB |
| 2 min after connect | 40MiB | 59 MB | 26 MB | 33 MB |
| 10 min after connect | 40MiB | 62 MB (peak 66 at ~7 min) | 29 MB | 33 MB |
| 10 min after connect | none | 60 MB (peak 63 at ~7 min) | 27 MB | 33 MB |

"Go's share" is the untagged `VM_ALLOCATE` category (the Go runtime maps its
heap, stacks and metadata anonymously) plus the dirty `__DATA` of the image
that holds the core (1.4 MB, from `vmmap`). "The rest" is everything else:
framework `__DATA`/`__DATA_CONST` on the Simulator runtime (~13 MB),
malloc (7 MB idle, 12 MB connected; the growth is not attributed, the
candidates being the log view, URLSession, and the Security framework that
Go's TLS verification calls), CoreAnimation, page tables and dyld. The
core's log line on each start: `GOMEMLIMIT set to 40MiB (was none)`.

**The limit did not bind in these runs.** Over 10 idle minutes Go's share
stayed at 26–29 MB with or without it, below the 40 MiB it caps (the limit
counts Go's own memory, not the app's); the 2 MB between the two rows is
run-to-run noise, not the limit. At G0 the host core without a limit peaked
at 62 MB in its first minute and the limit flattened it to 42–45 MB; no
such burst happened in either in-app run. The limit stays in the profile as
the guard for those bursts and for traffic (not measured here: VPN, SOCKS,
chat load come with M3/M4).

**Against item 1.7's check, "the 1.4 gauge settles at ≤ 45 MB idle": not
met in-app.** The 45 MB came from the host measurement at G0, where the
process was the core alone (42–45 MB there with the same limit). In the app,
the gauge also counts the SwiftUI app and the Simulator's framework state,
which is 25 MB before the core starts. Go's share, the part that moves into
the extension, is 29 MB connected at 10 min with the limit; the extension's
own non-Go baseline on a device is not measurable before Lane D3.

The 9 MB Go spends before any start is the runtime plus the package
initializers of the 927 packages in the core's graph, run when the archive
loads, whether or not Connect is ever tapped.

## Findings on the way

1. **The local dmsg relay cannot bind on iOS.** The visor binds a unix socket
   at `<local_path>/dmsg_relay.sock` for standalone dmsg clients running
   beside it (a desktop's dmsgweb proxies). An app container path makes that
   220 bytes on the Simulator and over 130 on a device, against `sun_path`'s
   103, so every start logged an ERROR. Nothing on a phone attaches to it:
   the iOS profile sets `dmsg.local_relay.enabled = false`. (Android's path
   is short enough to bind; M2's full profile port is where both platforms
   can drop it.)
2. **dmsg discovery answers 422 after a quick restart** ("sequence field of
   new entry is not sequence of old entry + 1") on the first post of the new
   start. Go-side and not iOS-specific; the core keeps going.
3. **No socket reclaim on the Simulator.** After 2 minutes suspended, the
   API listener still worked. A device may reclaim a suspended app's
   sockets; that matters only for `InAppCoreHost` on a device, which is a
   debug option (the extension is not suspended). Lane D1 checks it.

## Not measured here

A device, the extension, VPN traffic, the NE memory limit itself: Lane D.

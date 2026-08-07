# Implementation report — Skywire Android

Running log of what was implemented, when, and how it was verified.
After finishing a part, add a dated entry at the top: what was built, key
decisions/deviations discovered while building it, and the verification that
was actually performed (commands, devices, measured numbers — not intentions).

---

## 2026-08-08 — A polish pass from device use: icon, light theme, battery, coin logos

**Built:**

- **Launcher icon** breathes again: the adaptive-icon foreground drops from
  56×42dp to 44×33dp, so the cloud's box half-diagonal (27.5dp) now sits
  fully inside the 33dp safe-zone radius — no more crowding under OEM masks
  or the launcher's parallax scale.
- **Hub:** the wallet tile's subtitle is now "SKY · Fibercoin · BTC · ETH";
  the hero's killswitch chip is words again — "Killswitch on/off" colored
  green/red replaces the icon-only shield (`KillswitchChip` renders `Text`,
  the shield glyphs and `HeroChip`'s dead `dim` parameter are gone).
- **Light theme:** `TransportPreferenceCard` and `MinHopsCard` now wrap
  `SectionCard`, so every card on SkyVPN carries the outlineVariant hairline
  and the 22dp radius instead of two of them floating borderless; the light
  palette's card fill lifts to `#FAFCFF` and the surfaceContainer ramp
  brightens a step, with the border rather than the fill drawing the edge.
- **Battery card no longer lies:** `AppVisibility` grew a `resumes` counter
  bumped from `MainActivity.onResume()`. The system's exemption dialog only
  pauses the Activity — start/stop never fires, so the old
  `isForeground`-driven refresh missed the grant until an app restart.
  Settings re-reads the exemption on every resume; Home's prompt joins the
  same signal through its `combine`.
- **Coin badges are real logos:** SKY/BTC/ETH/USDT ship as bundled 128px
  PNGs (CC0 `cryptocurrency-icons` set, no network fetch at render) mapped
  in the new `ui/wallet/CoinIcons.kt`; `CoinBadge` takes the `CoinSpec` and
  draws artwork → user image → ticker letters, in that order.
- **User-added coins pick an image, not a symbol:** the add-coin screen's
  Icon row opens the system photo picker (`PickVisualMedia`); the picked
  image is center-cropped square, scaled to 192px, and copied into
  `filesDir/coin_icons/` (the picker's grant dies with the process — the
  badge has to be our own file), with the stored name in the new nullable
  `CoinSpec.icon` (backward-compatible, `ignoreUnknownKeys`).
- **"Fibercoin" everywhere:** every user-visible "fiber coin(s)" in strings
  and the wallet copy now reads Fibercoin — one brand word, matching the
  chain family's actual name.
- **Killswitch card button:** the "Always-on VPN settings" tonal button lost
  its `contentPadding = 0.dp` override, which had the label riding the
  pill's rounded edges (and spilling out at larger font scales).

**Verified on AVD `skywire` (light theme, PIN 1234):** launcher drawer shows
the smaller cloud sitting with the same margins as its neighbors; hub hero
reads "Killswitch off" in red text and the wallet tile lists all four coins;
SkyVPN's Killswitch/Transport/Route-length cards render as one bordered
family and the Always-on button holds its label with real insets; the
battery flow was walked end-to-end — whitelist removed via
`cmd deviceidle whitelist -`, Settings offered Allow, the system dialog's
Allow flipped the card to its granted text immediately on return, no app
restart; the coin sheet shows the four real logos; a Testcoin was added with
a pushed test image through the photo picker — preview in the Icon row, then
the badge on the coin chip and sheet, surviving from app-private storage.
Debug APK built and installed via Android Studio JBR.

---

## 2026-08-07 — The wallet learns Ethereum: ETH and USDT (and any ERC-20)

**Built (wallet-core, new `eth` package — plain JVM, host-tested like the
rest of the money code):**

- `EthCrypto`: Keccak-256 (BouncyCastle's `KeccakDigest` — the original
  Keccak, not FIPS SHA3), BIP 44 `m/44'/60'/0'/0/i` off the existing Bip32,
  EIP-55 checksummed addresses, and strict parsing (mixed case must be the
  exact checksum; `0x` required).
- `Rlp`: encoder only — this wallet authors RLP, never parses it.
- `EthTxn`: EIP-1559 type-2 build/sign. The signature is the existing
  `Secp256k1.signCompact` — Ethereum's recoverable form is Skycoin's wire
  format over a different hash, and low-S means the recovery id IS yParity.
  Plus the two ABI call datas the wallet needs: `transfer(address,uint256)`
  and `balanceOf(address)`.
- `EthRpcClient`: JSON-RPC (balance, pending nonce, chainId, EIP-1559 fee
  data, estimateGas, eth_call, sendRawTransaction) plus history via the
  etherscan-style `?module=account` API, which Blockscout serves keyless —
  plain RPC cannot list an address's past transactions, the same reason BTC
  uses an esplora server.
- `EthWalletCore`: one WalletCore for the native coin and any ERC-20 token —
  a token send is the same transaction with the value moved into `transfer`
  call data and gas still paid in ETH. **Unit choice:** the seam's amounts
  are 64-bit, and wei overflows them at ~18.4 ETH — so the native coin is
  carried in gwei (exponent 9) and a token in its own decimals capped at 9,
  with wei arithmetic in BigInteger strictly inside the core. Sends pick the
  funded address (account chains have no change side), pad the node's gas
  estimate by a fifth, price at 2×baseFee+priority, and refuse a token send
  whose address lacks gas ETH with a new `WalletException.InsufficientGas`
  that says exactly that.

**Built (app):** `CoinKind.ETH`/`ERC20`; ETH and USDT
(`0xdAC17F…1ec7`, 6 decimals) ship built in; the add-coin screen grew a
Fiber-coin / ERC-20 toggle so any token is user-addable (contract +
decimals, checksum-validated). Fee UI: a gas card that shows rather than
asks (EIP-1559 prices itself; "calculated at review", then the worst-case
fee, gas limit and gwei ceiling), review-sheet branches for ETH (amount +
fee ≤ totals) and tokens (no total row — amount and fee are different
currencies), history fee lines always in ETH, `0x…` hints, and honest Max
notes (native holds back gas headroom, tokens send everything).

**Verified:** vector tests in `EthVectorsTest` — Keccak reference outputs,
the four EIP-55 examples plus corrupted-case rejections, the standard test
mnemonic's first two addresses (`0x9858EfFD…`, `0x6Fac4D18…`), the RLP
examples from the design docs, and the **EIP-155 worked example reproduced
byte-for-byte** (our RFC 6979 nonce yields the document's exact signature —
RLP, Keccak, low-S and recovery id pinned in one assert), plus a type-2
sign→recover round trip. `EthLiveTest` (opt-in, `SKYWIRE_NET_TESTS=1`) ran
against production: publicnode RPC balance, Blockscout txlist history, USDT
`balanceOf` and tokentx transfers all parse (2 tests, 0 skipped, 12.5 s).
Full wallet-core suite green; `:app:assembleDebug` BUILD SUCCESSFUL. Not
yet sent-and-received on-device with real funds — the SKY wallet's 2-coin
round-trip equivalent still wants doing for ETH/USDT.

---

## 2026-08-07 — Eight more from use: the cloud jump, the hero's exit line, live tile numbers, chat file verbs, upload progress, a DEX list view, and heartbeats out of the room

**The cloud that did nothing.** Tapping the raised Skycoin button from inside a
screen the hub had pushed (SkyVPN, Chat opened from a tile) appeared to be a
no-op. It was `navigateToTab`'s `restoreState`: popping to Home saved the
`[hub, pushed screen]` stack, and navigating to the hub restored it — pushed
screen back on top, "nothing happened". The cloud now has its own
`navigateToHub()`: same pop-and-save, no restore, so it always lands on the
services list itself. Tabs keep their save/restore behaviour untouched.

**SkyVPN hero: the exit line and the killswitch light.** The exit line is now
flag + country name + the first 6 characters of the exit key (`🇩🇪 Germany ·
02ab4f`). The exit's own IP was asked for and is genuinely not knowable from
this phone: service discovery's geo carries lat/lon/country/region only
(`pkg/geo.LocationData`), and the VPN handshake carries a public key, a TUN IP
and a gateway — no public address in either direction (the constraint
NetworkAddressCard already documents). The "Shared carrier address" chip —
the *device's* address, which SkyVPN never changes — is gone from the card;
the bottom row is hops plus a killswitch that is now a state light: one
shield icon, green (`successBright`) armed, red (new `SkyAccents.dangerBright`)
not, words in the content description where the unreadable caption used to be.

**Live numbers on the hub tiles.** Three tiles now carry their one number:

- *SkyChat: unread count.* The page's seen-counters live in its localStorage,
  and the WebView is torn down when the tab closes — so the count is now a
  server arrangement. skychat gained `/unread`: the browser UI POSTs its
  total whenever `updateUnreadBadges` changes it, and between reports every
  inbound message bumps the estimate — counted in `recordEvent`, the one
  choke point every surface (DM, group, pair, files) already funnels
  through. The hub polls it via `SkychatApi.unread()` while the app runs and
  draws a filled pill beside the status dot (`99+` cap).
- *Wallet: the active SKY wallet's balance* (`12.5 SKY`) as the subtitle,
  read from the wallet cache only — the wallet tab owns talking to the node,
  and the hub must render with the node unreachable.
- *Fleet: visors connected*, when the ingest is on. Counted from
  `visors-summary` folded into the hub poll as every third pass (15 s) —
  the Summary-RPC-per-remote fan-out documented in FleetViewModel tolerates
  nothing faster.

**`__skychat_group_heartbeat__` rendered as a sent message.** The live
subscriber path always filtered heartbeats (session.go `onUpdate`), but
*replay* did not: `replayHistory` handed every decoded leaf to the handler,
so each reconnect/join resurrected the owner's liveness probes as chat, in
groups and channels both. Filtered in three layers now: the replay decode
loop (before the cap window, so probes cannot displace real history), a
guard at `groupInbox.deliver` (the filter `IsHeartbeat`'s own doc promised
pkg/visor would apply, and never did — also keeps `last_message_at` honest),
and a read-side filter in `GroupHistoryPage` for stores already written by
older builds. The page filters defensively too (SSE + history rows), for
rings and caches predating all of this.

**Chat file verbs into the message menu.** The `download` / `re-request`
links under file bubbles moved into the per-message menu (⋯), leading it:
Download for the served copy (same `/files/` rule the old link used, via a
real anchor click so the Android host still turns it into a DownloadManager
download), Re-request when this device lacks the bytes. The caption keeps
name and size; the bare file card keeps its one-tap download icon — a card
with every action behind a menu would have no affordance at all. And sending
a file now shows a live counter: `/send-file` goes over XHR (the one thing
fetch cannot do is upload progress), a `2.1 MB / 10 MB` line under the bubble
fills continuously, holds the full reading for a beat at completion, fades,
and is removed. `resendFile` got the identical treatment.

**SkyDEX: cards or a list, every section.** The trading page is a vendored
built bundle, so this rides the same injection lane as the phone stylesheet:
a Cards/List switch at the top of `.content` (re-inserted by the same
MutationObserver pass that re-applies the data-labels; choice persisted in
the page's localStorage). List mode flattens the labelled cards into hairline
rows showing only the primary pair — the first two kept columns, Amount and
Price wherever the table has them — with everything else, actions included,
arriving when the row is tapped open (chevron, not a per-row footer). The
market grid gets the same reading: amount and price on the line, seller and
Buy behind the tap. Card mode is exactly what shipped before.

**Wording.** The chat link bar's "Connecting to X over Skywire…" became
"Establishing connection with X over Skywire…" — the route comes up between
two visors, and the old phrasing made the peer sound like a server.

**Verified:** `go build` over the three touched package trees;
`pkg/skychat/group` Replay/Heartbeat/History and `pkg/visor` Group* inbox
tests pass; both of the chat page's script blocks pass `node --check`;
`make android-mobile` rebuilt the payload (64,028,968 B) and
`make android-apk-debug` came out BUILD SUCCESSFUL (74 MB debug APK). Not
yet exercised on a device — the badge poll, the upload counter and the DEX
list toggle in particular want an on-phone pass.

---

## 2026-08-07 — Six from a test pass: contrast, the nav cloud, back, battery, hang-up, and the first message

Six problems reported after using the app on a phone. Five were small and
local; the sixth was a design gap that a peer-to-peer chat has and a
server-backed one does not.

**Light theme: the invisible controls.** The report named the voice-message
play button and the call screen's mic/speaker. Both were the same bug, not a
palette that was slightly too pale: *white ink on a light surface*. The call
screen's `CallButton` tinted every glyph `Color.White`, which is right for the
two filled buttons (Answer green, Hang up red) and wrong for mic and speaker,
which sit on `surfaceVariant` — a near-white card tint on the light theme.
Those two now take the theme's own ink and only switch to `onPrimary` once the
blue fill is under them; Speaker also takes a different glyph off than on, so
its state is legible without reading colour. In the chat page the voice player
was `color: #fff` on `rgba(255,255,255,.16)`, correct on a sent (blue) bubble
and invisible on a received one. Ink and disc are now variables (`--vm-ink`,
`--vm-face`) set per bubble side, and the waveform canvas — which was a
hardcoded `#f1f5f9` — reads its colour from its own computed `color` and dims
the unplayed half with `globalAlpha`, so one value covers both halves without
mixing an rgba string for an unknown ink. A `MutationObserver` on
`data-theme` repaints the players when the app's theme flips under them:
painted pixels do not re-colour themselves. The in-page call panel's filled
states (`.cp-ctl.hangup`, `.cp-ctl.muted`) now name the ink for the colour
under them. Separately the light scheme's `outline` went from `#7C8AA0` to
`#56657C` (~4.9:1 on white): it is not only hairlines, it tints the bottom
bar's resting icons, and at the mock's value those read as switched off.

**The nav cloud sat inside the chat composer.** The raised Skycoin button is
drawn with `offset`, which moves painting but not measurement, so the bar
measured as the shell alone and Scaffold handed screen content the strip the
cloud was standing in. The bar now reserves the lift with `padding(top =
LIFT)`. Every screen loses 21dp it was never really allowed to use, and no
screen has to know the cloud exists.

**Back left the app instead of stepping back inside it.** SkyChat's header ←
called the tab's own back, so it threw the reader out of SkyChat from an open
conversation; the phone's back gesture already walked the page's history. Both
now run one rule — page step first, leave only when there is none. SkyDEX got
the same plus a middle step: the trading page's own screens (it is React and
routes with pushState), then the market picker, then the hub. And the two hub
tiles that are also bottom-bar tabs — SkyChat and Wallet — are now *pushed*
rather than switched to: going the tab way rewinds the stack to Home, so
backing out of either landed on Home rather than on the hub they were opened
from. Tab roots use `leaveTab()`: pop if there is anything behind, Home only
when there is not.

**Battery optimisation did nothing.** `openRequest` was handed the
Application by both callers, and `startActivity` from a non-Activity context
throws without `FLAG_ACTIVITY_NEW_TASK` — caught by the `runCatching`, so
Home's Allow silently did nothing and Settings reported "this phone has no
battery-optimisation screen". One flag. Verified on the emulator: logcat
shows `START … REQUEST_IGNORE_BATTERY_OPTIMIZATIONS … flg=0x10000000 …
result code=0`, and `dumpsys deviceidle whitelist` afterwards lists
`user,com.skycoin.skywire`.

**Hang up took up to five seconds.** The call screen is drawn off
`VoiceCalls`, which a watcher fills from a 2s poll — so the red button did
nothing visible until the next tick. Hang up and Decline now remove the call
from the shared state *before* the request goes out and suppress it until the
visor agrees it is gone; a request that fails un-suppresses it and the poll
puts the screen back. The watcher's delay is also nudgeable, so the confirming
poll happens at once rather than up to a tick later.

**The first message to a cold peer.** The real one. A DM send has always
dialled on demand, inside the first message's own request: for skynet that
means planning and building a route, which can take the better part of a
minute, and while it ran the sender saw a message sitting there with nothing
to say for itself — so they sent another, and another, each starting a dial of
its own, and concluded the app was broken.

The handshake is now something the UI can ask for and talk about.
`dm.Controller.Connect` is `Send`'s dial without the send; `/link` in skychat
wraps it — `GET` reports (never dials, so polling is free), `POST` starts one
if needed and answers immediately, because a route must not hold a browser
request open. The page asks when a conversation opens, shows a bar above the
composer while it runs, and *holds* what is typed in the meantime: the bubble
opens as a new `queued` status (an amber clock, before `pending` in the
monotonic ladder) and goes out — in order, one at a time — the moment the link
is up. A failed link says why and offers Retry and Send anyway, so a held
message is never a trapped one; the same is true if the app has no `/link` at
all, which stands the whole mechanism down for the session rather than holding
messages for a handshake nobody is performing. A `queued` bubble that outlives
its page becomes `failed` on reload, which is the state that has a Resend
button.

**Verified on the emulator (light theme, Pixel-class 1080×2400, debug APK with
a fresh `make android-mobile` payload):** the composer sits clear of the cloud;
SkyChat's ← returns to the conversation list from an open chat; the battery
grant round-trips (above); a received voice bubble's play button and waveform
resolve to `#000` on `#E4E7EC` in light and `#fff` in dark (measured through
the WebView's DevTools, `getComputedStyle`), with the sent bubble unchanged at
white-on-blue; and a DM to an unreachable peer showed "Connecting to
024ec474…58c7 over Skywire… · 1 message waiting" with the bubble on the clock,
then "No route to 024ec474…58c7 yet — they may be offline." with Retry and
Send anyway. Feeding the page a ready link drained the queue (held 1 → 0) and
advanced the bubble `queued` → `pending`. `go test ./cmd/apps/skychat/...
./pkg/skychat/dm/` green, including new coverage for `/link` (GET does not
dial; malformed, empty and null keys and unknown networks are refused; auto
prefers dmsg). `pkg/skychat/group` times out at 600s — confirmed pre-existing
by running it on a stashed tree.

---

## 2026-08-07 — SkyVPN: real rates, route length, killswitch, and an honest address

Four things asked of the SkyVPN card and screen. Three were straightforward;
the fourth turned out to be impossible as literally requested, and the
interesting part is why.

**Down and Up were always zero, and the fields were never going to work.**
`AppConnection.upload_speed` / `download_speed` are not derived from the byte
counters. The route group exchanges them inside its ping/pong keepalive —
`handlePingPacket` stores whatever throughput the far side announced, and the
download figure is the remote throughput echoed back — so they need an active
route group, a cooperating exit and a completed ping round. On a phone they sit
at zero and never move. The SkyVPN screen had quietly known this for a while:
its rate row was already behind a `> 0` guard, so it simply never appeared.

`bandwidth_sent` / `bandwidth_received` do move, so the new `RateSampler`
measures the rate here instead — bytes gained since the previous sample over
the time between them, on `elapsedRealtime` so a clock correction cannot
produce a rate in gigabytes. A counter that goes backwards means the app
re-dialled and ends the series rather than reporting the difference. Nothing
new is asked of the visor; the numbers come from a poll already being made.

**Route length is now a control.** `min_hops` is a router knob — 1 allows a
direct route, 2 or more forces intermediaries so no single node sees both who
is asking and what is being asked. The card offers 1/2/3 with those words
rather than a bare number, since "min_hops" decides nothing for anyone. The
existing `setTransportPreference` already documented why the PUT must be
read-modify-write (it applies every field, so sending one alone would send
`min_hops: 0`, which the router reads as routing disabled); both setters now go
through one private `updateRouterSettings` so no future one rediscovers that.
Changing it re-dials a running tunnel — the setting only takes effect when a
route is built, so an established tunnel would otherwise keep the hop count it
was dialled with while the screen claimed otherwise. Unlike the transport
order, which the phone owns and re-pins every launch, this one is the visor's:
it persists it, and the phone profile's routing edit starts from the existing
object, so it survives.

**Killswitch state is on the card**, read from the phone's own preference so it
is right with the core down — which is when someone checks whether they are
still covered.

**The before/after IP cannot be done the way it is normally done, and showing
it anyway would have been a lie.** `SkyVpnService` excludes this app's UID from
the tunnel, and it has to: the visor is a child of the same UID and its dmsg
traffic is what *carries* the tunnel. So any address probe the phone makes —
whether from Kotlin or from the visor — leaves through the underlay whether or
not SkyVPN is up. It would print the same address twice and read as broken.
That is also why the desktop CLI's "Your current IP" does not port: that
process is inside the tunnel; this app is not.

Nor can the exit's address be asked for. The VPN handshake is
`ClientHello{UnavailablePrivateIPs}` and `ServerHello{Status, TUNIP,
TUNGateway}` — private `192.168.255.x` addresses and a public key. Neither side
carries a public address, so there is no field to read.

So the card shows the two things that are true: the address this device reaches
the network from, which SkyVPN does not change, and the country the traffic
leaves from, which is the thing that does. The device address comes from
`Overview.public_ip`, already on the wire in a poll being made anyway — but it
is not safe to print raw. The visor writes the *NAT type* into that field when
STUN fails and leaves it empty behind symmetric NAT, so `publicIpOrNull` gates
it and the UI says "Shared carrier address" or "Not discoverable" rather than
rendering the word "Blocked" as though it were an address. A note under the
rows explains why the device's own address does not move.

A real exit IP needs a public-address field added to `ServerHello`, on both
ends, with version-skew handling — deliberately not done here.

**Verified** on emulator-5554 against a live core. The hero card renders the
device address, hops and killswitch chips alongside the stats row; the SkyVPN
screen renders the Network address card with its explanation and the Route
length control. Tapping "2 hops" flipped the hint to the multihop wording and
`routing.min_hops` in the visor's own config became `2`; tapping back restored
`1`. The emulator sits behind NAT, so the device address correctly reads
"Shared carrier address" — the symmetric-NAT guard doing its job on the first
device it met.

Not verified: Down/Up showing non-zero. That needs a tunnel actually carrying
traffic to a reachable exit, which the emulator has not established; the cells
render "—" from the null path, which is the same code path with no samples yet.

---

## 2026-08-07 — CI for the app, and a release lane behind a `mobile-v*` tag

Two workflows. Neither touches the desktop lanes.

**`android-app.yml` — pull requests that change `android/**`.** Builds the
pure-Go payload, runs `:wallet-core:test`, assembles the debug APK and keeps it
as an artifact for triage. A PR that does not touch the app runs none of it.
The Go half is deliberately *not* under the same path filter and is not
duplicated here: `test.yml`'s existing `android` job builds the arm64 payload
and enforces its size budget on every PR, which is what catches a Go change
breaking the mobile variant. The payload is still built in this lane, though —
an APK assembled without the `.so` is not the artifact we ship, and packaging
is part of what is being checked. `LiveNodeTest` is already gated behind
`SKYWIRE_NET_TESTS=1`, so the suite stays offline.

**`android-release.yml` — push `mobile-vX.Y.Z`.** The prefix is what keeps the
lanes apart: `release.yml` fires on `v*`, and `mobile-v1.0.0` does not match
it, so tagging the phone never starts a Skywire release.

The tag is the version. `mobile-v1.2.3` becomes versionName `1.2.3` and
versionCode `10203` (`major*10000 + minor*100 + patch`), passed as Gradle
properties; `build.gradle.kts` falls back to the committed values so a local
build still needs no arguments. Minor and patch are rejected above 99, since
the scheme stops being monotonic there and Play and F-Droid both require the
code to only ever increase.

The payload is the **NDK/cgo** lane, not the pure-Go one used for CI and the
emulator: cgo resolves DNS through bionic's `getaddrinfo`, which is the only
path that honours the phone's real resolver configuration.

**On signing, the workflow refuses rather than improvises.** `assembleRelease`
has always produced an unsigned APK, and unsigned means uninstallable; the
debug APK is installable but is `debuggable`, which is not a thing to hand to
users of an app holding wallet seeds. So neither is a fallback. The build takes
its keystore from the environment — absent, `release` stays unsigned exactly as
before, so `make android-apk` is unchanged — and the workflow fails at a
preflight step, before the ten minutes of build, printing the `keytool` and
`gh secret set` lines needed. A later step re-verifies with `apksigner` and
fails if the APK came out unsigned anyway, which is also what catches the
unsigned build (its filename is `app-release-unsigned.apk`, so the expected
path is simply missing).

Four secrets are required and are not yet set: `ANDROID_KEYSTORE_BASE64`,
`ANDROID_KEYSTORE_PASSWORD`, `ANDROID_KEY_ALIAS`, `ANDROID_KEY_PASSWORD`. The
key must be generated once and kept forever — Android will not upgrade an app
signed with a different key, so this key is what lets every future release
reach whoever installs the first one.

Release notes range over the previous `mobile-v*` tag; left alone
`--generate-notes` walks back to whatever tag came last, usually a desktop
release, and the changelog would be every Skywire commit since. The APK ships
as `skywire-X.Y.Z-arm64-v8a.apk` with a `.sha256` beside it, attached to a
**pre-release**. F-Droid and Play come later.

**Verified** by rehearsing the release path locally rather than by reading it:
a throwaway keystore, `assembleRelease` with the properties the workflow
passes, then the workflow's own verification steps. `apksigner` reports Signer
#1; `aapt2 dump badging` reports `versionCode='10203' versionName='1.2.3'`;
the APK is not debuggable. With the keystore variables unset the same command
still produces `app-release-unsigned.apk`, so the local path documented in the
Makefile is unchanged (its help text now names the signing variables). Both
workflows parse. The throwaway keystore and both rehearsal APKs were deleted;
no keystore exists anywhere in the tree.

Not covered: the workflows have not run on GitHub — the first `mobile-v*` push
will fail at the preflight until the four secrets exist, which is the intended
behaviour but has not been observed. Runner-provided values (`ANDROID_HOME`,
`ANDROID_NDK_LATEST_HOME`) are asserted with explicit guards rather than
assumed, since neither can be checked from here.

---

## 2026-08-07 — The phone stops shipping the deployment's survey whitelist

`survey_whitelist` is not a battery field, but it turned up while reading the
survey machinery and it is the same question the hardening pass asked: what on
this device is readable, and by whom. Its keys authorise their holders to fetch
this visor's **log server, system survey and pprof over dmsg** —
`initDmsgHTTPLogServer` builds one allow-list out of them, and
`forward_proxy` uses the same set. On a fleet node that is the point: it is how
an operator inspects machines they run. A handset is not one of those machines.
Nobody deploys a phone, the survey exists for reward eligibility, and this build
sets no reward address — so the keys buy the owner nothing, and each is a party
that can read the device.

Generation was writing seven of them. That is not the conf-service fetch, which
the phone already skips with `--nofetch`: they are embedded deployment defaults,
so `--nofetch` never suppressed them.

Two sites had to change together, and the second is the one that makes it work.
`config gen` writes the field, and `startConfigRefresh` re-reads the key sets
from the conf service every hour and **overwrites** `survey_whitelist` with
whatever it returns — so emptying it at generation alone would have been undone
within the hour, silently. Both now consult one predicate,
`visorconfig.UseDeploymentSurveyWhitelist`, a build-tag pair in the shape the
tpviz and dmsg-ingest gates already use.

Empty by *default*, not always: three ways in survive, deliberately.
`--surveywhitelist` at generation still applies (the flag's keys are kept and
only the deployment's are dropped), `user_survey_whitelist` is a separate field
that `EffectiveSurveyWhitelist` merges and config refresh preserves, and the
hypervisor keys Fleet adds are appended separately by `initDmsgHTTPLogServer`
and are untouched — enabling Fleet still authorises the phone's own hypervisor.
The visor's own PK is always whitelisted, so nothing local loses access.

**Verified** with a control rather than an assertion: the same argv the app
passes, run through both builds. Desktop writes 7 keys; mobile writes none.
Build-tagged tests pin the predicate on each side, since a silent flip would
restore the keys in two places at once. Both tags build; `android-mobile-check`
passes at 63,963,432 bytes.

The refresh half is verified by inspection, not end to end. With the interval
temporarily shortened the loop demonstrably runs and reaches this code path,
but the conf service returned a payload whose `prod` block would not parse
("unexpected end of JSON input"), so neither build could repopulate and the
comparison proved nothing. The temporary timing change was reverted. What
remains unproven is only that a `false &&` short-circuits.

---

## 2026-08-07 — Battery: two periodic jobs the phone was paying for

A survey of everything that ticks inside the visor as the phone configures it
— roughly sixty recurring jobs once the conditional ones are resolved against
the phone profile — looking for work with no corresponding benefit on a
handset. The 5-minute uptime/TPD heartbeat, the one we had flagged, turned out
not to be worth touching: it is twelve remote wakes an hour against the dmsg
keepalive's hundred and twenty, it cannot be disabled by config anyway
(`resolveUptimeTargets` derives the TPD URL from `transport.discovery`,
deliberately independent of `uptime_tracker`), and with no wakelock anywhere in
the app it does not fire during deep sleep at all. Two other things did.

**The dmsg client republished its discovery entry five times too often, on
every visor in the fleet.** `dmsgc.New` builds a `dmsg.Config` literal with
`MinSessions`, callbacks and protocol, and never sets `UpdateInterval`.
`EntityCommon.init` then falls back to `DefaultUpdateInterval` — one minute,
which is the *server's* cadence, because a dmsg server's `AvailableSessions`
changes on every client connect. A client's entry only carries its delegated
servers, which on a settled client never change, and `DefaultConfig` sets
`DefaultUpdateInterval * 5` accordingly. That constructor is simply never
consulted: eleven of the fifteen client call sites in the tree build the
literal. The periodic tick is never short-circuited either — the `SamePubKeys`
guard in `updateClientEntry` only suppresses nudge-driven updates, and the due
timer always makes `due` true — so each one was a signed GET+PUT to
dmsg-discovery over a fresh dmsg stream.

The default moved into `Config.Ensure`, which `NewClient` already calls before
`EntityCommon.init` reads the value, rather than into the one caller. Fixing
`dmsgc` alone would have left the other ten literals wrong and the next one
would have reintroduced it; `Ensure` is documented as the place that ensures
config values are set. The dmsg *server* has its own `ServerConfig` and keeps
its one-minute cadence untouched. Three unit tests pin the invariant, since
what let this survive was that nothing asserted the interval.

**tpviz runs on the phone, and nothing on the phone can reach it.** The
network-visualizer backend is constructed whenever a hypervisor has a local
visor and started by `startUI` — which the phone must call, because that is the
localhost API the app talks to. `Start()` costs a geoip and cache fetch at
boot, a ~4m30s SD/DMSG cache refresh over dmsg-HTTP, and a 2-second websocket
broadcast ticker, all backing routes that this build cannot serve: the mobile
variant embeds no hvui at all.

`tp_viz.enable` looked like the lever and is not one. It is written by the
generator but read nowhere, and it cannot be made to work: `FillDefaults` only
sets it inside its `DmsgDiscovery == ""` branch, so a config that already names
a discovery leaves it false, and an absent block is indistinguishable from an
explicit false — reading it is exactly what produced the `/tp-viz/` 404s the
comment there warns about. So the gate is a build tag, matching the
`hypervisor_dmsg_ingest` pair already doing this for the Fleet ingest client.
Every use of `hv.tpvizServer` was already nil-guarded, including the `/api/*`
routes it mounts at root — and none of those are routes the app calls, since
its service-discovery lookups go through `/api/svc-fetch` rather than tpviz's
root-mounted `/api/services`.

**Verified** on a host build of the mobile variant (`make build-mobile`),
against a config carrying `tp_viz: {"enable": true}` so the tag is doing the
work: `/tp-viz/`, `/api/transports`, `/api/uptimes`, `/api/local-visor`,
`/api/ip-groups` and `/api/health` all 404, while `/api/about`,
`/api/service-health` and `/api/visors-summary` serve 200, and no tpviz line
appears in the log. For the cadence, with the phone profile applied (the
`-i`-forced `lan_dmsg_server`, a genuine 1-minute *server* entity, removed as
the app removes it): four `[dmsgC]` entry updates in the first 37 seconds —
registration plus session nudges — then nothing until a single tick at 5m01s.
The old build would have republished five times in that window. Both tag
variants build, `android-mobile-check` passes at 63,963,432 bytes against the
80 MB budget, and the dmsg suite is green.

**Not done, and next.** The three `while (true)` poll loops in Android
ViewModels that survive backgrounding — Wallet's 30s is the expensive one
because it hits a remote Skycoin node rather than loopback, and its ViewModel
is constructed eagerly at app launch. `AppVisibility.isForeground` already
exists for exactly this. Below that: stcpr/quic/webtransport re-register to the
address resolver every 90s each plus two `Resolve` calls a minute, for
transport types that never establish behind carrier NAT.

---

## 2026-08-07 — Redesign: one visual language for the whole app

The app-wide redesign from the design mock: a new palette, two new
typefaces, a floating bottom bar with the Skycoin cloud on a raised
circular button, and the apps hub rebuilt as a living dashboard. All
Compose; no Go changes.

**The design system (`ui/theme/`).** Primary moves from `#0072FF` to
`#0F7BF4`, with a deep-blue gradient pair (`SkyHeroGradient` for hero
surfaces, `SkyButtonGradient` for the round primary actions) defined once in
the theme. Light is the design's native theme: white background, cards in
blue-tinted near-white `#F7FAFF` behind a `#E7EEF9` hairline (`outlineVariant`
— the border is what makes a card a card at that fill). Dark derives the same
hues on deep navy (`#0A101C` background) rather than grey or black, with a
brighter `#4AA3FF` primary carrying dark ink instead of white. Status colors
consolidate into `SkyAccents` (success `#22C275`, its bright form `#5CF2A8`
for dots on blue, warning `#F59E0B`); the shared `CONNECTED_GREEN` /
`PENDING_AMBER` names survive but now point at the theme, and the four
scattered duplicate hexes (Home ×3, DexScreen's private shadow copy, the log
viewer's hardcoded `#0072FF` INFO blue) point at the same place. A `Shapes`
scale lands at 8/12/16/22/28 — chips at small, buttons and icon plates at
medium, cards at large, sheets and the nav shell at extraLarge.

**Type: Quicksand + Nunito** (both OFL, bundled as single variable-weight
files — minSdk 26, so the wght axis is real). Quicksand Bold carries every
display/headline/title role; Nunito carries body (SemiBold) and label (Bold).
The Skycoin .otf family is gone with its callers. Two findings shaped the
weights: the old family shipped no 500 cut, which is why ~60 screens had
hand-bolted `FontWeight.Bold` onto body roles (all now harmless), and the
first cut at Medium body read *faint* on the tinted cards — user-confirmed,
light mode only. Related fix in the same pass: every `surfaceVariant` card
now sets `contentColor = onSurface` explicitly, because `contentColorFor`
resolves that container to `onSurfaceVariant` and quietly muted every card
title in the app; and light `onSurfaceVariant` sits at `#44536B`, darker than
the mock's caption grey, because that role carries real prose here.

**The bar (`SkywireApp`).** The Material `NavigationBar` is replaced by the
mock's floating shell: a rounded-28 surface with a hairline border and soft
shadow, icons only (per feedback — labels removed, the icon's Rounded form
plus primary tint marks the active tab, Outlined+`outline` grey the rest),
and the Skycoin cloud on a 66 dp gradient disc riding 21 dp above the shell,
ringed in the shell's own color so it reads as punched through, with a slow
`PulseRing` behind it (shared composable; Home's Connect reuses it while
connected). The lift is `offset`, which moves drawing and hit-testing but
not measurement, and Scaffold places the bottom bar last, so the jut wins
the overlap without reserving dead space above the shell.

**The hub is the mock's Apps screen, with live data.** New `HubViewModel`
polls the local summary every 5 s (local HTTP only — nothing crosses dmsg,
unlike the Fleet lesson) for every app's status: the header's "6 installed ·
N running", per-card status dots (green running / amber starting / red
errored / border-grey stopped-or-unknown). Category chips (All / Network /
Finance / Social) filter the grid. SkyVPN is not a tile: it is the hero card,
full-size on or off (feedback), on the deep gradient with the two soft
corner discs, showing status + session length, the exit as a person would
say it — flag emoji plus `Locale` display name ("🇨🇦 Canada"), short key when
the country is unknown, "No exit chosen yet" before the first connect — and
live Down/Up rates plus session data from `appConnections` while it carries.
Its switch turns the tunnel off directly (stop app, then release the
interface — the SkyVPN screen's own order), and turns it *on* from the hub
only when nothing needs a screen: consent already granted
(`VpnService.prepare` returns null) and an exit already saved; otherwise it
opens the SkyVPN screen. The `vpn_last_server` / `vpn_killswitch` preference
keys moved to `VpnArgs` so both owners read the same names. SkyMeet stays
the one dashed coming-soon card.

**Buttons became visible** (feedback: "so transparent, cannot see it").
This M3 revision draws `OutlinedButton` borders with `outlineVariant` — a
hairline here — and standalone `TextButton` actions vanish entirely when
disabled. Every standalone action (Change port, Refresh, Retry, View logs,
Fleet's Logs/Restart, DEX disconnect, VPN system-settings, Settings'
identity/config actions, transport Change) is now `FilledTonalButton` on
`secondaryContainer`; dialog confirm/cancel pairs and inline links (See all,
Show more, Paste, Max) stay text, as convention wants.

**The header (`SkyTopBar`)** is the mock's: left-aligned Quicksand title
with an optional live subtitle line under it, 42 dp rounded-square tonal
back and ? buttons. Same API plus `subtitle`; every screen inherits.

Also: launcher/splash colors follow the palette (`ic_launcher_background`
`#0F7BF4`, dark window/splash `#0A101C` via `@color` instead of framework
black), and Home's Connect is the gradient disc with white Quicksand.

**Verified** on emulator-5554 (1080×2400, API 34): light and dark
screenshots of Home, hub, Wallet, Settings; tab selection and hub
navigation; hero card off-state with 🇨🇦 Canada exit line; the cloud button
opens the hub with the pulse ring visible; core started on-device
(libskywire-mobile child alive in logcat) with the familiar cosmetic netlink
denials. Feedback applied live across three rounds (hero always big, flag +
country name, faint-text fix, icons-only bar, tonal buttons).

**Known gaps, deliberate.** The embedded SkyChat page still wears its own
palette keyed to the old blue — it lives inside the Go `.so` and syncs only
a light/dark boolean, so it needs its own pass (and `make android-mobile`)
to match; visually close, not identical. SkyDEX's injected phone CSS keeps
its dark-only assumption and one `#00000038` card fill. The raster
`skywire_logo.png` stays `#0072FF`-blue where it appears untinted (lock
overlay); on the launcher and the nav cloud it is tinted white over the new
blue, so nothing clashes.

---

## 2026-08-07 — Hardening: what leaves the phone, what is readable on it

Four things that had been carried as open questions since the size work
settled, plus one audit finding.

**Nothing this app stores leaves the device by itself.** `allowBackup="false"`
was already set and already refused Google Drive. It does not refuse the other
exit: the device-to-device transfer that runs when a new phone is set up from
an old one, which would have carried the visor's secret key and the wallet's
recovery phrases onto a second handset. `res/xml/data_extraction_rules.xml`
now declines both `<cloud-backup>` and `<device-transfer>`, and
`res/xml/backup_rules.xml` says the same thing for Android 11 and earlier,
which ignores the newer file — minSdk is 26, so that is real devices rather
than a formality.

Both are `exclude domain="root" path="."` — everything, nothing carved back
in. Naming the two sensitive files would have been narrower and would have
rotted the first time someone added a third store. There is nothing a user
loses: the identity is meant to move deliberately through Export config, and
the wallet is meant to be restored from its twelve words. The seeds and
passwords are sealed under non-exportable AndroidKeyStore keys anyway, so a
transferred copy would have been undecryptable ciphertext; this stops the
ciphertext travelling either.

**The config can be encrypted at rest, and that is the largest piece.**
`skywire-config.json` holds `sk` — the whole of this phone's identity. It is
app-private, so the threat is not another app; it is the device being examined.
New `ConfigVault` seals it as AES-256-GCM under its own keystore alias
whenever the core is not running, and unseals it at start. Two files, never
both meaningful: plaintext while the visor runs (it opens the path it is given
and rewrites it), ciphertext while it is stopped.

Sealing happens after the visor exits, not at generation — that is what lets
the visor's own runtime rewrites survive, since whatever it left behind is what
gets encrypted. It runs in the service's `finally` under `NonCancellable`, so a
crashed core still seals and a cancelled scope cannot skip it and leave the key
in the clear. `seal` writes the ciphertext, reads it back, and only then
deletes the plaintext; a power cut mid-way leaves both, and `unseal` resolves
that in favour of the plaintext. The outcome is never a config that is gone.

The dangerous failure this had to not have: `ensureConfig` treats a missing
config as first-run and generates a **new identity**. A sealed config plus a
caller that forgot to unseal would look exactly like a new phone and would
replace the user's key without a word. So the unseal lives inside
`ConfigManager` itself — `ensureConfig`, `replaceSecretKey` and the readers all
go through it — rather than in the service that happens to call it. The two
synchronous readers (the public key on the identity screen, the redacted copy
in a diagnostics bundle) read through the vault too, so both still work with
the config sealed and the core down, which is exactly when the identity screen
is being looked at.

Off by default and it stays optional. It buys nothing against a running
unlocked phone, and it cannot be recovered if the keystore is wiped — a factory
reset, or on some OEMs removing the screen lock. The Settings copy says so.
Turning it off is confirmed biometrically, because that is a security decision
being reversed; turning it on is not.

**Battery.** The foreground service was already the baseline and was never the
issue: Doze is a separate mechanism that suspends the *network* of non-exempt
apps once the screen has been off a while, and a foreground service does not
opt out of it. Short naps cost nothing — dmsg sessions are TCP with their own
keepalives and resume when the window opens — but over a long idle they drop,
the visor reconnects, and what the user sees is messages arriving when the
phone next wakes rather than when they were sent.

`BatteryOptimization` reports the state and opens the system dialog, falling
back to the full list on OEM builds where the direct intent does not resolve.
It is offered twice: a permanent card in Settings, and a one-time prompt on
Home that appears only *after* the core is running — before that the question
is about a problem the user has not got yet. "Not now" is remembered for good
and silences both. `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` is declared; Play
restricts it to apps whose core function needs it, and an always-on P2P node
that is also a VPN service is on that list.

**The audit found one gap.** FLAG_SECURE covered the four wallet screens that
can show or take a recovery phrase — seed backup, verify, restore, reveal — and
the app lock holds it session-wide when enabled. It did not cover Settings ▸
Replace secret key, which is a field you paste a visor secret key into in the
clear. That is the same class of secret as the twelve words: it *is* the
identity, it cannot be reissued, and anyone who reads it owns the visor. It has
the flag now. `SecureWindow` moved from `ui/wallet/WalletUi.kt` to
`ui/components/`, since it stopped being a wallet concern.

**Size, re-measured rather than assumed:** `libskywire-mobile.so` is
63,963,432 bytes against the 83,886,080-byte budget `make android-mobile-check`
enforces — 76% of it. Debug APK 74 MB.

**Verified on the emulator.** Config encryption end to end, which is the one
that mattered: toggled on with the core down and 9,513 bytes of plaintext
became 9,541 bytes of `skywire-config.json.enc` with no plaintext beside it —
exactly plaintext + 12-byte IV + 16-byte GCM tag — and `strings` on it returns
nothing. Connecting restored the plaintext at 9,513 bytes and the public key
was **unchanged** (`02498cde10…deadef20` before and after), which is the
no-new-identity property. Disconnecting re-sealed it. Settings still showed the
key while sealed. The Home battery prompt appeared once Connected, with Allow /
Not now, on an emulator confirmed absent from `dumpsys deviceidle whitelist`.
The secret-key dialog screenshots as a pure-black 21 KB PNG where a normal
screen is 140 KB — FLAG_SECURE holding.

**Also:** the bottom bar's Skycoin cloud went 44dp → 56dp. It carries no label,
so it has the label row to grow into, and it is the one slot aimed for by shape
rather than read — at 44 it was a slightly large icon among four icons instead
of the centre of the bar. Checked for clipping at the new size; there is none.

**Not covered:** device-to-device transfer cannot be exercised on an emulator,
so the D2D exclusion is verified by declaration and not by observation; Doze
behaviour was reasoned from the platform contract rather than measured over a
long idle; and the keystore-wiped path (factory reset with a sealed config)
is handled with a stated error but has not been provoked.

---

## 2026-08-07 — SkyChat wears the Wallet's design; one header for every tab

Three things, all cosmetic in the sense that no protocol changed, and none of
them cosmetic in the sense that anyone using the app will notice all three.

**SkyChat is redesigned onto the Wallet's palette, in both themes.** The chat
UI is one 12k-line HTML file served by the Go app and shared with the desktop,
and it was a single dark theme built on its own token set — slate greys, a
cyan-ish `#0ea5e9` accent, green section headings. It now carries the exact
tokens from the Wallet design: `#0072FF` on `#000000`/`#FFFFFF`, the same
surface ladder, the same status colours. The old token names are kept as
aliases pointing at the wallet ones, which is what let the whole app re-skin
at once instead of rule by rule.

Light arrives through `prefers-color-scheme`, and either theme can be forced
with `data-theme` on `<html>`. The phone forces it: the app has a
Light/Dark/System setting the WebView knows nothing about, so a phone set to
System-dark with the app pinned to Light was opening a black chat inside a
white app. The theme is named in the URL query (`?theme=dark`) and read by a
script in `<head>` before the stylesheet is reached, so there is no flash of
the wrong theme. `ChatWebView.applyTheme` sets the same attribute on an
already-built document, for the case where the app recomposes without the
WebView being recreated; note the Activity declares no `configChanges`, so a
*system* uiMode change recreates it and takes the URL path instead —
applyTheme is the narrower belt-and-braces path, not the main one.
`loadedUrl` deliberately stores the address without the query, so the theme
is never mistaken for a new URL.

`LocalDarkTheme` is new in `ui/theme/Theme.kt`: `isSystemInDarkTheme()` is the
wrong question once a user override exists, and anything handing a scheme to
something outside Compose needs the answer *after* that override.

Type is the Skycoin face, shipped: `skycoin-regular.otf` and
`skycoin-bold.otf` now sit in the app's own static dir and are `@font-face`d,
so the page needs no network to look right and the desktop chat gets the brand
face too. The family has no 500 cut, so every weight in the file is now 400 or
700 — an unpinned 500 silently resolves to Regular and flattens the hierarchy.
Numerals are tabular throughout. The `'Monaco', 'Menlo', monospace` stack that
public keys used is gone: the Wallet renders addresses in the brand face and
this now matches it.

**Icons: 49 drawn glyphs replace the emoji.** Nearly every icon in the chat
was a platform emoji pasted in as an HTML entity — 📎, 📣, 🔔, ⋮. Three
problems. They are rendered by the platform's font, so the same button was a
flat glyph on one machine and a glossy cartoon on the next. They ignore
`currentColor`, so a menu row that tints itself orange to say "muted" kept a
yellow bell. And they sit on a different baseline at a different weight from
the hand-drawn SVGs the file already had. One `ICON_PATHS` table, one
`icon(name, size)` helper, 24-unit grid, 2-unit stroke, round caps — the
Wallet's drawing. A missing name returns `''` rather than throwing, because an
icon is decoration and a typo in one must not take a menu down.

Four plain-text strings lost their glyph rather than gaining a drawing, since
no markup reaches them: file previews now read as the file name, and the
"attachment" preview as *Attachment*. `_msgPreview` is one of them, and it is
protocol-visible — it rides inside a reply so the quote renders for a peer who
lacks the parent.

**No scrollbars anywhere.** The `::-webkit-scrollbar` block that drew an 8px
bar down every list is replaced by a global `width:0; display:none` plus
`scrollbar-width:none`. Every container still scrolls; only the indicator is
gone. It also reclaims the width, which is why a narrow column used to reflow
the moment its content passed the fold.

**One header on every tab except Home.** Back at the left, the name centred, a
circled ? at the right, and the two round buttons are the same size so the
title sits still as you move between screens. Back on a tab root goes to Home;
on a pushed screen it pops as before. The ? opens a few sentences about that
screen. Fleet's ? keeps opening its own sheet rather than a dialog, because
its guidance ends in a command with a copy button — it just answers "what is
this tab" first now.

The ? replaced the per-screen **Logs** action on SkyChat, SkySOCKS, SkyVPN and
SkyDEX, and each of those help texts says where the logs went. A log viewer is
not wanted from the screen being used; it is wanted when something is wrong,
and then all of them are wanted, which is the list Settings ▸ Diagnostics
already keeps (it lists exactly these four app sources plus core and process).
Fleet keeps its per-visor Logs button: that feed is a remote machine's,
arriving over dmsg, and Diagnostics only knows about this phone. SkyChat's
overflow menu is gone entirely — with Logs moved it held only Reload, and a
wedged page is reloaded from the Retry the error state already offers.

**One bug found while testing, one fixed.** The recording lock pill — the
target a finger slides up onto to record hands-free — was pinned at
`right: 20px` while the composer row's own padding was 16px on a desktop and
12px on a phone, putting a 40px pill 4px and 8px to the left of the 40px
button it is supposed to sit above. On a phone that is a finger sliding past
the target. The row now names its padding (`--composer-pad`) and the pill and
the video self-view are inset by it, so all three are concentric at every
width. The lock threshold itself is a pure 56px vertical distance and never
hit-tested the pill, which is why the gesture still worked while looking
wrong. The phone misalignment predates this redesign.

**Verified on the emulator (Pixel, arm64, `make android-mobile` + a fresh
APK).** Core connected, Chat opened: header shows back / SkyChat / ?, and the
page renders the redesign — drawn QR, address-book and gear icons, the
Connected pill, filter chips with All filled blue, Saved Messages behind a
drawn bookmark on a tinted circle, a grey CHATS label, no scrollbar. Settings
▸ Theme ▸ Light and back to Chat rendered the page in the light theme, every
icon inverting with `currentColor` and the drawn set holding up on white; the
app was pinned to `theme_mode=DARK` beforehand, so a system night-mode flip
correctly did nothing. That path exercises the URL query, since returning to
the tab rebuilds the WebView — `applyTheme` itself is not separately covered. The Chat ? dialog renders its copy and the Settings ▸ Diagnostics
pointer. A conversation shows blue sent bubbles with the tail corner, drawn
delivery ticks, the waveform player, and the touch action sheet with drawn
reply / forward / trash. Go builds clean; a temporary test confirmed both OTFs
and the page are reachable through the embedded FS (549,375 / 67,292 / 74,840
bytes). The lock-pill fix was confirmed on device by the user.

**Not covered:** the desktop chat has not been opened against this build (same
file, so the redesign lands there too, including the light theme on a
light-set desktop); no screenshots of the light theme inside a *conversation*;
the icon set is our own drawing, not the design team's — `android/icon-brief.md`
is the brief for replacing it.

---

## 2026-08-07 — Wallet: SKY, fiber coins and BTC, keys never leaving the phone

The Wallet tab is no longer a placeholder. It is a native Compose wallet for
three kinds of chain behind one interface: Skycoin, any fiber coin the user
adds (same daemon, their node URL), and Bitcoin mainnet. Seed generation,
address derivation, transaction construction and signing all happen on the
phone; the network is asked only for balances, history and broadcast.

### 1. `:wallet-core` — the crypto is a port, not a binding

The decision the wallet hinged on: how to run Skycoin's crypto on Android.
gomobile would have meant a second Go artifact next to the visor payload and a
JNI boundary for key material. Instead the needed slice of the reference
implementation is ported to pure Kotlin in a new `:wallet-core` JVM module —
no Android types, so every byte of money-handling code runs under host-side
unit tests. BouncyCastle's lightweight API supplies the secp256k1 curve math,
RFC 6979 nonces and RIPEMD-160; the JCA "BC" provider is never registered
(Android ships its own crippled copy under that name).

Ported faithfully from the reference repo: the deterministic keypair iterator
(`secp256k1Hash`, the sha256-until-valid step, the chained wallet seed), the
address codec (`ripemd160(sha256(sha256(pub)))`, version byte, 4-byte
checksum), the skyencoder transaction wire format (little-endian, u32-prefixed
slices), inner-hash/sign-hash construction, and the whole of
`transaction.Create`: MinimizeUxOuts spend selection with its three-phase
ordering, `ceil(hours/burnFactor)` fee, proportional hour distribution with
the remainder rules, the force-an-extra-input change-hours recovery, and the
retry at full share when change hours would otherwise burn. Burn factor, max
decimals and the size cap are read from the node's `/api/v1/health` at plan
time, so a fiber chain with different rules is honored automatically.

Signatures are the one deliberate deviation: the reference signer draws a
random nonce; ours is RFC 6979 deterministic, then low-S normalized with the
recovery id flipped to match — the chain verifies recovery and malleability,
not nonce provenance, and a phone's RNG is the one component with a track
record of losing coins.

Bitcoin is the same shape: BIP 39 → BIP 32 → BIP 84 (`m/84'/0'/0'`), P2WPKH
receive and change chains, destinations in every standard form (base58check
P2PKH/P2SH, bech32 v0, bech32m v1+), BIP 143 sighash, DER low-S, RBF
signaled, dust folded into the fee, and an esplora client (mempool.space by
default) for the chain view and sat/vB presets.

### 2. Tests are against the reference implementation, not against ourselves

`wallet-core` carries 18 host-side tests, and the load-bearing ones compare
against outputs of the Go implementation rather than hand-computed values:

- The cipher testsuite's golden files: seed → secret/public/address chains
  must match, and every stored signature must recover to its stored pubkey
  through our port.
- A Go fixture generator (run against the local reference repo) emits five
  `transaction.Create` cases — change, multi-input, send-all, exact-amount
  with the extra-input recovery, zero-hour mix — and the Kotlin port must
  reproduce the chosen inputs in order, every output's coins and hours, the
  inner hash and the **byte-for-byte serialization**.
- The BIP 84 chain from the canonical test mnemonic, generated with the Go
  repo's own bip32+segwit code, must match — and does, including the BIP's
  published first address.
- The BIP 143 native-P2WPKH example: our sighash equals the vector, and
  because the BIP's example signature is itself RFC 6979, our DER signature
  matches it byte for byte.
- `LiveNodeTest` (opt-in, `SKYWIRE_NET_TESTS=1`) parses production
  node.skycoin.com balance and 150-transaction history through the real
  client.

### 3. App side: sealed seeds, cached truth, one ViewModel

Seeds are sealed with a new AndroidKeyStore AES-256-GCM key
(`skywire_wallet_seed` — deliberately not the service-password key; coins and
passwords must not share a blast radius). The key is not auth-bound: address
derivation legitimately runs without a prompt, and a keystore-enforced prompt
would silently brick the seed the day the user removes their screen lock.
Every send and every reveal instead goes through the shared `Biometrics`
confirm — the phone's own credential — before the seed is touched, and the
prompt states the consequence *before* authentication, never after.

Wallet metadata (addresses are public) lives in the `wallet` DataStore, so
opening the app never decrypts anything. Each wallet's last good chain view is
cached to disk; when the node stops answering, the screen keeps the cached
numbers under an amber banner naming the time it was last true, and Send is
disabled — a wallet that cannot reach a node can still be read, but not
spent from.

The whole flow shares one Activity-scoped ViewModel: the freshly generated
phrase and the send draft live in memory only and never ride in navigation
arguments. `FLAG_SECURE` is set per-screen (backup, restore, reveal) through a
helper that on dispose respects the app-lock preference already holding the
flag session-wide.

Screens follow the wallet design set: coin chip → balance → per-chain
sub-line (Coin Hours for the Skycoin family, confirmed outputs for BTC),
Receive with QR and the same-seed address sheet, Send whose fee card is the
only thing that changes between chains (hours burned/after vs sat/vB presets
and slider), review sheet with exact figures, result screen with the txid,
history with filters and day groups, transaction detail with explorer
link-out, and the wallets manager (rename / reveal / remove, plus create and
restore for more wallets per coin). QR scanning is zxing's embedded capture —
the one camera dependency — and paste/scan both strip `skycoin:`/`bitcoin:`
URI prefixes.

Terminology fixed after review: Skycoin is the original chain, fiber coins
are separate chains built from its codebase. The review sheet names the
coin's own network ("on the Skycoin network", "on the <coin> network"), and
only actual fiber coins carry the fiber label in the coin sheet.

Cleartext policy changed from a loopback whitelist to base-allow: the shipped
Skycoin node is plain HTTP, and user-entered fiber nodes are free-form (an
operator's bare IP, usually without TLS) so they cannot be whitelisted by
domain. Nothing secret rides those connections — signing is local and chain
data is public.

### 4. Verified — with real coins on the production network

Unit: 18/18 `:wallet-core` tests green (`./gradlew :wallet-core:test`).

Emulator (API 36 arm64, PIN set), against production infrastructure:

- Create: intro → twelve-word grid (screencap returns black — `FLAG_SECURE`
  held; content verified through the accessibility tree) → quiz rejected a
  planted wrong word naming its position ("That is not word 6.") → activated.
- The derived address was accepted by node.skycoin.com, and the first refresh
  wrote the cache snapshot.
- **2.000 SKY was received from a real wallet** (txid `86f1aa99…9f5cc5`):
  balance 2.000 SKY / 27,243 Coin Hours, green `Received +2.000` row,
  detail screen with Confirmed pill, the sender's fee of 6,054 hours and
  confirmations counting.
- **Sent the 2.000 SKY back through the app** (Max): the fee card projected a
  2,725-hour burn — exactly `ceil(27,243/10)` — the review sheet promised
  amount 2.000 / burn 2,725 / balance after 0.000 / hours after 0, the PIN
  prompt gated signing, and the node accepted the broadcast:
  txid `bc65baea…b98aedeb`, confirmed on chain with precisely the promised
  2,725-hour fee and 24,518 hours delivered. The counterparty confirmed
  receipt. Balance and history rows updated to the empty, two-transaction
  state.
- Bitcoin: created a second wallet; mempool.space answered (0.00000000 BTC,
  "0 confirmed outputs" sub-line, Send enabled); the fee card showed live
  Economy/Normal/Priority presets and the sat/vB slider; the fresh bc1q
  address was accepted by mempool.space's address endpoint.
- Fiber: added a coin through the form (name/ticker/node URL); it appears in
  the coin sheet as "Fiber coin" and opens its own setup.
- Addresses: generated a second receive address from the sheet; the node
  accepted a balance query spanning both.
- Reveal: PIN prompt with the named-wallet warning first, all twelve original
  words back from the Keystore, 2:00 countdown, black screencap.
- Offline: with wifi+data off, the next refresh tick raised the amber
  "last updated at 05:29, 1 minute ago" banner, disabled Send with its
  caption, and kept the cached history; re-enabling recovered silently.
- Insufficient balance, wrong quiz word, damaged addresses and a wrong-chain
  address all surface their specific errors inline.

Not covered: a fiber chain with non-default verification parameters (none is
publicly reachable to test against), a real BTC spend (needs real BTC; the
signing path is vector-proven), and restore-scan against a wallet with deep
address usage.

The Settings tab was the last placeholder. It holds the four things that are
about the phone rather than about an app — who this visor is, how to get its
config off the device, what guards the app, and where the logs are collected —
plus the theme override and the version card.

### 1. Identity, and the flag that does not exist

`config gen` has no `--sk`. `-r/--regen` takes the secret key from the config
it is about to overwrite (`gen.go:933-947`), so installing a key means writing
it into that file first and letting the regenerate read it back. Everything
else is the first-run pipeline unchanged — same argv, and the phone profile is
re-applied at the next start — with one thing the generator does for free:
`mergeExistingApps` keeps per-app argv instead of rebuilding it.

**The key is validated before anything is touched, and that is not politeness.**
Handed an SK whose public half will not derive, `config gen` does not fail: it
silently generates a fresh random keypair (`gen.go:674-677`). A mistyped paste
would land the user on a brand-new identity with no error anywhere. So the
pasted key goes through `config pk` first, which both validates it and derives
the public key — which is then what the confirmation dialog quotes, so the user
approves the actual outcome rather than a promise.

**New identity deletes the config instead of regenerating one**, so the next
start runs the untouched first-run path. One pipeline for a new identity, not
two.

Both operations clear `local_path` — chat history, app work dirs, transport
logs. A visor's key *is* its identity, and carrying a previous identity's
messages under a new one puts a conversation on screen that nobody can
continue. It also makes the warning true: both flows are confirmed twice, and
the first dialog says exactly what is lost rather than saying "destructive".
`users.db` deliberately stays — the local API account is the app's own device
credential, not part of the visor's identity.

One bug this would have had: `VisorApi` caches this visor's public key for the
life of the process, and every `/api/visors/{pk}/…` route is built from it. After
an identity change the cache addresses a visor that no longer exists and every
call 404s until the app is killed. `forgetIdentity()` is called the moment the
identity changes.

### 2. The one Go change: the CLI printed to stdout on every Android run

`getInterfaceNames()` (`gen.go:2261`) called the standard library's
`net.Interfaces()`, which Android 11+ denies unprivileged processes. It runs at
flag-registration time — i.e. on **every** invocation of the binary — so on
Android it put `Error: route ip+net: netlinkrib: permission denied` on stdout
ahead of the output of whatever command was actually asked for. Harmless until
something parses that output, which `config pk` now does. It now uses
`anet.Interfaces()`, which is what the rest of the repo already uses for exactly
this reason (`pkg/netutil/net_native.go` carries the comment). Off Android anet
is a straight pass-through.

### 3. App lock

`BIOMETRIC_STRONG | DEVICE_CREDENTIAL` on API 30+, `BIOMETRIC_WEAK |
DEVICE_CREDENTIAL` below it — not a preference: the strong pairing is rejected
outright on API 28-29 (`PromptInfo.Builder` throws "Authenticator combination is
unsupported on API n"), and the weak one is what androidx's own deprecated
`setDeviceCredentialAllowed` resolves to there. Fingerprint or face when one is
enrolled, the device PIN/pattern/password otherwise — the phone's own bar, which
is the bar this asks for. `MainActivity` is now a `FragmentActivity` because
`BiometricPrompt` hosts itself in a fragment; nothing else changed, since
FragmentActivity *is* a ComponentActivity and only AppCompatActivity would have
demanded an AppCompat theme.

**The lock is drawn over the app, not in place of it.** Composing the navigation
tree only while unlocked would tear down the back stack and the embedded chat
WebView on every glance at a notification. What keeps content from leaking
anyway is `FLAG_SECURE`, set for the whole session while the lock is on: the
recents snapshot is taken as the app *leaves*, before it is locked and with the
last screen still on it, so blocking it has to be a flag that was already set.
Screenshots go with it, which is the same promise stated the other way round —
and the setting says so.

Two decisions the obvious implementation gets wrong. The lock state starts
*locked*, because a fresh process is exactly the case that must ask, and the
gate ignores it entirely while the preference is off. And a ringing or connected
call is shown *through* the lock: a call screen holds no secrets, and every
dialer on Android surfaces one above the lock screen for the obvious reason.

### 4. Logs & diagnostics

The aggregate home of the shared viewer: every source listed in one place
(core runtime, the captured process output, each app), Export all, and the log
level. Sources are named **product first, process second** — "SkyVPN
(vpn-client)" — because the list is otherwise four process names and the
process name is the part you actually need there: it is what the config calls
it, what the API route is keyed on, and what a log line says. The viewer's own
app bar shows the product name alone; "SkySOCKS (skysocks-client)" does not fit
a centered title, and the row that opened it already showed both.

**Export all writes the config redacted.** A diagnostics bundle is a thing
people attach to an issue and the config carries the secret key, so `sk` is
stripped; the full file has its own deliberate export behind a biometric check
and a warning that names what is in it. Nothing fails the export either — a
source that cannot be collected becomes a line in `collection-notes.txt`, since
the bundle is most wanted exactly when things are broken.

The log level is `log_level` in the config, written from the phone's preference
on every launch like the transport order and the Fleet opt-in, and read once
while the visor builds its module graph — so changing it restarts the core, with
the same confirmation Fleet's toggle uses.

### 5. Smaller

Core version comes from the running visor's summary, not from
`libskywire-mobile.so --version`. The binary would answer with the core down
too, which is tempting, but §2 is why: the CLI writes to stdout before any
command runs, and scraping it means parsing whatever else happened to be
printed that launch. Theme override (system/light/dark) is a Compose-level
choice — the palette stays brand-locked, this only picks which half is drawn.

**Verified on the emulator (Pixel arm64, Android 17 / API 37):**

- Log level: DEBUG chosen with the core down saved silently and appeared as
  `"log_level": "debug"` at the next start; chosen with the core up it asked
  first, restarted, and came back `"info"`. The five chips wrap to two rows —
  the first cut put them in a `Row` and TRACE rendered one letter per line.
- Export all: 10 files, 180 KB. `sk` absent from the redacted config, `pk`
  present, no collection notes. Stopped apps' feeds are empty files, not
  errors (the server's 500 "no new available logs" contract).
- Replace SK: an all-zero key was refused in the core's own words ("invalid
  secret key") with nothing touched; `…0001` derived
  `0279be667e…16f81798` in the confirmation, and after the two dialogs the
  config carried that keypair, `local/` was recreated empty (a fresh 32 KB
  `skychat-history.db`), and Home reported Connected under the new key —
  which is also the proof that the API client dropped its cached identity.
- New identity: fresh random keypair, and every phone-profile pin survived the
  regenerate (`cli_addr: ""`, absolute `local_path`, `bin_path`,
  `dmsg_ingest: false`, `log_level`).
- Export config: the picked document is byte-identical to the on-device config,
  secret key included.
- App lock, with a device PIN set: enabling asked first ("Turn on the app lock"
  / "Confirm it is you"), and from then on `screencap` returns black — FLAG_SECURE
  working. Cold start raises the prompt automatically; away 5 s returns straight
  into the app, away 40 s asks again. Disabling asks too. The emulator has no
  enrolled biometric, so every prompt fell through to the PIN keypad — the
  fingerprint sheet is untested and wants a real device.
- Dark/light both rendered; `config pk` output confirmed clean of the netlink
  line after the Go fix; `go test ./cmd/skywire-cli/commands/config/...` passes.

**Not covered:** the fingerprint/face path (no enrolled biometric on the
emulator), hardware-backed Keystore behaviour, and FLAG_SECURE in the real
recents list — all of it wants a real device.

---

## 2026-08-06 — Fleet: the visors you run elsewhere, seen from the phone

Off by default, and that is the feature. The phone's core ships API-only — the
visor's local HTTP API on loopback and nothing else. The config section it
lives under is historically called `hypervisor`, but on this build it is just
the API. **Enable Fleet** hands that hypervisor its dmsg client, which starts
the RPC listener other visors dial in on; from then on any visor carrying this
phone's key in its own `hypervisors` list connects and reports status here.

The bool (`hypervisor.dmsg_ingest`) already existed from the lite-core work.
What was missing was everything around it.

### 1. The one piece of Go: a restart route

The hypervisor mux had `POST /visors/{pk}/shutdown` and `RestartApp`, but no
way to restart a whole visor. The visor's own `Reload` RPC is exactly that —
close the module stack, re-read the config, run again, same process — and it
was already in the `API` interface and the RPC client. So the route is a
wrapper: `POST /api/visors/{pk}/restart` → `ctx.API.Reload()`.

**It answers 202 without waiting, and it has to.** `Reload` cannot return: it
tears down the RPC transport the call is riding, so the caller's outcome is an
EOF whether the restart worked or the visor died. `cli visor reload` has always
handled it the same way (goroutine, fixed pause, then "Visor reloaded"). The
route reports *dispatched*, not *completed*; the real outcome is the visor
dropping out of `/api/visors-summary` and coming back. Whether the visor was
reachable at all is already settled before the handler runs — `visorCtx`
answers 404/503 for a PK this hypervisor cannot reach.

### 2. The toggle, and what it costs

`hypervisor.dmsg_ingest` is read once, while the visor builds its module graph,
so flipping it means restarting the core. The preference is the source of
truth and `ConfigManager.applyPhoneProfile` writes it into the config on every
launch — same arrangement as the transport order, for the same reason.

`SkywireCoreService.restart()` is **not** a suspend function on the caller's
scope. Fleet is a pushed route: a back press would cancel its view-model scope
somewhere between the stop and the start and leave the phone with no core at
all. It runs on a process-scoped job, serialized behind a mutex, and callers
follow it through `CoreServiceState` like any other lifecycle change. The wait
before restarting is not politeness — the child holds `:8000` and a racing
spawn dies on the bind. `HomeViewModel`'s auth-recovery path (stop → drop
users.db → start) now goes through the same helper via its `between` hook; its
old inline version waited only for `Stopped`, which `Failed` never becomes.

Android 12+ refuses a background `startForegroundService`, and stopping our own
FGS is exactly what can cost us the exemption — so a failed re-start lands in
`CoreState.Failed` with the reason, where Home shows it and Connect recovers.

### 3. What a row says

`/api/visors-summary` per visor: online/offline, version, uptime, **transports
broken down by carrier type** with the total under them (a bare "24" says
nothing; "stcpr 10 / webrtc 14" is the diagnosis on a visor that is reachable
but unroutable), and health. The local visor is filtered out — it is not part
of anyone's fleet and it has the whole Home tab.

Actions: **Restart**, and **Logs** — `/api/visors/{pk}/runtime-logs` resolves
remote visors through the same mux, so the existing viewer got a `visor-<pk>`
source and nothing else changed.

Three things came from using it:

- **Naming.** A row that says 66 hex characters does not tell you which
  machine it is. Names live on the phone (`core/VisorNames.kt`, one JSON map in
  DataStore), not on the visor: Fleet is a read-only window onto those
  machines and a label would be a strange first exception. The cost is honest
  and stated in the dialog — names do not travel to another device.
- **The "Add a visor" instructions are behind the app bar's `?`**, not a
  permanent card. They are read once. The empty list points at it.
- **A snackbar, not a card, for action outcomes.** A restart is fired from a
  card that can be anywhere in a scrolling list and its outcome arrives seconds
  later; the card is the wrong place to say it.

### 4. The bug that mattered: a 5-second poll broke the thing it polled

First cut polled `/api/visors-summary` every 5 s. Every poll makes the visor
fire a `Summary` RPC to each remote over dmsg, and from a phone those routinely
exceed the server's own 5-second budget for them. Polling faster than they
complete stacks calls onto one dmsg stream until it breaks. Measured on the
emulator: the peer cycled `summary RPC slow (>5s)` → `connection is shut down`
→ evicted from `remoteVisors` → redialed, roughly once a minute, forever.

The screen looked *fine* through all of it, because the server keeps serving an
evicted visor from its summary cache for three minutes and renders it
`online: true` — deliberately, so slow peers don't flicker. But `visorCtx`
resolves actions against `remoteVisors`, which no longer had it. So every row
said **Connected** and every Restart answered **503 "currently disconnected
(last seen 1m45s ago) — retrying"**. Two green screens, one broken button.

Three changes, in order of how much they fix:

- **Poll every 15 s.** Nothing here is second-by-second data. This alone ends
  the eviction cycle.
- **Retry a 503 restart** three times at 3 s. The server's message says
  "retrying" and its code comments expect the UI to; a hypervisor client's RPC
  conn idle-closes after ~2 minutes and redials within seconds, so a tap can
  legitimately land in that gap.
- **Say how old the numbers are.** `last_seen_at` is now rendered whenever the
  snapshot is over 45 s old — including under a green dot. An uptime can be
  minutes stale while the row reads Connected, and that is exactly the window
  where actions fail.

### 5. Two things fixed on the way

- **The straight apostrophe is broken in the Skycoin typeface** — huge
  sidebearings render "this phone's key" as `phone ' s`. All of `strings.xml`
  now uses U+2019, which the family has and kerns correctly. Verified by
  screenshot at 2× crop.
- **The log viewer claimed "No log entries yet." while it was still
  fetching.** Harmless for local sources, wrong for a remote visor whose first
  page travels the whole ring buffer over dmsg — measured 8.1 s, then 3.8 s.
  It has a loading state now. The remote feed is also titled with the visor's
  name when it has one, read from the same store.

`AppRouteScaffold` is deleted — Fleet was its last user, every hub route now
has a real screen. `InfoRow` and `formatUptime` were duplicated in HomeScreen
only because the shared `InfoRow` lacked a `valueColor`; it has one now.

### Verified

Emulator `skywire` (1080×2400) + a desktop visor on the Mac
(`02acc53c3d…48c56fd6`), phone `03202fd6a2…f4528ada`, added with **the exact
one-liner the app shows**: `skywire cli config update hv --add-pks <phone-pk>`.

- **Fleet off = no ingest.** Config carries `"dmsg_ingest": false`; the process
  log has `Hypervisor enabled` and `Hypervisor HTTP serving on 127.0.0.1:8000`
  but **zero** `Serving hypervisor RPC over DMSG`. The desktop visor dialed the
  phone **21 times** and failed every one (`dmsg error 202 - cannot connect to
  delegated server`, `i/o deadline reached` on `…:46`).
- **Toggle on.** Confirm dialog → core restarts → config rewritten to
  `"dmsg_ingest": true` → `Serving hypervisor RPC over DMSG addr="03202f…:46"`.
  The desktop visor's `Serving RPC client...` landed **8 s later**, and the row
  appeared: Connected, `darwin_arm64`, uptime, stcpr/webrtc counts, healthy.
- **Restart.** `POST …/restart → 202` (20-byte body). The desktop log unwound
  all 37 modules — `Shutdown complete. Goodbye!` — and re-entered
  `main module set to hypervisor` 0.05 s later; ~6 s end to end. Uptime in the
  app went **2h 29m 42s → 2m 31s**. Snackbar: "Restart sent to test."
- **Offline.** `kill -9` on the desktop visor at 09:07:54; the row read
  **Offline** (grey dot, health `—`, uptime frozen at its last snapshot,
  "Last answered 6m 45s ago — everything above is from then", Logs and Restart
  both disabled) when checked at 09:13:10. The flip is not immediate by
  design: the server holds an evicted visor as online for its three-minute
  cache-freshness window first.
- **The 503 path**, before the poll fix: four attempts logged 3 s apart, and
  the failure reached the user in the server's own words rather than a code.
- **Logs.** A remote visor's own lines render (stcpr re-registration, address
  resolver binding the Mac's IPs, self-probe) under the visor's name.
- **Rename** persists across an app reinstall (DataStore).
- **Toggling back off** is symmetric: config returns to `"dmsg_ingest": false`,
  the core restarts, `Hypervisor HTTP serving` appears and
  `Serving hypervisor RPC over DMSG` does not. The screen returns to the
  explainer and the list is dropped.

**Not covered:** a second remote visor (list ordering with more than one row),
and offline behaviour on a real phone — the emulator's dmsg is slow enough that
its timings are a worst case, not a typical one.

---

## 2026-08-06 — SkyChat Settings, and a chat that can move to the phone

The phone app embeds skychat's own page, so this is a change in
`cmd/apps/skychat` that the phone gets for free — and it is the phone that
needed it. A new visor is a new identity with an empty store: install the app
and every conversation you have had is simply not there, because none of those
messages were ever addressed to this key. Nothing syncs, and pretending
otherwise would need an identity that spans devices. So: a file.

### 1. Settings

The sidebar header had a bell for notification preferences and nowhere for
anything else. It now has a **⚙ Settings** dialog with two sections —
Notifications (the bell's whole contents, moved: permission line, the two
switches, the muted list) and **Import / export chat**. The bell is gone;
it was a second entry point to half of one screen.

Opening Settings still carries the notification permission ask, which is the
one thing about the old bell that was load-bearing: browsers only honour that
request from a user gesture, and the preference defaults to on, so without an
ask on open a fresh profile silently drops every notification.

The dialog is the one that can outgrow a phone viewport (the muted list has no
fixed length), so it caps at `86vh` and scrolls its body with the title and
Close pinned.

### 2. What an archive is

`GET /export` → one JSON file: the address book and every stored message, 1:1
and group. `POST /import` merges one back. `cmd/apps/skychat/commands/transfer.go`.

The line drawn is **data vs identity**. Messages and the names you gave to
keys travel. The visor's keypair, group membership and its key material,
pairing ratchets and in-flight transfers do not — importing a group's messages
puts the conversation on the phone to read; it does not make the phone a
member, which still means rejoining. The UI says so rather than leaving it to
be discovered.

Two details that are Android's, not skychat's:

- **Export is a navigation, not a fetch-and-blob.** `ChatWebView`'s
  `shouldOverrideUrlLoading` already turns a same-origin main-frame navigation
  into a `DownloadManager` request *with the basic-auth header attached* — a
  blob URL built inside the WebView would not get that. Zero new Android code.
- **The filename is in the URL** (`/export/skychat-<date>.json`, a prefix
  route whose suffix the handler ignores). That same path never sees
  `Content-Disposition`, so it names the download from the URL:
  `URLUtil.guessFileName` on a bare `/export` yields `export.bin`, which the
  import picker's `accept="application/json"` then filters out. Import needed
  nothing new either — `onShowFileChooser` was already wired for attachments.

### 3. Import is not Append in a loop

This is the part with a real decision in it. `history.Store` grew
`Import(msgs, groups) (ImportResult, error)`, implemented in both backends:

- **No per-peer rate limit.** It exists to stop a peer filling the disk over
  the network at 20 messages/minute. An operator restoring their own archive
  is not that, and applying it would have delivered a handful of messages per
  conversation — the failure would have looked like a successful import.
- **Duplicates are skipped**, so importing the same file twice changes
  nothing. Identity is the envelope ID where there is one and
  (timestamp, direction, text) where there is not — older plain-text messages
  carry no ID.
- **Every other guardrail stands**: size cap, whitelist, total-bytes cap, and
  the per-peer FIFO cap.
- **It reports what it will not keep.** `Expiring` counts records already
  outside `--persist-ttl` (default 30 days — the sweep takes them within the
  hour) and `Evicted` counts what the per-peer cap pushed back out (default
  500). Both are silent losses otherwise, and "imported 2000" while 500
  survive is the number that sends someone to wipe their old device too early.
  The UI names the flag to change in each case.

`evictOldest` now returns how many it removed, for that last count.

### Verified

`cmd/apps/skychat` standalone on `127.0.0.1:8801` with `--persist`, driven
through the page in a browser:

- Import → export → import round trip: an archive of 2 messages + 1 group
  message + 1 contact imports as `{messages:2, group_messages:1, contacts:1}`;
  exporting reproduces it byte-for-byte in content; re-importing **the
  exported file** reports `{duplicates:3}` and stores nothing.
- The imported contact appears in the sidebar as a conversation (the page's
  `syncHistoryPeers` is re-run after an import).
- Refusals reach the user as the server's own words, not a status code:
  "not a SkyChat archive" (400), "archive is version 99; this SkyChat reads up
  to 1" (400), unreadable JSON (400), `POST /export` (405), `GET /import` (405).
- Settings renders both sections and scrolls; the file input is cleared after
  each pick so choosing the *same* file again still fires.
- Go tests: `pkg/skychat/history` (import table across both backends — not
  rate limited, idempotent, ID-less dedup, cap eviction counted, TTL counted,
  limits rejected, full store) and `cmd/apps/skychat/commands`
  (export contents, persistence-off export, round trip, no-overwrite of a
  local name, the three refusals, method guards). Both pass.

**Pre-existing and unrelated:** `pkg/skychat/group` hangs its test binary past
600 s in cxo connection code. Confirmed on a clean HEAD as well — it is not
this change.

---

## 2026-08-06 — SkyVPN: the phone's TUN, on loan from Android

The whole phone now exits through a Skywire visor. This is the one part of the
app that needed new Go, because the thing vpn-client wants — `/dev/net/tun` —
is the one thing an Android app may never open.

### 1. The handoff: Android owns the interface, the core borrows the descriptor

Android hands out a TUN only through `VpnService`, and only after the user has
granted the system's VPN consent. The core runs as a **child process** of the
app, so it cannot inherit the descriptor either; it has to be sent.

**Go** (`pkg/vpn/tun_device_android.go`, new — the only new Go in the app):

- `newTUNDevice()` returns a device with **no descriptor yet**. It cannot have
  one: the shared client calls it before it knows the address the exit will
  assign, and on Android the address is an argument to creating the interface,
  not something set afterwards.
- `Client.SetupTUN` (`pkg/vpn/os_client_android.go`, new) is therefore where
  the interface actually appears. It sends
  `{"op":"establish","addr":"172.16.0.12/29","gateway":…,"mtu":…,"dns":…}` over
  an abstract unix socket and reads back the descriptor as `SCM_RIGHTS`
  ancillary data (`ReadMsgUnix` + `ParseUnixRights`).
- The descriptor is put in **non-blocking** mode before `os.NewFile`, which is
  what registers it with the Go runtime's netpoller. A blocking one parks a
  `Read` in the kernel with no way out, so replacing the interface — or a
  killswitched stop — would hang the copy goroutine forever. Non-blocking,
  `Close` unblocks it with `os.ErrClosed`.
- Reconnecting to the same exit lands the same address, and then the request is
  satisfied by the interface already up: it is left alone rather than swapped,
  so a killswitched reconnect has no seam.

**Kotlin** (`core/SkyVpnService.kt`, new) is the server on that socket. The
abstract namespace has no filesystem permissions to lean on, so the peer's UID
is the whole gate — `peerCredentials.uid != Process.myUid()` is refused before
a line is read.

### 2. The route calls became Android, not no-ops

`os_linux.go` shelled out to `ip`/`nmcli` and raised capabilities. None of that
exists for an app process, and none of it is needed — `VpnService.Builder`
declares the address, MTU, DNS and routes and the system installs them. So the
client half of `os_linux.go` moved into `os_client_linux.go` (now
`linux && !android`) and `os_client_android.go` says the same intentions the
way Android allows:

| Shared client calls | On Android |
|---|---|
| `SetupTUN` | establish the interface with these parameters, take the descriptor |
| `AddRoute` / `ChangeRoute` | already declared by `establish()` |
| `DeleteRoute` (the two half-space routes) | drop the interface — the interface *is* the route |
| `DeleteRoute` (a `/32` direct route) | nothing: our UID never entered the tunnel |
| `SetupDNS` / `RevertDNS` | travels with the interface |

`DeleteRoute` on the default route is not a shortcut: it is exactly where the
shared client says "stop carrying traffic", and it is only reached with the
killswitch **off**. `tun_device_unix.go` is now `!windows && !android` so the
`water` path stays untouched everywhere else.

### 3. What makes the killswitch real

The service keeps **its own** copy of the descriptor. The core closing its copy
does not take the interface down, so when the tunnel drops there is a live
interface with nobody draining it and the packets go nowhere. Releasing it is
an explicit decision in exactly three places: a `down` from the core (killswitch
off), a control-socket EOF with the killswitch off (the core was *killed*, not
stopped), and the user disconnecting. A crash-restart of the visor
(`CoreState.Restarting`) deliberately keeps blocking; a terminal core stop does
not, because a phone blocked with nothing left to reconnect is a bug, not a
killswitch.

`--killswitch` is set through the visor's own `killswitch` PUT field rather
than by rewriting argv — it is a first-class setter (`SetAppKillswitch`) that
adds the bare flag or strips it. Like the transport preference, the **phone's**
stored value is authoritative and is re-applied to the config whenever the core
comes up.

### 4. Screen

`ui/vpn/` repeats the SkySOCKS shape — service discovery (`type=vpn`), tap an
exit, `PUT pk + killswitch + status`, poll — and adds the killswitch toggle
(with a deep link to Android's own Always-on VPN settings, the stronger
guarantee this app cannot provide itself) and a live stats card from
`…/apps/vpn-client/connections` + `…/stats`. A lifetime byte counter is kept in
DataStore, written in 8 MB chunks rather than on every 2 s poll.

The list/status pieces both screens now share moved to
`ui/components/ServerUi.kt` (`SavedServer`, `ServerRow`, `SectionCard`,
`InfoRow`, the formatters, the status colors).

**One unit bug fixed on the way:** `AppConnection.latency` was decoded as
nanoseconds and divided by 1e6. The visor already converts —
`ConnectionSummary` in `pkg/app/appserver/proc.go` stores
`time.Duration(…Milliseconds())` — so the wire value is milliseconds. Harmless
on SkySOCKS (whose latency is always 0, so the row never renders); it would
have shown every VPN ping as `0 ms`. Field renamed `latencyMs`.

### Verified

Real device (`DM_B70104`, Android 15, LTE): consent → connect to a US exit →
**Connected**, interface `172.16.0.12/29`, stats ticking (↑118.9 KB ↓198.8 KB,
session 55 s). Stopped there — the tunnel takes over the phone's networking and
the device was needed.

Emulator (arm64, API 37), full pass against a Singapore exit
`036433b792…83c74724`:

- **Whole-phone traffic exits at the visor.** From the emulator shell (uid
  2000, inside the tunnel's UID range): `ip-api.com` → **207.148.77.89,
  Singapore, VULTR**. Host egress without the tunnel is `45.56.79.245`.
- **The visor stays outside it.** `ip rule` routes uid ranges
  `0-10230`, `10232-20230`, `20232-99999` into `tun0`'s table — a
  one-UID hole at **10231**, which `pm list packages -U` confirms is
  `com.skycoin.skywire`. That hole is `addDisallowedApplication(self)`, and it
  is what keeps the dmsg traffic carrying the tunnel out of the tunnel.
  `ip route show table 1019`: `default dev tun0`.
- **Killswitch on.** Visor `kill -9`'d (an unclean loss — no `down` sent):
  `tun0` still up at `172.16.0.20/29`, HTTP from a routed UID **times out after
  20 s**. Screen reads *Blocked by killswitch*.
- **Killswitch off.** Same `kill -9`: `tun0` gone within seconds, traffic
  immediately direct again (`45.56.79.245`).
- **Disconnect** releases the interface and restores networking.
- Toggling the switch rewrites the config as expected: `--killswitch` appears
  in and disappears from vpn-client's `args`, and the re-dial establishes a new
  interface (`tun0` index 19 → 20 → 21 across the run).
- Builds: `make android-mobile-check` (63,766,824 B, budget 83,886,080),
  `make android-apk-debug`; `pkg/vpn` compiles for android/linux/darwin/windows
  and `go test ./pkg/vpn/...` passes.

**Not verified, and it needs a real device:** killswitch behaviour under Doze
and OEM background kills, and whether a plain (non-foreground) `VpnService`
sharing the core service's process survives long idle periods on aggressive
OEM ROMs. Also unverified: `adb shell` on the physical device reported the
carrier IP with the tunnel up, i.e. that ROM appears to exclude the shell UID
from VPN routing — the emulator does not, which is why the routing evidence
above was taken there.

---

## 2026-08-05 — SkyDEX: a gate on the trading UI, and a phone layout for it

The two things the SkyDEX screen shipped without, both now closed as far as
this side of the repo can close them.

### 1. The trading UI is no longer open to every app on the phone

**Why it was:** skydex-client's UI and control API had no authentication of
any kind, and Android has no per-app network namespace — a loopback listener
is reachable by every installed app holding INTERNET. Behind that port sit the
live market session, the registered wallet addresses, and placing or
cancelling orders. skychat solved the same exposure with `--password-file`;
skydex-client had no equivalent because the server that serves the UI comes
from the skycoin repo and takes an address, not a listener or a handler.

**Built** (`cmd/apps/skydex-client/commands/auth.go`, new):

- `--password-file`, reading the format skychat already uses — one line of
  `"<hex salt>:<hex sha256(password || salt)>"` — so one writer serves both
  gates. Empty (the default) still serves ungated: **the desktop shape does
  not change**, one server on the configured port.
- With a password set, the wrapper takes over `--addr` with a basic-auth
  reverse proxy and moves the engine to a loopback port drawn fresh at every
  start. The credential is stripped before the request is forwarded.
- Fails **closed**: `--password-file` pointing at something unreadable or
  empty refuses to start rather than quietly serving the surface open.
- Android side: `core/SkydexProfile` (mirroring `SkychatProfile`) pins
  `--password-file` next to the loopback `--addr`, `SecretStore` grows its own
  skydex secret, `SkydexApi` sends the credential on every call, and the
  WebView answers the challenge.

**Verified on the emulator**, with the phone connected and trading:

- From `adb forward` — a different UID, the same reachability another app has
  — `GET http://127.0.0.1:8051/api/status` → **401** with
  `WWW-Authenticate: Basic realm="skydex-client"`; a wrong password → **401**.
- The app's own listeners, read from `/proc/net/tcp` for its uid: 8000
  (visor), 8001 (skychat), **8051 (the gate)**, and a random high port — 33043
  on that run — where the engine actually is.
- Go tests (`auth_test.go`): the record formats that do and do not gate, the
  middleware's three answers, that `gatedServer` moves the engine and proxies
  the path through only with the credential, and that with no password it
  returns the configured address untouched.

**What this does not close, precisely:** the engine's own port answers
ungated — measured, not assumed: `curl` to 33043 returned the full market
status. An attacker must now scan ~28k ephemeral ports, re-rolled at every
app start, instead of connecting to a documented one. The operating system
offers no way to bind a socket only this UID may connect to, so **closing the
last gap needs one upstream change**: `skydexclient.Run` accepting a
`net.Listener` (or exposing its handler). At that point the proxy here
collapses into wrapping a handler and no second port exists at all.

### 2. The page has a phone layout

**Why:** the trading UI declares `width=device-width` and is built on
Bootstrap, but its own ~10 kB of CSS contains **no media query at all** —
every padding, grid minimum and flex basis in it was measured on a desktop.
(The earlier entry called it "desktop-width"; that was wrong in a way worth
correcting — it renders at the real width, it just has no breakpoint.)

It is a built bundle from another repo, so the lever is a stylesheet layered
on top, injected exactly like the header hide and scoped to
`max-width: 600px` so a tablet keeps the layout the page intends
(`ui/dex/DexWebView.kt`). The WebView stopped zooming out to fit
(`loadWithOverviewMode = false`) since the page now has a layout at the real
width; pinch-zoom stays on, because this is a screen full of hex.

**Seen working on the emulator:** all five tabs on one line — **Settings**,
which holds the wallet addresses, was previously off the end of a scroll
strip; the banner's *Open Settings* now a full-width button instead of a word
wedged in a corner; the page's padding no longer spending an eighth of the
screen; and the Settings form one full-width column.

### 3. Tables became cards, because they were unreadable and unreachable

With a live listing on screen the tables turned out to be worse than cramped.
My Listings is **ten columns**; a phone showed the first five and cut the rest
off, and the column past the edge was **Actions** — the one holding Cancel. A
sideways scroller does not fix that: nobody scrolls a table they cannot see
the end of, to reach a button they do not know is there.

Each row is now a card. What a closed card shows is chosen by column *name*,
not position: `Type`, `Amount`, `Price`, `Status`/`Lifecycle`, capped at four,
plus the Actions cell, always, as a full-width button. Everything else — the
id, the escrow address, the transaction hashes, the timestamps — is behind a
**Details** toggle on the card. Amount and Price take the weight the market's
own product card gives them (plain and accent), so a row reads as a trade
rather than a grid.

Two judgement calls worth recording:

- **A closed card shows one status badge, not the chain.** The lifecycle is
  three badges and two arrows — "Pending deposit → Confirmed → Listed as
  product" — of which only the last is news. The completed and future steps
  come back on open, where "why is my deposit still pending" is actually the
  question. The current step is the one the page paints `bg-info`.
- **An Actions cell with no button is removed**, not left as a labelless "—"
  floating on its own line, which is what a finished order produced.

This is the one part that cannot be pure CSS: a `<td>` carries no clue which
column it is in, so the labels are copied off `<thead>`, and the choices above
need to be made per cell. The page re-renders its tables **every eight seconds**
while polling, so a MutationObserver re-applies all of it and the open cards
are remembered outside the DOM — otherwise every card snaps shut mid-read.

**Also fixed, and it was a real bug:** a WebView with no `onJsConfirm`
silently suppresses `window.confirm()` and hands the page `false`. The page
guards cancelling a listing or an order behind exactly that call, so **Cancel
did nothing at all** — the worst possible failure on a screen holding escrowed
coins. `chromeClient` now answers `onJsConfirm`/`onJsAlert` with a native
dialog; verified by tapping Cancel on a live listing and dismissing it.

**Verified on the emulator against a live market**, with a listing, two
cancelled orders and a history entry present: all three tables render as
cards; Details expands to all ten fields and survives the eight-second
re-render; Cancel raises its confirmation.

**Not exercised:** the sell-order trade builder's stacking rules — reaching it
needs a market that has enabled a sell coin, which the throwaway local one
cannot do without a chain node.

**Worth knowing before upstream changes:** everything in this section binds to
names inside a *vendored, pre-built* bundle — `.app-container > header.header`,
`.panel.table-wrap`, `.table`/`thead th`, `.badge.bg-info`, and the header
texts `Actions`/`Amount`/`Price`/`Type`/`Status`/`Lifecycle`. If the skycoin
repo rebuilds that bundle with different markup, none of it errors: the page
just quietly returns to a duplicated header and clipped tables. skychat has no
such exposure because its UI source lives in this repo. The durable fix is to
land these breakpoints upstream; until then a build-time assertion over the
vendored assets would turn a silent regression into a failed build.

---

## 2026-08-05 — SkyDEX screen: native market entry, embedded trading UI

**Why:** SkyDEX is the desktop flow — market public key → connect → the
trading UI — and the only part of it that does not belong in a WebView is the
key. Sixty-six hex characters want the phone's own field, its paste
behaviour, and a list of the markets this phone has already used. Everything
past the handshake is the page the desktop already serves.

**Built** (`ui/dex/`, `api/SkydexApi.kt`):

- Native header: market-key field with validation, a recent-markets dropdown
  (names learned from the handshake, in `AppPreferences`/DataStore), and
  Connect. Once there is a market it collapses to one row — dot, market name,
  shortened key (tap to copy), **Disconnect** — and the page takes the screen.
- Connect is three steps in one action: `PUT …/apps/skydex-client` with the
  argv carrying `--market-pk`, then `status: 1`, then a `POST /api/connect`
  on the app's *own* control API once its listener answers.
- The trading UI in a WebView below, with the page's own header suppressed
  (see below). `Logs` in the bar, scoped to `skydex-client`, like every app
  screen.
- `core/ConfigManager` now pins a third app's flags on every launch, and the
  loopback-address rewrite it shared with SkySOCKS is one helper.

**Hard-won facts:**

- **`SetAppPK` refuses this app.** `PUT …/apps/{app}` with a `pk` field is
  allow-listed to `skysocks-client` and `vpn-client` (`api_apps.go`), and the
  flag is `--srv` regardless. The market key therefore goes through the
  `args` field — the whole argv, rewritten.
- **`--market-pk` connects nothing.** It is the value the page pre-fills its
  connect form with; the engine dials only when something POSTs
  `/api/connect` (`skydex-client/commands/api.go`: *"The client never
  connects automatically"*). Setting the flag and starting the app would have
  left the user typing the key a second time into the page — exactly what
  entering it natively was meant to avoid. The native side POSTs it, and the
  page, which reads `/api/status` on load, comes up already on the market.
- **Two servers answer different questions.** The visor (`:8000`) owns the
  app — argv, running or not. skydex-client (`:8051`) owns the market
  session. The screen polls both: an app that reports a market is by
  definition an app whose UI is up, so one `/api/status` call is also the
  readiness probe.
- **`--addr` defaults to `:8051` — every interface.** Same exposure the proxy
  had, with a worse payload: the trading UI has **no gate at all**. (The
  one-time-code scheme in the app list is `skydex-market`'s operator panel,
  not this.) The profile pins the host to loopback. That closes the Wi-Fi
  side; it cannot close the on-device side, since Android has no per-app
  network namespace and the app exposes no auth flag to pin — noted below.
- **The page drew the same header we did** — brand, connected dot, market
  name, shortened key, Disconnect — two identical bars costing a fifth of a
  phone screen. It is hidden with a stylesheet injected at page-finished, not
  by removing the node: the page is React and would put it straight back. The
  native row is the keeper because it stays reachable whatever the page's own
  layout does.
- Stop-before-configure carries over from SkySOCKS unchanged. The `args`
  field triggers a server-side `RestartApp`, which is a no-op when no proc is
  running (`api_apps.go:577`) — so with the app reliably stopped first, one
  PUT carrying `args` + `status: 1` is correct in every case.
- **Ship-blocker checked, and it is already clear:** `go.mod` has no
  `replace` to a local skycoin checkout. The engine comes from the pinned
  `github.com/skycoin/skycoin v0.28.6-0.20260730141451-1bb474401424` and is
  vendored, so a release build needs nothing resolved here.

**Verified** on the emulator (`sdk_gphone64_arm64`, Android 17 / API 37,
light + dark), against the market `024a37ba…43bdb9` ("Unofficial Skycoin
Market"):

- Typed key → **Connect** → *"Reaching the market over Skywire…"* → the
  trading UI rendered, connected, in ~20 s: Market / My Orders / My Listings
  / History, the wallet-address prompt, and *"No products available right
  now"* (the market had no live listings).
- On-device config after connecting:
  `--addr 127.0.0.1:8051 --market-port 8050 --market-pk 024a37bae6…` — the
  loopback pin held and the key was appended, clobbering nothing.
- **Disconnect** dropped the session and stopped the app
  (`skydex-client: Context canceled, shutting down`), and the panel returned
  with the key still in the field.
- The recents dropdown offered *"Unofficial Skycoin Market · 024a37ba…43bdb9"*
  — the name came from the connect handshake, not the user. Picking it and
  reconnecting took ~25 s on a warm route.
- `Logs` opened the shared viewer titled **skydex-client**.
- Recents and the argv survived an APK reinstall: reopening the screen
  pre-filled the key with no typing.
- The page's own header is gone; the embedded UI now starts at its tab bar.

**Control test** (that the flow, not the market, was being measured): a
desktop visor built here with `skydex-market` autostarted answered its own
`skydex-client` over loopback with
`{"connected":true,"currencies":["BTC","LTC"]}`.

**A first-connection transient, not a fault:** the phone's first target was a
freshly-stood-up desktop market. Its visor logged the accept, but the
client's `get_currencies` came back `read response: EOF` and the following
dmsg dial to `…205ee:8050` timed out. Retried against the same market later
the same day, it connected normally — so the first dial to a market whose
route has never been built can fail once and is worth simply repeating.

**Deferred at the time, done the next day** (see the entry above): the
trading UI's phone layout, and closing the trading UI to other apps on the
device.

---

## 2026-08-05 — The address book moves to the visor, so names reach every surface

**Why:** a nickname lived in the chat page's `localStorage`, which meant the
two places a name matters most could not see it — a notification title, which
skychat composes in Go before any UI is involved, and the phone's native call
screen, which is Kotlin and cannot read a WebView's storage. Both showed 66 hex
characters for someone the user had already named. (The profile package's own
header had flagged this: *"the address book … fixed that one device at a time"*.)

**Built:**

- `pkg/skychat/contacts` — the address book as one small JSON file beside the
  profile, written temp-then-rename so a crash mid-write cannot turn a power cut
  into "all my contacts are gone and the app won't start the feature".
- skychat serves it: `GET/POST /contacts`, plus `POST /contacts/import` for
  migration. Import **fills gaps only** — it can never revert a rename made
  since, which is what makes running it on every page load safe.
- `displayName(pk)` is now the single answer to "what do we call this key",
  used by every notification title (DM, group, file, missed call).
- The page reads and writes the server book, keeping its in-memory map for the
  synchronous render paths, and pushes any surviving `localStorage` names up
  once before dropping them.
- The Android side caches the book (30 s) and resolves it for the ringing
  notification and the full-screen call screen.

**Resolution order, now the same everywhere:** the operator's nickname → the
name the peer publishes about itself → the shortened key. The published name is
never consulted in the notification path (it is a network fetch); the UI writes
one into the book when there is no nickname yet, so by the time it matters it is
already a nickname. A name the user chose is never replaceable by the person it
labels.

**Verified** on the emulator, naming the desktop peer "Alice" in Contact
Settings:

- `skychat-contacts.json` on device: `{"037f16ce…": "Alice"}` — server-side,
  and it now survives a WebView cache clear and is shared by every UI of this
  visor.
- Message notification title: **Alice** (was `037f16ce…ecf2`).
- Ringing notification: **Incoming call / Alice**.
- Full-screen call screen: **Alice**.

**Also:** the Contact Settings copy said "Saved here, on this device", which
stopped being true — corrected rather than left to mislead.

**Not moved:** contact *membership* (`skychat_contacts`) and avatars (`i_<pk>`)
are still per-browser. Only the name is needed outside the page, and avatars are
data-URL blobs that would want a different store.

---

## 2026-08-05 — The phone is a generic sink for the notification hub

**Why:** the hub was decoupled from skychat so anything — an app, the visor,
later the hypervisor — can publish. The phone consumed it but presented every
event as a SkyChat message, which quietly undid that: one `messages` channel,
`CATEGORY_MESSAGE`, a SkyChat title fallback, and a single notification id for
all tagged events. skydex-client already publishes (market alerts, lifecycle),
so this was not hypothetical — its alerts would have landed in "Messages", and
its `skydex-lifecycle` tag could overwrite a chat notification.

**Built:**

- `MessageNotifications` → **`NotificationBridge`**: app-agnostic, and named
  for what it is. Presentation is now driven by the hub's `app` field.
- **Per-app Android channels**, created on first use. An app the phone has
  never heard of is not dropped and needs no code: it gets a channel named
  after itself at default importance, so the user has a real switch for it in
  system settings the day it first appears. The `CHANNELS` table only gives
  known apps a nicer label and a deliberate importance (SkyChat interrupts,
  SkyDEX doesn't, visor events are quieter still) — adding a row tunes an app,
  adding a *notification* needs no row at all.
- **Tags namespaced by app**, so two publishers that both say "lifecycle"
  cannot replace each other's alerts. Untagged events stack.
- **`(*Visor).Notify(title, body, tag)`** — so a visor-side notification is one
  line at the place that already knows the thing happened, with the `visor` app
  name (`NotifyAppVisor`) stamped for sinks to key on. Intentionally unused
  today; it is the seam the next notification is written against.

**What this buys:** a new notification — "peer went offline", say — is now one
`v.Notify("Peer offline", …, peerPK)` at the point of detection. No Android
change, no new endpoint, no channel to register; the phone shows it under a
"Skywire" channel the user can silence on its own.

**Verified** on the emulator: a missed call published by skychat arrived as
`channel=app_skychat category=msg importance=4` — routed from the event's app
field into a channel created at run time, where it previously landed on the
hardcoded `messages` channel. The unknown-app fallback is the same call minus
the table hit and is not separately exercised on device.

**Worth knowing (hub-side, not changed here):** the stream is **live-only, no
replay** — anything published while the phone's SSE connection is down (visor
restart, doze, a network flap) is gone, since with zero subscribers the hub
falls through to the host-OS tier, which on Android is nothing. The bridge
reconnects within ~2 s, which is the whole mitigation. A small ring buffer plus
`?since=` on the stream would close it, and would matter more for a
notification the user is expected to act on than it does for chat.

---

## 2026-08-05 — Calls have audio, a screen, a log, and messages have notifications

**Built:**

- **Real audio on a call.** The visor has no audio device on Android, so it
  borrows the phone's. `pkg/skychat/call.Bridge` presents the host app as an
  ordinary Source/Sink pair; `VoiceAudioEngine` (AudioRecord/AudioTrack) plays
  the device and carries PCM over two long-lived streams on the visor's local
  API. `VoiceCallService` runs them for exactly as long as a call is connected.
- **A call is a screen, not a notification** — in either direction, on every
  tab, the Chat tab included. `ui/call/CallScreen` takes the whole display for
  all three moments of the same event: placing one (Calling… / hang up), being
  rung (answer / decline / ringtone), and being in one (mute, speaker, hang up,
  live timer). With the app backgrounded the system is asked to raise it via a
  full-screen intent.
  - **A call being placed had no state anywhere.** It is in neither the ringing
    list (that is the callee's) nor the active list (that starts at
    "answered"), so the caller saw nothing for the whole ring. The call manager
    now tracks outbound invites in flight and the visor serves them at
    `…/skychat/voice/dialing`. The same registration makes hanging up DURING
    the ring possible: there is no session to close, so cancelling the invite
    is the hang-up — before, a caller could only wait out the dial timeout.
- **Message notifications**, for every kind of message, from the visor's
  existing `/api/notifications/stream` — which turned out to have been built
  for exactly this consumer and never had one.
- **A missed call is a message**, in the conversation it belongs to, with the
  notification that follows from being one.
- **A Calls tab** beside Channels: every call — incoming, outgoing, missed —
  with the time, the duration, and a tap to call back.
- Home: the visor card now shows public key / version / uptime, with
  everything else (transports **by type**, DMSG servers, service health) behind
  **More info**.

**Two defaults that were wrong for a phone, both found by testing:**

1. **skychat only ran while its tab was open** (autostart off, like every other
   app on the phone). A chat app that runs only while you are looking at it
   cannot receive anything — no message notification, no ringing call, no
   missed call recorded. `auto_start` is now pinned on for skychat alone.
2. **`--persist` is off by default**, so nothing was ever stored: every
   conversation was erased on the next core restart, which happens on every
   crash, reconnect and app update. Now pinned on with the DB under the app's
   local dir. The Calls tab depends on it too — the call log is call records
   read back out of message history, which is why it needs no store of its own.

**Traps worth keeping:**

- **The `/api` route group applies `middleware.Timeout(30s)`** — a deadline on
  the whole request, which silently severs any long-lived one. The microphone
  stream broke and reopened every 30 s for a whole call with no error anywhere;
  the fix is to register outside that group, as the notification stream already
  documents. Both audio routes now live at `/api/voice-audio/{pk}/…`.
- **A foreground service typed `microphone` is REFUSED** — SecurityException,
  crashing the app — unless RECORD_AUDIO is already granted. A call can arrive
  before the user has ever been asked, so the service starts as `mediaPlayback`
  (honest: audio out, none in) and promotes itself the moment the grant lands,
  before a single frame is recorded.
- The visor's skynet-first voice dial had to be given a bounded slice of the
  call's budget or dmsg — the carrier that actually works to a phone — was
  never reached. (Landed with the earlier voice work; the audio only made it
  visible.)

**Verified** on the `skywire` emulator with an audio-capable desktop visor
(`-tags voiceaudio`, cgo) as the second party:

- Desktop → phone call, answered on the phone: `MODE_IN_COMMUNICATION` with
  `Recording active: true` in `dumpsys audio`, both streams live, the chat
  page's call panel running with a timer and level meters on both sides.
- Microphone permission is requested exactly when a call connects, and the
  call continues (receive-only) while it is outstanding.
- Full-screen call UI on the Home tab with Decline/Answer; ringing notification
  confirmed posted with `category=call`, `importance=4`, full-screen intent.
- Phone → desktop, placed from the chat page's ⋮ Call while ON the Chat tab:
  full-screen `Calling…` with Hang up, then the connected screen with the timer
  and mute/speaker/hang-up when the desktop answered, then
  `Outgoing call · 03:40 PM · 1m 4s` in the Calls tab beside the earlier
  `Missed call · 03:01 PM`.
- Missed call: `Voice: missed call with 037f16ce…ecf2 logged`, an Android
  notification on `channel=messages` `category=msg`, a `📞 Missed call` row in
  the conversation, and the record in the Calls tab as
  `Missed call · 03:01 PM` with a call-back tap.
- `skychat-history.db` created on device; skychat autostarts with the core.
- `go test ./cmd/apps/skychat/... ./pkg/skychat/call/`, golangci-lint clean on
  every touched package. `pkg/skychat/group` hits the 10-minute test timeout —
  untouched by this work.

**Still open:** *declined* is not distinguishable from *no answer* in the log —
both are a call that rang and stopped, and telling them apart needs a decline
signal shared by the chat page's `/voice/decline` and the phone's visor API.
Speakerphone uses the deprecated `AudioManager.isSpeakerphoneOn`.

---

## 2026-08-05 — `skychat://` links open the phone; voice calls come back

**Built:**

- **`skychat://` deep links.** `MainActivity` now answers `ACTION_VIEW` for the
  `skychat` scheme (both `skychat://<pk>[/<group-id>]` and the opaque
  `skychat:invite:<…>` form) and parks the link in `core/DeepLinks`. The app
  navigates to the Chat tab and hands the address to the page, which opens
  **Add by address** with the field filled and the lookup already run.
  - It stops at the resolve. No contact is added, no chat opened, no group
    joined — a link that arrived from outside gets the user a *look* at who or
    what it points at, and the tap that follows is the consent.
  - Only `skychat` is claimed. `skycoin://` is deliberately left alone: the
    Skycoin wallet app already answers it on the same phones, and a second
    filter would put a disambiguation chooser in front of all of its links.
- **Voice calls work on the phone** — the Call row in a conversation's ⋮ menu,
  the incoming-call banner, answer/decline, and the active-call panel. Two
  separate defects had to be fixed; see below.
- Hub: **Pay-with-Sky tile removed.** At most one "coming soon" tile at a time
  (now SkyMeet alone) — several of them read as an unfinished app, one reads
  as the next thing being built.
- Naming: the menu row is **"Call"**, the banner **"Incoming call"** (was
  "Voice call" / "Incoming voice call").

**Why voice was missing — two independent bugs, both outside the Android app:**

1. **The phone runs no visor RPC port, and skychat could only reach the visor
   through one.** Everything skychat relays to the visor — pairing, group
   chat, and all of `/voice/*` — goes through its pair-RPC client, which dials
   `cli_addr`. The phone profile sets `cli_addr: ""` on purpose: on Android any
   installed app holding INTERNET can connect to another app's loopback
   listener, and the visor RPC has no authentication of its own. So the dial
   always failed, `/voice/incoming` answered 503, and the page hid the Call row
   (it polls that endpoint every 3 s and degrades when it 503s). Everything was
   working exactly as designed and the feature was invisible.
   - Fix: `pkg/visor/local_api.go` — a visor running internal-mode apps
     publishes itself, and skychat's `connectPairRPCLocked` prefers that over
     dialing. The mirror image already existed (an internal app publishes its
     HTTP handler and the visor serves it in-process rather than dialing its
     port); this is the same trade in the other direction.
   - What the registry hands out is a wrapper whose **`Close` is a no-op**. The
     caller treats it as an RPC *client* and closes it on every redial —
     closing the live visor there would shut the process down.
   - Unregistration is compare-and-clear, and the test that covers it needed a
     stub with a real field: `proxyDefaultAPI` is an empty struct, and two
     pointers to distinct **zero-size** allocations may compare equal in Go, so
     with an empty stub "a different visor unregistered" and "this visor
     unregistered" are literally the same test.
2. **A call to a phone timed out without ever trying the carrier that works.**
   `initVoice`'s dialer prefers skynet and falls back to dmsg, but handed the
   skynet attempt the caller's whole 30 s. Route setup to an unreachable peer
   does not fail fast — setup nodes, then a local BFS, then a direct-transport
   dial — and a phone accepts no inbound connections, so it consumed the entire
   budget and dmsg was never reached. Measured live: `local BFS found no path`,
   then `stcpr` dialing the emulator's egress IP, then `context deadline
   exceeded`. The skynet leg now gets its own 10 s slice (`dialSkynetBounded`),
   which is what preferring a carrier has to mean if the fallback is to matter.

**Verified** — Android Studio emulator `skywire` (arm64, 1080×2400), pure-Go
payload lane, plus a desktop visor on the Mac as the second party:

- `adb shell am start -a android.intent.action.VIEW -d "skychat://03565f7a…9ce3"`
  from Home → app switched to Chat → **Add by address** open, field prefilled,
  resolved to "Person · direct message", `Start Chat` waiting, nothing added.
  No keyboard: `openAddressModal({focus:false})` on the link path, or it would
  have risen over the result the dialog exists to show.
- ⋮ in a conversation → **Call** present (it was absent before the seam fix).
- Device log: `[skychat]: Pairing: using the in-process visor API (startup)`.
  Confirmed the config it ran against: `cli_addr ''` with skychat still carrying
  `--pair-enable` — i.e. the exact configuration that was silently dead.
- Desktop → phone call (`skywire cli skychat voice call <phone-pk>`): phone
  logged `voice: INCOMING CALL … RINGING`, the **Incoming call** banner appeared
  with Decline/Answer, Answer connected, and the active-call panel ran with a
  live timer (00:04) and You/Peer level meters. Same call before the dial fix:
  `context deadline exceeded`, no ring.
- Hub screenshot: SkyMeet is the only "coming soon" tile.
- `go test ./pkg/visor/ ./cmd/apps/skychat/...` pass; golangci-lint clean on
  both. `pkg/visor/rpcgrpc` `TestSystemStatsCollect` fails, and fails the same
  way on a stashed tree — pre-existing, unrelated.

**Not done (agreed as its own piece):** the phone still has no audio device for
a call. `pkg/skychat/call` on Android compiles the **PulseAudio** backend
(GOOS=android satisfies the `linux` build tag), which has nothing to talk to, so
capture and playback degrade to silence and the call connects mute in both
directions. Real audio needs a bridge — either the Android app capturing with
AudioRecord/AudioTrack and shipping PCM over new endpoints, or the WebView page
doing it with getUserMedia (already permitted here for voice messages). The
first also allows a call to survive the Chat tab being closed and can back a
proper incoming-call notification.

---

## 2026-08-04 — Chat tab: skychat embedded, gated, and made to fit a phone

**Built:**

- `ui/chat/` — the Chat tab is now skychat's own web UI in a WebView, not a
  placeholder. `ChatViewModel` waits for the visor API, starts the `skychat`
  app (autostart is off for every app on the phone), then polls the app's own
  HTTP surface until it answers; only then is the WebView created, so the
  chat never opens on Chromium's error page. `ChatWebView` holds the Android
  half: the password gate, media permissions, uploads, downloads, and the
  rule for what may leave the page.
  - Android back ↔ page history. The UI already pushes a history entry when a
    conversation opens (`_enterChatPane`) and answers `popstate` by going back
    to the list, so `BackHandler(enabled = canGoBack)` → `goBack()` lands
    exactly on Telegram behaviour: back closes the chat, back again leaves the
    tab.
  - Top bar overflow: **Reload** and **Logs**, the shared viewer scoped to
    `skychat`.
  - Uploads via `onShowFileChooser` → `StartActivityForResult`; the callback
    is answered on cancel too, or the page's file input is dead for good.
  - Downloads: the page's `<a download>` is on renderable media, which WebView
    ignores and *renders in place of the chat*. Same-origin main-frame
    navigations are turned into DownloadManager jobs carrying the gate's
    Authorization header; anything off-origin goes to the browser.
- **skychat now runs password-gated on the phone** (`core/SkychatProfile.kt`,
  `api/SkychatApi.kt`, `SecretStore.skychatPassword`). See below — this is the
  security-relevant part of the step.
- `core/ConfigManager` pins skychat's argv on every launch the way it already
  pinned skysocks-client's: `--addr` host forced to loopback, `--portless`
  stripped, `--password-file` pointed at a record it writes itself.
- **Web UI (`cmd/apps/skychat`), the phone pass.** The one-pane breakpoint
  already existed; what did not survive contact with a 406px viewport:
  - the composer needed 489px and overflowed by 62px, which scrolled the
    header's back button off screen — `.message-input` had `flex:1` with no
    `min-width:0`, so it refused to shrink below its placeholder's intrinsic
    width. Fixed generally, plus a tighter composer at the breakpoint.
  - `<input>` → `<textarea>`: one row, grows to three, then scrolls
    (`growComposer`), scrollbar hidden. Enter sends on a real keyboard,
    inserts a newline on a touch device (`onComposerKey`).
  - the toast reserved `100vw - 360px` for a sidebar that isn't there in one
    pane — every toast was a ~46px column of wrapped words.
  - the image lightbox had no zoom of its own and the app has page zoom off:
    added pinch, double-tap and drag-to-pan (`_lbInit`/`_lbZoomAt`).

**Hard-won facts:**

- **A `wrap_content` WebView breaks every percentage height in the page.**
  Compose's `AndroidView` measures a view with default layout params using an
  AT_MOST spec, and Chromium then treats the layout viewport height as
  *indefinite*: measured live, `innerHeight` was 777 while
  `getComputedStyle(html).height` and `body` were **0px**. Every flex column
  collapsed — the conversation list rendered nothing below the tabs and the
  composer sat under the header instead of at the bottom. `layoutParams =
  MATCH_PARENT` on the WebView is the whole fix.
- **WebView needs `MODIFY_AUDIO_SETTINGS`, not just `RECORD_AUDIO`.** With
  RECORD_AUDIO granted (verified in `dumpsys package`), `getUserMedia` still
  failed with a bare `NotReadableError: Could not start audio source`; logcat
  named it: `cr_media: Requires MODIFY_AUDIO_SETTINGS and RECORD_AUDIO. No
  audio device will be available for recording`. It is an install-time
  permission — no prompt, just the declaration.
- **An unauthenticated loopback port is not private on Android.** There is no
  per-app network namespace: any app holding INTERNET can reach
  `127.0.0.1:8001`, and skychat's surface is the whole account — history,
  contacts, sending. So the phone profile turns on the gate skychat already
  ships: a second device-local secret, hashed the way `commands/auth.go`
  verifies (`<hex salt>:<hex sha256(password‖salt)>`, 16-byte salt) into
  `<local_path>/skychat-password`, written **before** the app is ever started
  so there is no open window. The WebView answers the challenge in
  `onReceivedHttpAuthRequest`; Chromium then reuses the credential for every
  subresource, XHR and the SSE stream (confirmed live — the page loads and
  streams with no second challenge).
  - The record is re-checked, not blindly rewritten: the salt is read back and
    the stored secret re-hashed against it, so a rotated keystore rewrites the
    file instead of the app 401-ing against its own gate. A running skychat
    holding a stale file is self-healed once (stop → start reloads
    `--password-file`) before the failure is reported.
- **`--portless` must never be set on the phone.** The visor's
  `/skychat/proxy/…` mount serves the same handler under a path prefix, but
  every fetch in the page is root-absolute (`/history`, `/sse`, `/message`),
  so the UI cannot run there without rewriting all of them. The port is the
  only way in, so the profile strips the flag.
- Composer sizing: `scrollHeight` is content+padding and the field is
  `border-box`, so the border has to be added back or every row is 2px short
  and the field grows a scrollbar it doesn't need. And sizing a *hidden*
  composer (the form is hidden until a conversation opens) measures 0 and pins
  it shut until the first keystroke — `growComposer` returns early instead.

**Verified** live on DM-B70104 (Android 15, LTE, dark) against a desktop
visor (`02870fd1…4570`), with Chrome DevTools attached over
`adb forward … webview_devtools_remote_<pid>` for the measurements:

- Core down → the tab explains it; Connect → "Waiting for the Skywire core",
  then the list. Config carries
  `skychat --pair-enable --addr 127.0.0.1:8001 --password-file …/local/skychat-password`
  and the record file is `-rw------- 97 bytes`.
- Text both ways; a 96 KB PNG sent (✓✓) and a 196 KB PNG + 3.2 MB MP3
  received, both rendering inline with a working player.
- list → chat → Android back → list, with the chat's unread badge intact.
- `getUserMedia({audio:true})` → OK; `{video:true,audio:true}` → `video=1
  audio=1` (both were `NotReadableError` before the permission fix).
- Composer at rest 42px, no inline height, `msgForm` overflow **0** at a 406px
  viewport (was 62px); toast 383px wide on one 40px row (was ~46px).
- Lightbox double-tap on a received screenshot → `scale 2.5` with the
  transform anchored on the tap; a synthetic two-finger pinch drove it 1 → 3.
- Attach → `com.android.documentsui.picker.PickActivity`; cancel returns with
  the conversation still open.
- Overflow → Logs opens the shared viewer live-tailing `skychat`.

**Left open:**

- **Duplicate delivery, seen once and not reproduced since.** One send arrived
  three times on the desktop and one reply arrived twice on the phone; a
  retest delivered once. The log around it is full of route-group churn
  between the two visors (`Failed to send periodic SACK … transport is not set
  up`, a new route group per message), which is the shape of a retransmit
  after an unacked send. Nothing in the embed sends twice — the page holds one
  EventSource and ignores the server's history replay. Worth chasing on the
  skychat/transport side with a reproduction.
- Leaving the Chat tab (or opening Logs) destroys the WebView, so returning
  reloads the page and loses which conversation was open. The WebView has to
  be Activity-scoped (an application-context one crashes on the composer's
  `<select>` popup), so a persistent host is its own change.
- Native notifications are their own piece of work: `pkg/osnotify` is a
  harmless no-op on Android, so the phone relies on the in-page experience
  until the notification wiring lands.

---

## 2026-08-04 — DMSG-servers card fixed; primary transport is now a choice

**Built:**

- **Home / DMSG servers** — the list was empty on every run. It was read
  from `GET /api/dmsg`, which is the *round-trip tracker*: to report a
  visor it must dial that visor's dmsgctrl port back over dmsg, and for
  the local visor that self-dial never lands on this phone. It now reads
  `dmsg_servers` out of the summary the card already fetches — the visor
  answers that from its own live session list, no network involved. Each
  row shows the session protocol always, plus latency when there is one.
  `VisorApi.dmsg()` and the `DmsgClientSummary` DTO are gone with it.
- **Primary transport** (`core/TransportPreference.kt`,
  `ui/components/TransportPreferenceUi.kt`) — a card on SkySOCKS with a
  sheet to pick which transport type the visor reaches for FIRST; the
  others stay behind it as fallbacks. Visor-wide, not per-app, so SkyVPN
  will show the same value from the same two composables. **dmsg is the
  phone default.**
  - Stored in `AppPreferences`, written into `routing.transport_preference`
    by the phone profile on every launch, and applied live (no restart)
    through `PUT …/router-settings` when the core is up. Changing it while
    the client runs re-dials it, since the setting only steers route setup.
- **Go core** — one order now governs both halves of "which transport":
  - `tptypes.PreferredOrder(...)` / `PreferenceOrder()` (new) sort a
    candidate list by the configured order;
  - the route-setup hook (`getRouteSetupHooks`) and `EnsureDirectTransport`
    both used a hardcoded STCPR → SUDPH → DMSG; they now walk
    `PreferredOrder`. The built-in default reproduces the old sequence
    exactly, so nothing changes for a visor that configures nothing.
  - `RouterSettings` gained `transport_preference` (GET reports the order
    in force; PUT applies + persists; `null` = leave alone, `[]` = revert
    to default, unknown names rejected rather than silently dropped).
  - The SUDPH gate — up to a 20 s wait on the STUN client — is now
    evaluated lazily, only when SUDPH is actually the next type to try.
    dmsg-first would otherwise still have paid it.

**Hard-won facts:**

- **`/api/dmsg` is empty forever on a phone, by design of what it is.**
  Caught live: `GET /api/dmsg → 200, bytes=3` (`[]`) next to a 2375-byte
  summary, with `dtm.establishTracker … client_pk=03704809…(self) error=
  "dmsg error 202 - cannot connect to delegated server"` repeating every
  ~5 min. The summary's `dmsg_servers` had entries the whole time
  (`Measuring DMSG server latencies via self-ping servers=2`).
- `DMSGServerInfo.latency` is a self-ping the visor runs 5 s after dmsg
  comes up and then **hourly**, so a server that joined since the last
  pass has `latency: 0`. On LTE the session set churns constantly (4 → 3
  servers between two screenshots), so most rows have no latency. Rows
  render `tcp · 1182 ms` when measured and `tcp` when not — never a
  fabricated "0 ms".
- **`PUT …/router-settings` applies every field of the struct**, so a
  caller changing one knob must read-modify-write. Sending only
  `transport_preference` would also send `min_hops: 0`, and the router
  reads 0 as *routing disabled*. Verified the round trip leaves
  `min_hops: 1` intact.
- The transport-preference sheet is the first sheet tall enough to reach
  the gesture bar: it needs `navigationBarsPadding()`, and even then the
  trailing Cancel row was clipped — dropped it (selection applies and
  dismisses; scrim/swipe/back cancel).

**Verified** live on DM-B70104 (Android 15, LTE, dark):

- Home now lists the dmsg servers after Connect — 4 rows, one with
  `1182 ms`, the rest with their carrier.
- Config written at launch carries
  `"transport_preference": ["dmsg","stcpr","squicr","sudph","stcp","webrtc","swsr","swtr"]`,
  and the visor logs `Applied configured transport preference order`.
- SkySOCKS Reconnect with dmsg primary, from the log: dial at `08:07:23.6`
  → `Dialing transport to 03b8ec58… via dmsg` at `:27.4` →
  `saved transport … type(dmsg)` at `:29.2` →
  `Found direct transport to destination … (type=dmsg)` at `:29.9` →
  route group up at `:32.6`. **No STCPR/SUDPH attempt, no STUN wait.**
  Screen went Connected with bytes flowing.
- Live switch DMSG → STCPR → DMSG: each logged
  `SetTransportPreference: [...]` and landed in the config file
  immediately, `min_hops` untouched.
- `go test ./pkg/visor/... ./pkg/router/... ./pkg/transport/types/...`
  green; `go build` with the mobile tags green.

**Left open:** the dmsg self-dial that fails with error 202 is the same
one behind both the empty `/api/dmsg` and the missing latencies. Worth a
look on its own; nothing on the phone depends on it now.

---

## 2026-08-04 — SkySOCKS screen: discovery list, connect, expose-port

**Built:**

- `ui/socks/` — the first real app screen, and the shape the other app
  screens copy: server list → configure → start → observe, with a `Logs`
  bar action opening the shared viewer scoped to `skysocks-client`.
  - Server list from service discovery, ~1050 entries, each with flag,
    country · region, short key and version; search over key/country/
    region/version; pull-free refresh.
  - Status card: state, the visor's own detailed status, selected server
    (tap-to-copy), live transferred bytes, Connect/Disconnect/Reconnect.
  - Helper card: *"SOCKS5 for other apps — 127.0.0.1:1080"*, tap-to-copy,
    with the port editable in a bottom sheet.
  - Last-used server persisted (`core/AppPreferences.kt`, plain DataStore
    — separate from the encrypted `SecretStore`), so the screen opens on
    a one-tap Reconnect.
- `api/VisorApi` additions: `services(type)` over `/api/svc-fetch`,
  `app(name)`, `appConnections(name)`, and a CSRF-signed `updateApp` that
  carries only the fields given (`pk` / `args` / `status`).
- `core/ConfigManager` now also pins two skysocks-client flags on every
  launch (see below).

**Hard-won facts:**

- The proxy-server query is `/api/svc-fetch?service=sd&path=/api/services
  ?type=proxy` (path URL-encoded). SD answers a bare `null`, not `[]`, for
  an empty result, and labels the entries `type: "skysocks"` — `proxy` is
  the filter, not the returned value.
- `svc-fetch` needs its own HTTP client. The visor dials the service over
  DMSG with a 15 s budget *per hop*, and the shared client's 15 s call
  timeout aborted at exactly that moment — the first list load failed with
  a bare "timeout" every time. It now uses a 50 s client, and the opening
  load retries 3× (the API answers well before dmsg has a session).
- `--addr` defaults to `:1080` — **every interface**, i.e. a SOCKS5 proxy
  any device on the same Wi-Fi could use. The phone profile forces the
  host to loopback and leaves the port alone (the one knob the screen
  exposes). `SetAppAddress` is skychat-only, so the port is written by
  replacing the whole argv via the `args` field.
- `--reconnect` is now pinned on too. Caught live: a cell-network route
  loss ("Liveness probe failed 2x; route group gone") makes the app *exit*
  without it — `accept: use of closed network connection`, Errored, dead
  proxy until the user notices. With it the client re-dials in place.
- Configure/start must stop the app first. `PUT` with `pk` or `args`
  triggers a server-side `RestartApp` that races the outgoing proc, whose
  blocked `accept` returns "use of closed network connection" and sticks
  the app in Errored; and `status: 1` on an already-running app is a 500
  ("app already started"). The stop is unconditional and its failure
  ignored — the polled snapshot can be a poll behind the visor.
- A read-modify-write of the UI state across a suspension point silently
  drops concurrent updates: the DataStore read of the last-used server
  captured the state *before* it suspended and clobbered the core-state
  collector's write, so the screen claimed the core was stopped while it
  was running. Every write goes through `MutableStateFlow.update`.
- `AppState.args` is a JSON **array** over the API; the space-joined
  string is an on-disk-only rendering (`appConfigOnDisk`).

**Verified (DM-B70104, Android 15, LTE, light + dark):** list loaded 1052
entries; tapping a server connected (~10 s to route, up to ~9 min on a bad
cell); `…/connections` drove the live byte counters; the port sheet moved
the listener 1080 → 1085 → 1080 with the app staying Connected and no
stale listener left behind; Disconnect closed the listener and kept the
server for Reconnect; the saved server + port survived an app reinstall
and a core restart. End-to-end, **from the phone itself** (adb shell, a
different UID — same reachability a third-party app has):
`curl https://api.ipify.org` → `5.208.36.94` (real), through
`--socks5-hostname 127.0.0.1:1080` → `182.8.229.155` / `36.95.212.119`
(ID exit) and `158.247.213.146` (KR exit), matching the flag shown for the
selected server. Same fetch from the Mac over `adb forward` → HTTP 200.

---

## 2026-08-03 — Core service, on-device config, the Connect screen, log viewer

**Built:**

- `core/SkywireCoreService` — `specialUse` foreground service that execs the
  Go payload from `nativeLibraryDir` as a child process: rotating capture of
  its combined output (`files/skywire/skywire-process.log`), crash-restart
  with backoff (1 s → 30 s, reset after a stable minute), graceful stop.
- `core/ConfigManager` — first-run `config gen` run by the payload itself
  (keys generated on-device, `-r` keeps them across regens) + the phone
  profile applied in Kotlin on every start: API pinned `127.0.0.1:8000` with
  auth, `cli_addr` emptied (no RPC), `pty`/`skywire-tcp`/`lan_dmsg_server`
  dropped, `dmsgscp` disabled, absolute app-private paths, apps in-proc with
  autostart off.
- `core/SecretStore` — random API password satisfying the server policy,
  AES-GCM-encrypted via AndroidKeyStore, stored in DataStore.
- `api/VisorApi` — OkHttp client for the local API: `admin` account
  bootstrap, transparent re-login on 401 (visor restarts drop sessions),
  CSRF helper, summary/dmsg/service-health/runtime-logs/app-logs endpoints
  (the app-logs 500 "no new available logs" is treated as an empty page).
- Home: Connect/Disconnect state machine over the service state + live visor
  card (PK tap-to-copy, version, uptime, transports, dmsg latencies, service
  health) and log-viewer entry points. Disconnect stays reachable while the
  core is still coming up or crash-looping.
- `ui/logs/` — one viewer, three sources: core runtime ring (since-cursor
  polling incl. reset detection across visor restarts), per-app feed
  (RFC3339Nano cursor + boundary-line dedupe), and the captured process
  output (UTF-8 byte tail, rotation-aware) — the only source that works when
  the visor won't start. Follow/pause, level chips, search, tap-to-copy,
  share (capped).
- Manifest: `FOREGROUND_SERVICE_SPECIAL_USE` + `POST_NOTIFICATIONS`;
  cleartext allowed for loopback only (`network_security_config.xml`).
- Go core fixes for app-UID Android: `pkg/netutil` enumerates interfaces via
  `anet` (Android 11+ denies the stdlib's netlink dump — "netlinkrib:
  permission denied"), with the device API level passed as
  `SKYWIRE_ANDROID_API_LEVEL` because a CGO-free process cannot detect it;
  `DefaultNetworkInterface` falls back to the first routable interface when
  `ip r` prints nothing; `Visor.Ports()` no longer panics on configs without
  `pty`/`skywire-tcp`/`cli_addr`.

**Hard-won facts:**

- `launcher.bin_path` must never point into the install dir: the launcher
  MkdirAlls it at startup and the native-library path is read-only *and*
  changes on every app update — the visor then aborts ("failed to create
  dir … permission denied"). It now points at an app-private dir and the
  profile re-pins it on every launch.
- Android's `Process.destroy()` sends SIGKILL, not SIGTERM — the graceful
  stop finds the child's pid in `/proc` and delivers a real SIGTERM
  (`Os.kill`), verified by "Shutdown complete. Goodbye!" and exit 0.
- An adversarial review pass over the diff surfaced and fixed: Disconnect
  unreachable during startup/crash-loop, the auth-recovery job cancelling
  itself (it stops the core, which cancels the collector that launched it),
  spawn-failure `Failed` state being overwritten by `Stopped`, the
  runtime-log cursor silently skipping a restarted visor's lines, and the
  log ring polluting itself via per-poll session probes.

**Verified (fresh install each time, DM-B70104, Android 15, slow LTE):**
config generated on-device in ~1 s (offline, `--nofetch`) with every pin
checked field-by-field on the resulting JSON; Connect → API up in 20–120 s;
card live with all service-health rows OK; `kill -9` of the visor →
respawned in ≤ 4 s → reconnected without user action; `am kill` of the app →
visor survived (same pid) behind the foreground service; Disconnect →
SIGTERM → clean module unwind → exit 0 and the notification cleared; the
viewer live-tailed the core ring during connection and the process capture
during startup.

---

## 2026-08-03 — Brand assets: real logo, Skycoin typeface, designed top bar

**Built:**

- The real Skycoin cloud (`skywire-logo.png`, 1600², supplied by the project
  owner) moved to `app/src/main/res/drawable-nodpi/skywire_logo.png` and wired
  everywhere the placeholder mark lived: the bar's center slot (44 dp,
  untinted — the selection pill signals the active state), the splash icon
  (`ic_splash_mark.xml`, 53×40 dp layer-list inside the masked circle), and
  the adaptive launcher icon foreground (white-tinted cloud on the brand-blue
  background layer). The hand-drawn placeholder vector was deleted.
- The Skycoin typeface (Light/Regular/Bold + italics, OTF) moved to
  `app/src/main/res/font/` and applied across the entire Material 3 type scale
  (`ui/theme/Type.kt`). Roles whose Material default weight is Medium (500)
  are pinned to Bold — the family ships no 500 cut, and they would silently
  fall back to Regular and flatten the emphasis hierarchy.
- Designed shared top bar (`ui/components/SkyTopBar.kt`): centered bold title
  in the brand typeface, tonal circular back button, container blending into
  the background; takes an `actions` slot so app screens can add `Logs` etc.
  All pushed routes use it via `AppRouteScaffold`.

**Verified:** rebuilt + reinstalled on DM-B70104; screenshots confirm the
cloud in the splash (light + dark backgrounds), the untinted cloud in the
bar pill, the typeface on every label, and the new top bar on the SkySOCKS
route — light and dark.

---

## 2026-08-03 — Android app skeleton: theme, splash, bottom bar, apps hub

**Built:** the `android/` Gradle project (this module) — Kotlin / Jetpack
Compose / Material 3, single-Activity architecture.

- Gradle 9.6.1 (committed wrapper) · AGP 9.3.1 · Kotlin 2.4.10 · Compose BOM
  2026.06.01; version catalog in `gradle/libs.versions.toml`. Note: AGP ≥9 has
  built-in Kotlin — the standalone `org.jetbrains.kotlin.android` plugin must
  NOT be applied (build fails with a dedicated error if it is).
- `minSdk 26`, `targetSdk 36`, `compileSdk 37`, arm64-v8a only.
  `useLegacyPackaging = true` so the Go payload is extracted at install time —
  required for exec-ing `libskywire-mobile.so` as a child process later.
- Brand theme (`ui/theme/Theme.kt`): primary `#0072FF`, white/black
  backgrounds, near-black dark surfaces, dynamic color off.
- Splash via `androidx.core:core-splashscreen`: Skycoin mark on white (light) /
  black (dark), 250 ms fade into Home.
- Bottom `NavigationBar`, 5 slots: Home · Chat · **Skycoin mark (icon-only,
  enlarged)** · Wallet · Settings. The center slot opens the apps hub.
- Apps hub: adaptive grid of rounded tiles — SkySOCKS, SkyVPN, SkyDEX,
  SkyChat, Wallet, Fleet, plus SkyMeet and Pay-with-Sky greyed as
  "Coming soon" (no click). SkyChat/Wallet tiles route to the same
  destinations as their tabs. SkySOCKS/SkyVPN/SkyDEX/Fleet push full-screen
  routes; back returns to the hub.
- All icons are Material Symbols placeholders behind `Painter` parameters —
  designed logos drop in later with zero layout change. The Skycoin mark
  (`res/drawable/ic_skycoin_mark.xml`) is a geometric placeholder awaiting the
  brand SVG.
- Placeholder `BiometricGate` composable so the navigation structure is final
  before the real app-lock lands.
- Root Makefile bridge targets: `android-apk` (assembleRelease),
  `android-apk-debug` (assembleDebug).

**Verified:** debug APK (44 MB, includes the 63 MB payload compressed) built
and installed on DM-B70104 (arm64, Android 15); splash → themed Home; all five
bar destinations switch; hub tiles push their routes and back returns to the
hub; light and dark checked via screenshots. AVD `skywire`
(android-37.0 google_apis arm64) created for emulator work.

---

## 2026-08-03 — On-device smoke of the Go core (real device)

**Built:** version stamping fix in the root Makefile — the mobile targets now
also stamp the repo-local `pkg/buildinfo` (`MOBILE_APPINFO`), with a
semver-safe fallback version (`v0.0.0-<sha>` when no git tag is reachable),
because `visorconfig.Parse` rejects a non-semver build version at startup.

**Verified on DM-B70104 (arm64-v8a, Android 15), NDK-cgo payload:**

- `config gen` ran on-device: DNS + HTTPS fetch work through bionic (the
  reason the NDK/cgo lane exists).
- Visor connected to dmsg from the phone's network; local API answered on
  `127.0.0.1:8000` through `adb forward`; auth + CSRF enforced.
- skychat, skydex-client, skysocks-client reached **Running in-proc** — one
  process total (~88 MB RSS), every listener loopback-only, no RPC port.
- Real traffic: desktop browsed the web through the phone's SOCKS5 over the
  Skywire mesh (skysocks-client → public proxy server).
- vpn-client start fails on-device with "could not find default network
  gateway" (Linux `ip r` route code) — expected; the Android VPN glue
  (VpnService TUN handoff) is a later part. Failure was contained; the visor
  kept running.
- Sizes recorded: pure-Go 63,242,536 B; NDK-cgo 63,237,504 B (both ≈ 21.2 MB
  gzipped).

---

## 2026-08-03 — `skywire-mobile`: the lite Go core (one binary)

**Built (Go side, commit `ef43096db`):**

- `cmd/skywire-mobile`: one multicall binary — the visor, the CLI `config`
  subtree, and skychat / skysocks-client / vpn-client / skydex-client. Apps
  run in-process via the launcher's internal-app registry; `app <name>` is the
  per-app exec fallback.
- `mobile` build tag strips ~51 MB of embedded desktop assets, each with a
  graceful-absence path: GeoIP DB (30 MB), manager web UI (6.8 MB → "/" serves
  a one-line API-only page), vendored browser wallet (11 MB → /wallet/* 404),
  tpviz legacy UI (2.8 MB), browse handlers.
- API-only surface on mobile: new `hypervisor.dmsg_ingest` config bool gates
  the remote-visor dmsg RPC ingest (off by default; the Fleet feature flips
  it). Desktop behavior unchanged.
- Build/CI: `make build-mobile` / `android-mobile` / `android-mobile-check`
  (80 MB size budget) / `android-mobile-ndk`; `android` CI job in
  `.github/workflows/test.yml`.

**Verified (desktop smoke, macOS host build of the same variant):** all four
apps started in-proc from one binary (three Running; vpn-client blocked only
by macOS route permissions); authenticated API + CSRF; exec fallback spawned a
real child process; Fleet toggle verified off→on; geo lookups degrade to
"??"; RPC port closed; SOCKS5 browsing through a public proxy worked
end-to-end. Binary: 63.2 MB raw / 21.2 MB gz (from 265.6 MB naive five-binary
payload).

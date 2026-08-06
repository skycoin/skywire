# Implementation report — Skywire Android

Running log of what was implemented, when, and how it was verified.
After finishing a part, add a dated entry at the top: what was built, key
decisions/deviations discovered while building it, and the verification that
was actually performed (commands, devices, measured numbers — not intentions).

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

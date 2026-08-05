# Implementation report — Skywire Android

Running log of what was implemented, when, and how it was verified.
After finishing a part, add a dated entry at the top: what was built, key
decisions/deviations discovered while building it, and the verification that
was actually performed (commands, devices, measured numbers — not intentions).

---

## 2026-08-05 — Calls have audio, a screen, a log, and messages have notifications

**Built:**

- **Real audio on a call.** The visor has no audio device on Android, so it
  borrows the phone's. `pkg/skychat/call.Bridge` presents the host app as an
  ordinary Source/Sink pair; `VoiceAudioEngine` (AudioRecord/AudioTrack) plays
  the device and carries PCM over two long-lived streams on the visor's local
  API. `VoiceCallService` runs them for exactly as long as a call is connected.
- **A call is a screen, not a notification.** Ringing or connected and not on
  the Chat tab → `ui/call/CallScreen` takes the whole display (answer, decline,
  mute, speaker, hang up, live timer, ringtone). With the app backgrounded the
  system is asked to raise it via a full-screen intent.
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

# The mobile UI boundary

Status: proposal. Documents which surfaces on Android are native and why, so
that every divergence from the hypervisor UI has a stated reason rather than an
implicit one.

## The rule

> **Everything that can be a web page is the hypervisor UI. Native code exists
> only where an Android API makes a WebView structurally incapable.**

There is no second management GUI, and none is planned. The Compose layer is
not an alternative rendering of the hypervisor UI — it is the app's contract
with the operating system, plus the one interaction the phone form factor
demands (connect, then pocket).

The test for any new screen is a single question: **name the Android API that
makes this impossible in a WebView.** If there isn't one, it belongs in the
hypervisor UI, and if the hypervisor UI renders it badly on a phone, the fix
goes into the Angular sources — shared by desktop, wasm and mobile — not into
Kotlin.

## What is native, and the API that forces it

Each row is a capability with no expression in a web page on Android. The
manifest entries are in `android/app/src/main/AndroidManifest.xml`.

| Surface | Android API | Why a WebView cannot |
|---|---|---|
| VPN consent + tunnel | `VpnService` + `VpnService.Builder` (`SkyVpnService.kt`) | The system owns `/dev/net/tun`; an app never opens it. The TUN arrives as a file descriptor over a unix socket via `SCM_RIGHTS` — see the protocol in [`pkg/vpn/tun_device_android.go`](../pkg/vpn/tun_device_android.go). No fds, no unix sockets, no service binding in a WebView. |
| Visor survives screen-off | `startForeground` + `FOREGROUND_SERVICE_SPECIAL_USE` (`SkywireCoreService.kt`) | WebViews are suspended off-foreground and JS timers are throttled under Doze. A visor that must hold dmsg sessions and accrue uptime needs a foreground service with a persistent notification. This is the single hardest constraint on the platform. |
| Voice/video call session | `FOREGROUND_SERVICE_MICROPHONE\|MEDIA_PLAYBACK` (`VoiceCallService.kt`) | Android 14+ requires a typed foreground service to hold the mic in the background. A page cannot declare one. |
| Notifications | `POST_NOTIFICATIONS`, notification channels (`NotificationBridge.kt`) | System notification surface; no web equivalent that survives the app being backgrounded. |
| Key storage | Android Keystore (`SecretStore.kt`, `ConfigVault.kt`) | Hardware-backed key material. `localStorage` is neither hardware-backed nor durable. |
| App lock | `BiometricPrompt` (`BiometricGate.kt`, `AppLock.kt`) | System biometric dialog; unavailable to page JS. |
| Battery exemption | `REQUEST_IGNORE_BATTERY_OPTIMIZATIONS` (`BatteryOptimization.kt`) | System settings intent. Without it Doze eventually stops the core. |
| Deep links | intent filters `VIEW`/`BROWSABLE` (`DeepLinks.kt`) | Inbound OS routing into the app. |
| Screenshot suppression | `FLAG_SECURE` (`SecureWindow.kt`) | Window flag, not a page property. |
| Connect / status fast path | — (Compose) | The only row here without a hard API justification. See "The one judgement call" below. |

## What is the hypervisor UI

Everything else. Concretely, the entire management surface stays in the shared
Angular sources and is served to the phone from the visor's own HTTP server on
`127.0.0.1:8000`:

- transport management
- routing rule management
- visor info / config / reward address
- port forwarding and reverse proxy
- logging view
- the visor list and remote-visor control (cluster management)
- service discovery / transport discovery / uptime / deployment tabs
- network visualizer
- the WinBox iframe browser and the GUIs reachable through it

This is not aspirational. The mobile build already serves the **complete**
`/api/*` surface — [`hypervisor_handlers_browse_mobile.go`](../pkg/visor/hypervisor_handlers_browse_mobile.go)
states it in a comment: *"The `/api/*` surface is untouched."* The `mobile`
build tag strips exactly one thing that matters here, the ~6.7 MB of static
Angular assets in [`static_mobile.go`](../pkg/visor/static_mobile.go). Shipping
those assets and pointing a WebView at `/` yields the whole UI with no
re-engineering.

The pattern is already in use for the GUIs that had web sources first:
`ChatWebView.kt` (skychat) and `DexWebView.kt` (skydex) are WebViews today, not
Compose reimplementations.

## Progressive disclosure, not reduced capability

Default view is the fast path: connect, VPN, chat, wallet. An **Advanced mode**
toggle reveals the hypervisor UI tab, an in-app terminal running `skywire-cli`
against the same in-process core, and a config text editor.

Nothing is removed and nothing is capped — the full desktop surface is one
toggle away. Hiding by default is a statement about frequency of use, not about
what the platform is permitted to do.

## The one judgement call

The connect/status screen is the only native surface without an API that
forbids a web implementation. The reason is interaction shape rather than
capability: the dominant mobile session is *phone out of pocket → tap connect →
screen off*, and that path has to survive process death and resume instantly.
A WebView cold start plus Angular bootstrap plus API round-trips on a mid-range
handset is the difference between a product and a demo.

The overlap this creates with the hypervisor UI is roughly four screens
— connect/disconnect, server selection, status, settings — against ~47k lines of
Angular. Divergence is expensive when two codebases implement the *same*
feature; these implement disjoint ones. If that overlap ever starts growing,
this document is the place to argue it back down.

## Deliberate divergences unrelated to the UI boundary

Other `*_mobile.go` files exist for reasons of size, battery or threat model
rather than interface. They are listed here so they are not mistaken for GUI
decisions:

| File | Reason |
|---|---|
| [`geoip_embedded_mobile.go`](../pkg/geoip/geoip_embedded_mobile.go) | Drops the ~30 MB embedded GeoLite2-City database. Size. |
| [`hypervisor_handlers_wallet_mobile.go`](../pkg/visor/hypervisor_handlers_wallet_mobile.go) | Drops the ~11 MB vendored skycoin-web bundle; the phone wallet is native app-side code. Size. |
| [`hypervisor_dmsg_ingest_mobile.go`](../pkg/visor/hypervisor_dmsg_ingest_mobile.go) | Remote-visor dmsg ingest is off unless the user opts in via Fleet. Battery. |
| [`survey_whitelist_mobile.go`](../pkg/visor/visorconfig/survey_whitelist_mobile.go) | The deployment survey whitelist is not applied. Those keys let holders pull logs, system survey and pprof over dmsg; an operator's machine is that machine, a handset is not. Threat model. |

Two entries change if the hypervisor UI ships to the phone, because both were
disabled on the grounds that no client could exist:

- [`hypervisor_tpviz_mobile.go`](../pkg/visor/hypervisor_tpviz_mobile.go) —
  `tpvizEnabled()` returns false because the build embeds no UI. With a WebView
  client it should become a runtime setting, gated on Advanced mode so the
  geoip fetch and the two tickers only run when something is watching.
- [`hypervisor_handlers_browse_mobile.go`](../pkg/visor/hypervisor_handlers_browse_mobile.go) —
  the in-UI mesh browser 404s for the same reason and should be restored
  alongside the assets.

## Known gaps

Tracked here rather than hidden, since they are capability gaps and not
interface choices:

- **`.dmsg` / `.skynet` name resolution under the VPN.** Not working on Android.
  [`mesh_client_other.go`](../pkg/vpn/mesh_client_other.go) is a stub and the
  real implementation is Linux-only — it needs an iptables OUTPUT-chain REDIRECT
  and a loopback resolver on `:53`, both requiring root. The Android path is
  cleaner than Linux: `VpnService.Builder` declares the DNS server directly and
  the TUN is already read in userspace, so no netfilter step is needed. Running
  the `.dmsg`/`.skynet` zones, synthetic-IP pool and proxy in-process and handing
  `Builder.addDnsServer()` the in-app resolver gets there without root.
- **Skynet port forwarding / reverse proxy** on the phone.
- **Survey generation** for reward eligibility — including whether it can attest
  single-instance-per-device and non-containerised execution.

## Changing this document

If a row in the native table cannot name an API that forbids a web
implementation, delete the row and move the surface to the hypervisor UI. That
is the whole enforcement mechanism.

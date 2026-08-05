# Skywire Android

The native Android app for Skywire: a Kotlin / Jetpack Compose (Material 3)
shell around the **Skywire mobile core** — a single Go binary
(`libskywire-mobile.so`) bundling the visor, config generation, and the four
client apps (skychat, skysocks-client, vpn-client, skydex-client), which run
in-process inside the visor. The app drives the core through the visor's
authenticated local REST API on `127.0.0.1:8000`.

This directory is a self-contained Gradle project; **Android Studio opens
`android/`**, not the repo root. The Go payload is built by the repo-root
Makefile and lands in `app/src/main/jniLibs/arm64-v8a/` (gitignored — always
rebuilt, never committed).

---

## Prerequisites

| Tool | Notes |
|---|---|
| Go (see repo `go.mod`) | builds the core payload |
| Android SDK | platform + build-tools; Android Studio installs these |
| Android NDK | only for the **release** payload lane (`android-mobile-ndk`) |
| Android Studio (or any JDK 17+) | Gradle needs JDK 17+; Studio's bundled JDK works. The Makefile defaults to it via `ANDROID_JAVA_HOME` |
| A device or AVD | **arm64 only** — the payload has no x86_64 build, never pick an x86_64 emulator image |

`android/local.properties` (gitignored) must point at your SDK:

```
sdk.dir=/Users/<you>/Library/Android/sdk
```

Android Studio writes this automatically on first open.

## Building

Everything runs from the **repo root**:

```sh
# 1. The Go core payload → android/app/src/main/jniLibs/arm64-v8a/libskywire-mobile.so
make android-mobile        # pure-Go lane: CI + emulator work
make android-mobile-ndk    # NDK/cgo lane: RELEASE builds (fixes Android DNS via
                           #   bionic getaddrinfo); needs ANDROID_NDK_HOME set
make android-mobile-check  # pure-Go build + the CI size budget (fails > 80 MB)

# 2. The APK
make android-apk           # release (unsigned until release signing lands)
make android-apk-debug     # debug-signed → installable directly via adb
```

Or with Gradle directly (`JAVA_HOME` must be a JDK 17+):

```sh
cd android && ./gradlew assembleDebug
```

APKs land in `android/app/build/outputs/apk/{debug,release}/`.

Rule of thumb: **whenever the Go side changed, run `make android-mobile`
(or `-ndk`) first**, then rebuild/reinstall the APK — Android Studio's Run ▶
does not rebuild the payload.

## Running

**Android Studio (the normal dev loop):** open `android/`, pick the device or
AVD, Run ▶.

**CLI:**

```sh
make android-apk-debug
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
adb shell am start -n com.skycoin.skywire/.MainActivity
```

**Emulator:** the project AVD is named `skywire` (Pixel-class, arm64,
`google_apis`). If it doesn't exist yet:

```sh
sdkmanager "system-images;android-37.0;google_apis;arm64-v8a"
avdmanager create avd -n skywire -k "system-images;android-37.0;google_apis;arm64-v8a" --device pixel_7
emulator -avd skywire
```

Emulator networking is NATed — the core's outbound dmsg/transport dialing
works out of the box.

## How the app runs the core

Home's **Connect** starts `SkywireCoreService` (a `specialUse` foreground
service) which execs `libskywire-mobile.so` from `nativeLibraryDir` as a
child process — the phone equivalent of `skywire visor -c skywire-config.json`
under a supervisor:

- **First run:** the service runs the payload's own `config gen` (keys made
  on-device, never leave it), then applies the phone profile in Kotlin
  (`core/ConfigManager.kt`): API pinned to `127.0.0.1:8000` with auth, RPC
  listener off, `pty`/`skywire-tcp`/`lan_dmsg_server` removed, all paths
  under `files/skywire/`, all apps in-proc with autostart off. The profile is
  re-applied on every start, so the pins survive config rewrites and app
  updates.
- **Auth:** single `admin` account with a random device-local password
  (AndroidKeyStore-encrypted at rest, `core/SecretStore.kt`); the client
  (`api/VisorApi.kt`) creates the account on first run and re-logins on 401
  (visor restarts invalidate sessions).
- **Lifecycle:** crash → restart with backoff (1 s → 30 s, reset after a
  stable minute); Disconnect → SIGTERM via `Os.kill` (Android's
  `Process.destroy()` is SIGKILL and would skip the visor's graceful
  shutdown), escalating only if it hangs. Swiping the app away does not stop
  the core; Disconnect or Force stop does.
- **Process log:** the child's combined output lands in
  `files/skywire/skywire-process.log` (rotating) — readable in-app via the
  log viewer's Process source, which works even when the visor won't start.

## App screens

Each app screen drives one of the visor's apps over the local API — list,
configure, start, observe — and carries a `Logs` action scoped to that app.

**SkySOCKS** (`ui/socks/`) lists public proxy servers from service discovery
(`/api/svc-fetch?service=sd&path=/api/services?type=proxy`, which the visor
fetches over DMSG on the app's behalf), points `skysocks-client` at the tapped
one (`PUT …/apps/skysocks-client` with `pk`, then `status`), and polls
`…/apps/skysocks-client` + `…/connections` for state and traffic. The chosen
server is remembered across restarts, so the screen opens on a one-tap
**Reconnect**.

Two of the app's flags are owned by the phone profile rather than the screen,
and re-pinned on every launch (`core/ConfigManager.kt`):

- `--addr` host is forced to `127.0.0.1` — the generated `:1080` listens on
  every interface, which on a phone means any device on the same Wi-Fi could
  use the proxy. The port stays editable from the screen.
- `--reconnect` is always on. Phones lose mesh routes routinely (a cell
  handover is enough); without it the app *exits* when its route group dies
  and the proxy is silently dead until the user notices.

Starting or reconfiguring goes through an explicit stop first: the visor's
restart-on-args-change races the outgoing proc, whose blocked `accept` returns
"use of closed network connection" and leaves the app stuck in `Errored`.

**SkyDEX** (`ui/dex/`) is native above, embedded below. The market public key
is a native field with a recent-markets dropdown; connecting writes
`--market-pk` into the app's argv (`PUT …/apps/skydex-client` with `args`,
then `status` — `pk` is allow-listed to skysocks/vpn and would be rejected),
starts the app, and then POSTs `/api/connect` on **skydex-client's own** API
at `127.0.0.1:8051`. That last call is what makes it one step: the flag alone
only pre-fills the page's connect form, because the engine never dials on its
own. The trading UI then loads in a WebView already connected, and
`/api/status` is polled so the native header mirrors the app rather than
guessing.

A stylesheet is injected at page-finished (again at `onPageFinished`, because
the page is React and a removed node comes back on the next render). It drops
the page's own header — brand, connected dot, market name and key, Disconnect
— which duplicated the native row exactly, and adds the phone breakpoint the
page never shipped: its own ~10 kB of CSS has no media query at all, so the
tab strip hid *Settings* off the end of a scroll, banners kept their actions
in a corner, and the grids held desktop column minimums.

Its tables become cards. My Listings is ten columns wide; a phone showed five
and cut off the rest, including **Actions** — the column holding Cancel. A
closed card shows the fields named `Type`/`Amount`/`Price`/`Status` (four at
most) plus the action button, one status badge rather than the whole lifecycle
chain, and everything else behind a *Details* toggle. That part needs script,
not just CSS — a `<td>` carries no clue which column it is in — and a
MutationObserver re-applies it, because the page re-renders its tables every
eight seconds while polling.

`chromeClient` answers `onJsConfirm`/`onJsAlert` with a native dialog. Without
that, a WebView suppresses `window.confirm()` and returns `false`, and the
page guards cancelling a listing or an order behind exactly that call — so
Cancel silently did nothing.

All of the above binds to class names inside a **vendored, pre-built** bundle.
If upstream rebuilds it with different markup nothing errors — the page just
returns to being unusable on a phone. The durable fix is landing these
breakpoints in the skycoin repo; skychat has no such exposure because its UI
source lives here.

`core/SkydexProfile` pins two flags on every launch:

- `--addr` host to `127.0.0.1`, as for the proxy.
- `--password-file`, holding a device-local secret. The trading UI ships with
  no authentication, and on Android a loopback listener is reachable by every
  app holding INTERNET — with the live market session, the registered wallet
  addresses and order placement behind it. The gate is skywire's own
  (`cmd/apps/skydex-client/commands/auth.go`): with a password set the wrapper
  serves basic auth on `--addr` and moves the vendored engine to a loopback
  port drawn fresh at every start. Off by default, so desktop is unchanged.

  It closes the documented port, not the engine's own: that answers ungated to
  anything that scans for it. Closing it fully needs `skydexclient.Run` to
  accept a `net.Listener` upstream, after which the proxy becomes a handler
  wrapper and the second port disappears.

## Testing & debugging

App logs:

```sh
adb logcat --pid=$(adb shell pidof -s com.skycoin.skywire)
```

The visor's own logs, in-app: Home → **View logs** (core ring buffer while
the API is up, the captured process output otherwise).

The core's local API from your desk (once the app runs the core — or with the
payload run manually, below):

```sh
adb forward tcp:8000 tcp:8000     # http://127.0.0.1:8000/api/ping → "PONG!"
adb forward tcp:1080 tcp:1080     # your desktop browser rides the phone's SOCKS5
curl --socks5-hostname 127.0.0.1:1080 https://example.com
```

SkySOCKS end-to-end, entirely on the phone (the device ships `curl`, and the
shell runs as a different UID — so this is the same reachability a third-party
app gets):

```sh
adb shell 'curl -s https://api.ipify.org; echo'                                  # your real IP
adb shell 'curl -s --socks5-hostname 127.0.0.1:1080 https://api.ipify.org; echo' # the exit node's
```

The API is authenticated (session cookie) and CSRF-protected: `GET /api/csrf`
→ send the token as `X-CSRF-Token` on every mutating request. `/api/ping` is
open; everything else needs `POST /api/login` first.

**Payload-only smoke (no app involved)** — run the core straight from a shell,
useful to bisect "app problem vs core problem":

```sh
adb push android/app/src/main/jniLibs/arm64-v8a/libskywire-mobile.so /data/local/tmp/skywire/
adb shell
  cd /data/local/tmp/skywire && chmod +x libskywire-mobile.so
  ./libskywire-mobile.so config gen -i -e -o ./skywire-config.json --binpath /data/local/tmp/skywire
  # apply the phone-profile edits (see the desktop smoke notes in the repo), then:
  ./libskywire-mobile.so visor -c ./skywire-config.json
```

There are no unit tests in the app module yet; UI/integration tests will be
added alongside the feature screens. Go-side tests run from the repo root as
usual (`make test`); the `mobile` build variant is compile-checked in CI by
the `android` job with a size budget.

## Project layout

```
android/
├── settings.gradle.kts / build.gradle.kts / gradle/libs.versions.toml
├── gradlew, gradle/wrapper/            # committed wrapper (Gradle 9.6)
└── app/src/main/
    ├── AndroidManifest.xml
    ├── java/com/skycoin/skywire/
    │   ├── MainActivity.kt             # the one Activity: splash → gate → scaffold
    │   ├── core/                       # foreground service, config, secrets, prefs
    │   ├── api/                        # local-API client + DTOs
    │   └── ui/                         # theme/, navigation/, components/, logs/,
    │                                   # home/ chat/ hub/ socks/ vpn/ dex/
    │                                   # fleet/ wallet/ settings/
    ├── res/                            # brand logo (drawable-nodpi), Skycoin
    │                                   # fonts (font/), splash + adaptive icon
    └── jniLibs/arm64-v8a/              # Go payload (gitignored build artifact)
```

Progress log: [implementation-report.md](implementation-report.md).

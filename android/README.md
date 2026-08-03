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

## Testing & debugging

App logs:

```sh
adb logcat --pid=$(adb shell pidof -s com.skycoin.skywire)
```

The core's local API from your desk (once the app runs the core — or with the
payload run manually, below):

```sh
adb forward tcp:8000 tcp:8000     # http://127.0.0.1:8000/api/ping → "PONG!"
adb forward tcp:1080 tcp:1080     # your desktop browser rides the phone's SOCKS5
curl --socks5-hostname 127.0.0.1:1080 https://example.com
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
    │   └── ui/                         # theme/, navigation/, components/,
    │                                   # home/ chat/ hub/ socks/ vpn/ dex/
    │                                   # fleet/ wallet/ settings/
    ├── res/                            # brand logo (drawable-nodpi), Skycoin
    │                                   # fonts (font/), splash + adaptive icon
    └── jniLibs/arm64-v8a/              # Go payload (gitignored build artifact)
```

Progress log: [implementation-report.md](implementation-report.md).

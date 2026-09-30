# Gate G2 evidence — M2 app shell (author's side)

Collected by the author on 2026-09-30 on the dev Mac (Xcode 27.0 27A266a, iOS
27.0 Simulator "iPhone 18 Pro", Go 1.26.4, Swift 6.4) on
`feat/skywire-mobile-ios` after G1 (`723936ec7`). The gate record
(`ios/gates/G2.md`) is the reviewer's to write.

| File | What it is |
|---|---|
| `coreclient-tests.log` | `swift test` in `ios/Packages/CoreClient`: 63 tests, and the 32 recorded exchanges the client was run against, one line each (the "stub-server test log listing every route") |
| `simulator-tests.log` | the Skywire scheme on the Simulator: CoreBridgeTests (a real core, three start/stop cycles), StringCatalogTests, SecretStoreTests (the app-hosted Keychain tests) |
| `flow-checks.log` | the SkywireTour scheme's FlowChecks: restart from Home, the Fleet switch both ways with the config read in between, the app lock round trip |
| `lock-grace.log` | after the review: the app lock inside and past its grace, from the review's own artifacts, three instrumented runs and the round trip with its new 10 s leg |
| `screens/` | the screen tour (`SkywireUITests/ScreenTour`) in English, Simplified Chinese and Spanish: Home disconnected and connected, Settings, the transport choice, Diagnostics, the core log. PNGs are gitignored: they go on the PR |

## What each item was checked with

- **2.1 CoreClient.** Every M2 route of appendix B against answers recorded
  from a real lite core (`ios/scripts/record-api-fixtures.sh`), replayed by a
  stub server that enforces the server's session rules. Session flows: first
  run creates the account, a later run only logs in, a live session skips
  the login, a rejected password is `authFailed`, a 401 costs one re-login
  and one retry, concurrent callers share one login, every mutation carries
  a CSRF token fresh from `/api/csrf`. Transport: a streamed body arrives as
  written, cancelling its reader closes the connection (the server sees it),
  timeouts and refused connections throw. `URLSession` appears only in
  `LoopbackTransport.swift` (G2's grep).
- **2.2 Profile.** The Android app's own output (pulled from the emulator)
  is a fixpoint of the Swift profile except the two iOS-only edits; a fresh
  `config gen` output gets every pin Kotlin made; argv quoting matches
  vectors printed by Go's `joinArgs`/`splitArgs`. On the Simulator: the
  config on disk carries every pin, and skychat opened its history database
  and password file at quoted paths under "Application Support".
- **2.3 Keychain.** SecretStoreTests in the app-hosted bundle, with the app's
  real access group (`FAKETEAMID.com.skycoin.skywire.shared`); a group the
  app is not entitled to is refused. In the app: a clean install creates the
  account; a relaunch logs in with the stored password.
- **2.4 Home.** Screenshots; restart from Home comes back connected with a
  new session and nothing asked (FlowChecks).
- **2.5 Settings.** Screenshots; Fleet on → `hypervisor.dmsg_ingest: true`,
  off → `false`, each after a core restart (FlowChecks + `jq` on the config).
- **2.6 Logs.** Screenshots of the core source; the parser on the recorded
  runtime and app lines (CoreClient tests).
- **2.7 App lock.** FlowChecks.testAppLockRoundTrip with Face ID enrolled on
  the Simulator and answered by `ios/scripts/faceid-matcher.sh`: turned on
  past a check, unlocked after 10 s away (added after the review), locked
  after 35 s away, unlocked by a match, turned off.
- **2.8 Strings.** StringCatalogTests; the tour in three languages.
- **2.9 Privacy manifest.** `PrivacyInfo.xcprivacy` at the app bundle's
  root; the reasons read from the c-archive's `nm -u` (see the file's
  comment for why `statfs` is not declared).
- **2.10/2.11 Workflows.** actionlint clean; `grep -n
  'exportArchive\|altool\|upload' .github/workflows/ios-release.yml` hits
  only inside the secrets-gated step. The runs themselves need the branch
  pushed: both on the PR (`ios-release.yml` rehearses itself on the PR that
  changes it, because GitHub dispatches only workflows on the default branch).

## Found on the way (details in the playbook, *M2 as built*)

1. An unsigned Simulator app cannot use the Keychain; Simulator builds are
   now signed to run locally (no account needed).
2. `/api/visors/{pk}/summary` failed on iOS: `netutil` shelled out to `sh`
   for the default interface. Fixed without exec (`pkg/netutil/net_ios.go`);
   Android's payload is byte-identical (67,698,984 B).
3. A router-settings PUT that echoes the GET pins every router knob into the
   config; the client sends the four fields it owns.
4. M1's profile dropped `transport.log_store`'s type; iOS paths need the
   visor's argv quoting; `local/` must exist before the core starts.
5. URLSession delivers a stream's head only with its first body bytes (for
   M3's notification stream).
6. `statfs` is linked (dead code through pkg/pty and dmsgscp); cut before
   the first App Store upload.
7. On this Mac a colima (docker) forward holds 127.0.0.1:8001, which the
   Simulator app's skychat needs from M3 on.

## After the review (G2 FAIL, 2026-09-30)

The review failed two criteria; the re-review covers them plus a smoke of
the rest (§4).

- **"Locks again after 30 s away and not before."** The lock did not engage
  early. The review's test stopped at its line 146, which looks for
  `core-state`, a Home-tab element, while the app was on Settings; its line
  145, the one that checks for the lock, passed. The review's own snapshots
  and videos show the unlocked Settings screen at the 10 s return, and three
  instrumented runs here show the lifecycle in order with no lock
  (`lock-grace.log`). No app change. The author's round trip now has the
  10 s leg and checks the screen it left.
  The review's other finding here is real and fixed: a tap or a Face ID
  match that lands between two sheets comes to nothing. The matcher now
  answers for as long as a test step asks, not once per request, and the
  test taps the switch again if it has not moved.
- **The two workflows.** At the reviewed SHA, no hosted image had Xcode 27;
  `ee738ee9e` (already on the branch) falls back to Xcode 26.6, whose
  compiler (Swift 6.3.3) then crashed in IRGen on `DiagnosticsView.swift`:
  the thunk for `choose`, a method passed as a `Binding` setter
  (`$sSSScA_pSgIeAghgg_SSIeAghn_TR`, `@isolated(any) (String, Actor?) ->
  ()` to `(String) -> ()`). It was the app's only method passed that way;
  it is a closure now. Xcode 27 builds either form. The run on the PR is
  the check (no Xcode 26 on the dev Mac).

## Reproduce

```sh
make ios-core
(cd ios/Packages/CoreClient && swift test)
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' -derivedDataPath ios/DerivedData test
# the tour and the flows (the live network; nothing else on 8000/8001)
xcrun simctl status_bar booted override --time 9:41
xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' -derivedDataPath ios/DerivedData \
  -resultBundlePath ios/build/Tour.xcresult test -only-testing:SkywireUITests/ScreenTour
xcrun xcresulttool export attachments --path ios/build/Tour.xcresult --output-path ios/gates/evidence/G2/screens
# the app lock (Face ID enrolled, prompts answered on request)
xcrun simctl spawn booted notifyutil -s com.apple.BiometricKit.enrollmentChanged 1
xcrun simctl spawn booted notifyutil -p com.apple.BiometricKit.enrollmentChanged
ios/scripts/faceid-matcher.sh & xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro' -derivedDataPath ios/DerivedData \
  test -only-testing:SkywireUITests/FlowChecks/testAppLockRoundTrip; kill %1
```

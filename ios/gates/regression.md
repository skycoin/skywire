# iOS regression checklist

Every flow M1 to M7 built, as numbered steps with the result to expect
(playbook item 8.1). It is the release candidate's test: run it top to
bottom on each Simulator, from a fresh device, with only this file and
`ios/README.md`. A step marked *auto* is a test run; *hand* is done and looked
at; the last section lists what only a device can show and the Lane D item
that covers it.

Commands run from the repo root. Three names are used throughout:

```sh
DEVICE="iPhone 18 Pro"                 # then again with "iPhone 17e"
PEER=<the desktop peer's pk>           # printed by desktop-peer.sh (0.3)
PHONE=<the Simulator app's pk>         # read in 2.2
```

## 0. Setup

0.1 *Once per Mac.* Xcode 27 with the iOS 27 runtime, Go as in `go.mod`,
then `make ios-core` (README, Building and running).

0.2 *Ports.* Nothing else may hold the app's ports or the peer's:
`lsof -nP -iTCP -sTCP:LISTEN | grep -E ':(8000|8001|8051|1080|5505|18000|18001|15505|13435) '`
prints nothing.

0.3 *Desktop peer.* In a second terminal:
`ios/scripts/desktop-peer.sh ../peer` builds this checkout's lite core into
`../peer/bin`, writes its config once and runs it; it prints `PEER`.
Expected: `curl -s http://127.0.0.1:18000/api/about` answers with that pk.
Stop it with Ctrl-C; rerun it with the phone's pk in 2.2.

0.4 *A fresh device.*

```sh
xcrun simctl shutdown "$DEVICE"; xcrun simctl erase "$DEVICE"
xcrun simctl bootstatus "$DEVICE" -b
xcrun simctl privacy "$DEVICE" grant microphone com.skycoin.skywire
xcrun simctl spawn "$DEVICE" notifyutil -s com.apple.BiometricKit.enrollmentChanged 1
xcrun simctl spawn "$DEVICE" notifyutil -p com.apple.BiometricKit.enrollmentChanged
xcrun simctl spawn "$DEVICE" notifyutil -g com.apple.BiometricKit.enrollmentChanged   # must print 1
```

A new device from `xcrun simctl create "RC iPhone 18 Pro" "iPhone 18 Pro"`
is as fresh as an erased one, and keeps the old one's data. Only this device
is booted while the checklist runs (`faceid-matcher.sh` answers `booted`).

## 1. Builds and unit tests (auto)

1.1 The README's Simulator test block on `$DEVICE`. Expected: `TEST
SUCCEEDED`; SkywireTests 8 tests, SkywireAppTests 72 with 1 skipped (the
tunnel probe, which runs only on request).

1.2 `(cd ios/Packages/CoreClient && swift test)`: 127 tests, 0 failures.
`(cd ios/Packages/WalletCore && swift test)`: 38 tests, 4 skipped (the live
ones, `SKYWIRE_NET_TESTS=1`), 0 failures.

1.3 `go test ./pkg/vpn/ -run 'IOSTunDevice|TunSettings' -count=1`: ok.

1.4 The packet-tunnel extension's device build (README): `BUILD SUCCEEDED`.

1.5 *Once, not per device:* `make mobile-test` with no Simulator app
running (it binds 8000 and 5505): ok, about 10 minutes.

## 2. First launch, the core, its config (hand) [M1, M2]

2.1 Build, install and launch without arguments (README, "run it", without
`-core.wantsConnected YES`). Open DeviceHub
(`/Applications/Xcode.app/Contents/Applications/DeviceHub.app`), tap Connect,
tap Allow on the notifications prompt. Expected: the prompt shows on this
first tap; Home reads Connected within about a minute; `curl -s
http://127.0.0.1:8000/api/ping` prints `"PONG!"`.

2.2 The config the profile wrote:

```sh
CFG="$(xcrun simctl get_app_container "$DEVICE" com.skycoin.skywire data)/Library/Application Support/skywire/skywire-config.json"
python3 -c 'import json,sys; c=json.load(open(sys.argv[1])); print(c["pk"], c["memory_limit"], repr(c["cli_addr"]), c["dmsg"]["local_relay"], c["dmsgscp"], "skywire-tcp" in c, "pty" in c)' "$CFG"
```

Expected: the pk (`PHONE`), `40MiB`, `''`, `{'enabled': False}`,
`{'disabled': True}`, `False False`. Restart the peer with the phone
granted for Fleet: `ios/scripts/desktop-peer.sh ../peer $PHONE`.

2.3 `xcrun simctl spawn "$DEVICE" log show --last 10m --predicate 'subsystem == "com.skycoin.skywire"' | grep GOMEMLIMIT`
shows `GOMEMLIMIT set to 40MiB`.

2.4 *Background.* `xcrun simctl launch "$DEVICE" com.apple.Preferences`
puts Skywire in the background. About 30 s later, the end of the background
grace the app asks for while connected, iOS marks it suspended:

```sh
xcrun simctl spawn "$DEVICE" log show --last 2m --predicate 'process == "runningboardd" AND eventMessage CONTAINS "Calculated state for app<com.skycoin.skywire"' \
  | grep -o -E 'running-[a-z]+ \(role: [A-Za-z]+\)' | uniq
```

shows `running-active (role: Background)` once the app has left, and ends
with `running-suspended (role: None)`. `xcrun simctl launch "$DEVICE" com.skycoin.skywire` brings it back:
Home reads Connected and the ping answers. The Xcode 27 Simulator does not
always freeze a suspended app's process, so the ping may answer during the
background as well; that is the Simulator, not iOS. On a device the core runs
in the extension (Lane D2).

2.5 *Force-quit.* `xcrun simctl terminate "$DEVICE" com.skycoin.skywire`:
the ping is refused. Launch it again without arguments: it connects by
itself (Connect is remembered) and the ping answers within about a minute.

## 3. The screen tour and the app's own flows (auto) [M2, M6.5]

Run the UI suites in the order below. The tour goes first: its Connect tap
is what the later notification checks rely on, if 2.1 was skipped.

```sh
t() { xcodebuild -project ios/Skywire.xcodeproj -scheme SkywireTour \
  -destination "platform=iOS Simulator,name=$DEVICE" -derivedDataPath ios/DerivedData test "$@"; }
```

A `TEST_RUNNER_*` variable before `t` reaches the tests (xcodebuild passes
them on without the prefix).

3.1 `t -only-testing:SkywireUITests/ScreenTour`: 3 tests pass (en, zh-Hans,
es). Export the pictures (README) and look: every screen is drawn in its
language, nothing clipped.

3.2 FlowChecks, with `ios/scripts/faceid-matcher.sh &` running (stop it
afterwards): `t -only-testing:SkywireUITests/FlowChecks`. 5 tests pass: the
log level reaches the core, a language change keeps the Settings place,
Fleet on and off, the app lock round trip (on behind Face ID, still unlocked
under 30 s away, locked past it).

3.3 *Hand.* Settings > Theme > Dark: Home, the Apps hub, Settings and the
Wallet draw dark, and stay dark after a force-quit and relaunch. Set it back
to the system's.

## 4. SkyChat [M3]

4.1 *Auto.* `t -only-testing:SkywireUITests/ChatChecks` with the peer named:

```sh
TEST_RUNNER_SKYWIRE_PEER_PK=$PEER TEST_RUNNER_SKYWIRE_PEER_CHAT=http://127.0.0.1:18001 t -only-testing:SkywireUITests/ChatChecks
```

6 tests pass: the page comes up, keeps its scale, answers a JavaScript
confirm, keeps the conversation across a tab switch, records a video
message, and the conversation with the peer: link, write, the peer answers,
the banner while away is tapped into the conversation, from Home too.

4.2 *Hand.* Force-quit, relaunch, open the conversation with the peer: the
messages from 4.1 are there.

4.3 *Hand, deep link.* `xcrun simctl openurl "$DEVICE" skychat://$PEER`;
iOS asks whether to open it in Skywire; tap Open. The app opens on Chat with Add by
address filled with the peer's key and an Open Chat button.

## 5. SkySOCKS, SkyDEX, Fleet, remote management [M4]

5.1 *Auto, then hand.* Start `t -only-testing:SkywireUITests/SocksChecks/testConnectsToAProxy`.
While it holds the proxy (40 s from the moment SkySOCKS reads Connected in
DeviceHub), from a second terminal:
`curl -s --socks5-hostname 127.0.0.1:1080 https://api.ipify.org` prints an
address that is not the Mac's (`curl -s https://api.ipify.org`).

5.2 *Auto.* `t -only-testing:SkywireUITests/SocksChecks`: 4 tests pass (the
proxy, the port moving to 1090 and back, a core restart with the proxy
connected that the app lives through, disconnect).

5.3 *Auto.* `t -only-testing:SkywireUITests/DexChecks`. With no market: the
field refuses a malformed key, Connect starts skydex-client and its page
answers, and the dial's failure shows in the engine's words. With a market,
`TEST_RUNNER_SKYWIRE_DEX_MARKET=<its pk>`, the trading page opens and
Disconnect returns to the form. Record which ran.

5.4 *Auto.* `TEST_RUNNER_SKYWIRE_FLEET_PEER=$PEER t -only-testing:SkywireUITests/FleetChecks`:
the peer (started with `$PHONE` in 2.2) is listed on the Fleet screen. The
test needs Fleet on in the app, which 3.2 leaves it.

5.5 *Hand, remote management.* Settings > Remote management > Grant access,
enter `$PEER`, Grant. It applies at the next start: on Home tap Disconnect,
wait for Disconnected, tap Connect. Then from the Mac:

```sh
go build -o ../peer/bin/skywire-cli ./cmd/skywire-cli
../peer/bin/skywire-cli --via dmsg://$PHONE visor pk --rpc localhost:13435
```

prints `$PHONE` (the peer's CLI asked the phone over dmsg). The first calls
in the minute after the start can fail with "connection reset by peer"; try
again after 10 s. The config now lists `$PEER` under `hypervisors` (2.2's
command). Revoke it in Settings.

5.6 *Hand.* After 5.2's restart the config still has the SOCKS pins
(`--addr 127.0.0.1:1080 --reconnect`), and after 3.2 the Fleet switch's
`hypervisor.dmsg_ingest` matches the switch.

## 6. Wallet [M5]

6.1 *Auto.* `t -only-testing:SkywireUITests/WalletChecks -only-testing:SkywireUITests/WalletTour`
with the Face ID matcher running: 3 and 4 tests pass (create, back up,
restore and receive on Skycoin; Bitcoin and Ethereum restore; every coin's
node can be moved; the tour in three languages; remove).

6.2 *Hand.* Create a wallet with a throwaway phrase, write down words 1 to 3,
finish the backup quiz, then:
`xcrun simctl spawn "$DEVICE" log show --last 30m --predicate 'subsystem == "com.skycoin.skywire"' | grep -c -i '<word1> <word2> <word3>'`
prints 0.

## 7. Calls [M6]

Calls between the phone and the peer, driven from the Mac with
`ios/scripts/peer-call.py`. Its `recv` is the level of the phone's audio as
the peer receives it; the Simulator's microphone is the Mac's.

7.1 *Hand, the peer calls.* `ID=$(ios/scripts/peer-call.py dial $PHONE)`
rings the phone (the app in front); tap Answer. `ios/scripts/peer-call.py
levels $ID 300`: `recv` stays above 0 for the 5 minutes. Mute on the phone:
`recv` reads 0; unmute: it comes back.

7.2 *Hand, background.* During the call put the app in the background
(2.4's command) for 60 s: `recv` keeps reading above 0. Bring it back.

7.3 *Hand, hang-ups.* `ios/scripts/peer-call.py hangup $ID`: the phone's
call screen closes. Call again and hang up on the phone this time: `levels`
answers 400, the peer no longer has the call. That the phone lets go of the
microphone is `VoiceAudioReleaseTests` (1.1); the Simulator's screenshots do
not show the microphone dot.

7.4 *Hand, the phone calls.* In the peer's conversation in Chat, tap the
actions button (top right), then Call. The phone shows the peer's name and
Ringing. On the Mac `ios/scripts/peer-call.py incoming` prints the call's id;
`ios/scripts/peer-call.py answer <id>`: the phone's screen shows the running
call with Mute, Hang up and Speaker, and `levels <id>` reads above 0. Hang up
on the phone.

7.5 *Auto.* A call the page placed and the peer answered at once:

```sh
TEST_RUNNER_SKYWIRE_CALL_PEER=http://127.0.0.1:18000 TEST_RUNNER_SKYWIRE_CALL_PEER_PK=$PEER t -only-testing:SkywireUITests/CallChecks
```

passes: the page's own call panel stays usable and its Hang up ends the
call.

## 8. Skymail [2026-10-12]

8.1 *Auto.*

```sh
TEST_RUNNER_SKYWIRE_MAIL_PEER=http://127.0.0.1:18000 TEST_RUNNER_SKYWIRE_MAIL_PEER_PK=$PEER t -only-testing:SkywireUITests/MailChecks
```

3 tests pass: mail to itself (read, reply, delete), the mailbox off and on,
mail both ways with the peer and the banner opening the message.

## 9. Settings and diagnostics [M2, M6.5]

9.1 *Hand.* Settings > Logs & diagnostics lists Core, Process and each
app's log. Export all opens the system's save sheet with a
`skywire-diagnostics-….zip`; cancel it.

9.2 *Hand.* After a force-quit and relaunch, the container's
`Library/Application Support/skywire/app-exits.log` (and `app-exits.txt` in
Export all's zip) starts with a line for the run that ended, `run ended
active` for one killed in front.

9.3 *Hand.* Settings > Export config warns that the file holds the secret
key, asks for Face ID (answer with `xcrun simctl spawn "$DEVICE" notifyutil
-p com.apple.BiometricKit_Sim.pearl.match`), then the save sheet offers
`skywire-config`; cancel it. Switch Encrypt the config at rest on while
connected; on Home tap Disconnect and wait for Disconnected: the config
folder (`dirname "$CFG"`) holds `skywire-config.json.enc` and no
`skywire-config.json`. Connect: it connects, and the plain file is back while
it runs. Force-quit and relaunch: it connects again. Switch encryption off
(Face ID again): the plain file stays and the `.enc` is gone.

9.4 *Hand, last: it changes `PHONE`.* Settings > New identity, confirm: the
key on Home changes and the core connects with it.

## 10. Release rehearsal [M7, item 8.5]

10.1 Build with an imaginary `mobile-v0.1.0` and a run number:

```sh
xcodebuild -project ios/Skywire.xcodeproj -scheme Skywire -destination "platform=iOS Simulator,name=$DEVICE" \
  -derivedDataPath ios/DerivedData MARKETING_VERSION=0.1.0 CURRENT_PROJECT_VERSION=42.1 build
for p in Skywire.app Skywire.app/PlugIns/PacketTunnel.appex; do
  plutil -extract CFBundleShortVersionString raw ios/DerivedData/Build/Products/Debug-iphonesimulator/$p/Info.plist
  plutil -extract CFBundleVersion raw ios/DerivedData/Build/Products/Debug-iphonesimulator/$p/Info.plist
done
```

Expected: `0.1.0` and `42.1`, twice. Rebuild without the two settings
afterwards.

10.2 On the PR, the `iOS release` run's log shows `Info.plist: 0.1.0
(<run>.<attempt>)` and the same for `PlugIns/PacketTunnel.appex/Info.plist`,
then the notice that the App Store Connect step was skipped. No step
uploads.

## 11. Footprint [item 8.2]

11.1 `ios/scripts/footprint-table.py --device "$DEVICE" --call-peer http://127.0.0.1:18000`
(about 40 minutes; the peer must be able to reach the phone, as in 7.1).
It prints one row per state. Expected: every state passes its test; the
connected row's Go's share is at most 33 MB, G1's band plus 10 %
(`ios/gates/G1.md`); whole-app numbers are recorded beside G1's 35 MB idle and
62 MB connected.

## 12. Device only (Lane D)

What the Simulator cannot show, and where it is covered:

| What | Why not here | Lane D |
|---|---|---|
| the core in the packet-tunnel extension, the app reaching it | the Simulator has no VPN service (`TunnelProbeTests`) | D2 |
| receiving chat, mail and calls while the app is suspended | in-app here, so the core sleeps with the app | D2 |
| the VPN icon while connected | no VPN service | D2 |
| the extension's memory under the jetsam limit | no extension | D3 |
| SkyVPN and SkyDNS (both screens say they arrive with the device build) | no tunnel | D4 |
| a call ringing a suspended app | not planned: needs a PushKit relay (proposal §5.3) | — |
| QR scanning with the camera | no camera; the paste path and `QrBridgeTests` cover the bridge | D1 |
| CallKit's system call UI | partial on the Simulator; the in-app screen is the one tested | D2 |
| Face ID itself | the Simulator's match is simulated | D1 |
| signing, Keychain groups with the team prefix | no account | D1 |
| TestFlight and App Review | no account | D5, D6 |

The oldest runtime: this checklist runs on iOS 27.0, the only runtime on the
bench; CI runs the Skywire scheme's tests on iOS 26.5 on every pull request
(Xcode 26.6, `ios-app.yml`). The deployment target, iOS 16.0, is
compile-checked only.

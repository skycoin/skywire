# App Store material

What App Store Connect asks for, written before the Apple account exists
(playbook item 8.3). Nothing here is sent anywhere. The person who submits
copies it into App Store Connect and settles the items marked **Decision**.

| File | In App Store Connect |
|---|---|
| `testflight.md` | TestFlight > Test Information: description, what to test, Beta App Review |
| `app-review.md` | App Review Information > Notes, and the guidelines behind them |
| `export-compliance.md` | the encryption questions at each upload, then Info.plist keys |
| `privacy.md` | the privacy policy URL, App Privacy, the privacy manifest |
| `screenshots.md` | screenshots, made on the Simulator by `ios/scripts/store-screenshots.sh` |

A line marked **device** describes the app on an iPhone, where the core
runs in the packet-tunnel extension (playbook Lane D). The Simulator cannot
show it; it is checked on a device before it is submitted.

## Decisions for the account holder

1. The privacy policy and its URL, including the VPN commitment guideline
   5.4 asks for (`privacy.md`).
2. App Privacy: whether the network's discovery records count as collected
   data (`privacy.md`).
3. Export compliance: the algorithm answer, the US classification and
   report, and France (`export-compliance.md`).
4. SkyDEX in the App Store build, and in which storefronts, under guideline
   3.1.5(iii) (`app-review.md`).
5. User-generated content in SkyChat's public groups, guideline 1.2
   (`app-review.md`).
6. Storefronts that need a VPN licence, mainland China above all
   (`app-review.md`).
7. The licence question below (playbook item 8.4).

## Licence check (playbook 8.4)

Not a code item; a person confirms it and the G8 record names who and when.
Skywire is AGPL-3.0-only, with a commercial licence from Skycoin
(`COMMERCIAL-LICENSE.md`). The FSF holds that the App Store's terms conflict
with the GPL family, so an App Store build relies on Skycoin distributing it
under the commercial terms. That works for code Skycoin owns. Facts for
whoever confirms it, read from the repository on 2026-10-13:

- The repository has no contributor licence agreement, so code from outside
  contributors is theirs, under the AGPL.
- Of the 102 Go modules linked into the iOS core, one is AGPL
  (`github.com/skycoin/skycoin`, Skycoin's own), one is MPL-2.0
  (`github.com/0magnet/yamux`, a fork of HashiCorp's), and the rest are MIT,
  BSD, Apache-2.0 or ISC.
- Five modules in `vendor/` carry no licence file:
  `github.com/0magnet/{bitree,calvin,gobrpc,osnotify,wisp}`. Their author
  needs to state a licence before they ship in a store build.
- The app's Swift code adds bitcoin-core's libsecp256k1 (MIT, vendored),
  the Quicksand and Nunito fonts (OFL) and Google's Material icons
  (Apache-2.0).

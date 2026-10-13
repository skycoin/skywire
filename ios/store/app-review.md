# App Review

The notes for App Store Connect > App Review Information, and the facts
behind them, guideline by guideline. Placeholders are in `<ANGLE_BRACKETS>`.
Lines marked **device** are from the design of the packet-tunnel host (Lane
D) and are checked on an iPhone before submission.

## Notes for App Review

Paste this into the Notes field (it is under App Store Connect's 4,000
characters).

> Skywire is the iOS app of the Skywire network, an open source
> peer-to-peer network (https://github.com/skycoin/skywire). The app runs a
> Skywire node on the phone. Other Skywire users reach the phone through
> that node for chat, voice calls and mail, with no server in between that
> keeps the content.
>
> Why the app installs a VPN configuration: iOS keeps an app's network
> connections alive in the background only inside a Network Extension, so
> the node runs in Skywire's packet tunnel provider. While SkyVPN is off the
> tunnel claims no routes, so no other app's traffic passes through it; it
> only keeps the node reachable. When the user turns SkyVPN on, the tunnel
> routes the phone's traffic through a Skywire VPN server the user chooses.
> That is why the VPN icon shows while Skywire is connected.
>
> No sign-in is needed: the app creates its own local account on the phone.
> Tap Connect on Home; the first start takes 30 to 60 seconds. To try chat,
> calls and mail, open Chat and add this node, which we keep online during
> the review and which answers messages: `<REVIEW_PEER_PK>`.
>
> The wallet is non-custodial. Recovery phrases are created on the phone and
> kept in the iOS Keychain; we never receive them. The app reads balances
> from public blockchain nodes and sends transactions the user signs on the
> phone. It sells nothing and has no in-app purchases.
>
> `<SKYDEX_SENTENCE>` (see "3.1.5(iii)" below).

## Guidelines, facts and open items

**5.4 VPN apps.**
- Uses `NETunnelProviderManager` with a packet-tunnel provider
  (`ios/PacketTunnel`), as the guideline requires.
- Needs an Organization developer account (README, Apple setup).
- *Open, before D6:* "a clear declaration of what user data will be
  collected and how it will be used on an app screen prior to any user
  action to ... use the service." The app has no such screen yet. It belongs
  before the first Connect on a device, which is when iOS asks to add the
  VPN configuration (Lane D2). The Android app has none to port.
- The privacy policy must say the VPN service sells and discloses no user
  data (`privacy.md`).
- *Decision:* storefronts where a VPN app needs a licence, mainland China
  above all. Without a licence, leave those storefronts out of the
  availability list.

**2.5.4 and the always-on tunnel.** The tunnel stays up while routing
nothing, to host the node. Apple may read that as a VPN extension used for a
non-VPN purpose; the notes above explain it. If review refuses it, the
fallback is already built: `InAppCoreHost` on a device, where the node runs
only while the app is open (calls and messages arrive only then).

**3.1.5(i) wallets.** Non-custodial Skycoin, Bitcoin, Ethereum and USDT
(ERC-20) wallet; allowed for an Organization account. No mining (3.1.5(ii)).
The app shows and offers no rewards (3.1.5(v)); the mobile core leaves out
the rewards UI, uptime reporting and the system survey.

**3.1.5(iii) exchanges.** *Decision:* SkyDEX connects to markets run by
third parties, which hold the escrow. The guideline allows exchange
transactions only "on an approved exchange ... in countries or regions where
the app has appropriate licensing." The account holder decides whether
SkyDEX ships in the App Store build, and in which storefronts; the sentence
in the notes follows that decision.

**1.2 user-generated content.** SkyChat has public groups and channels, so
strangers' content can reach a user. The guideline asks for filtering,
reporting with a timely response, blocking, and published contact details.
SkyChat can block a contact; there is no filter and no report path to us.
*Open:* decide what the App Store build offers here before D6.

**5.1.1 permission prompts.** Every purpose string names its use: Face ID
(app lock, wallet send, showing a recovery phrase), microphone (calls, voice
and video messages; "calls" was added in M8), camera (video messages, QR
codes), photo library add (saving a chat download). All three languages are
in `ios/Skywire/InfoPlist.xcstrings`.

**2.1 completeness.** The reviewer needs a node to talk to:
`<REVIEW_PEER_PK>`, a desktop visor kept online during review
(`ios/scripts/desktop-peer.sh` runs one; it needs an auto-reply, which it
does not have yet).

**Age rating.** *Decision:* the questionnaire's messaging and
user-generated-content answers follow the 1.2 decision. The web views show
only pages the app serves on the phone; links from them open outside the
app, so "unrestricted web access" is no.

# TestFlight test information

App Store Connect > TestFlight > Test Information, for `mobile-v0.x` builds.
English first; the zh-Hans and es fields can be filled from the app's own
strings when external groups in those languages exist.

Lines marked **device** describe how the app behaves on an iPhone, where the
core runs in the packet-tunnel extension (playbook Lane D). The Simulator
cannot show them; check each on a device before the first external build.

## Beta App Description

Skywire puts a Skywire node on your iPhone. With it you can chat and call
other Skywire users directly, send Skymail, use a SkySOCKS proxy, trade on
SkyDEX, keep a Skycoin, Bitcoin and Ethereum wallet, and manage your other
Skywire nodes. Chat, calls and mail go between Skywire nodes, end to end
encrypted, with no account to make and no server keeping your messages.

## Feedback email

`<FEEDBACK_EMAIL>` (placeholder: the account holder chooses the address).

## What to Test

Thanks for testing Skywire. Please try:

- Connect on Home. The first start takes 30 to 60 seconds while the node
  finds the network.
- SkyChat: add a contact by its public key or a skychat: link, send text,
  pictures, voice and video messages, and make a voice call.
- Skymail: send mail to another Skywire address and reply.
- **device** Apps > SkySOCKS: connect to a proxy from the list, then point
  an app that supports SOCKS5 at 127.0.0.1:1080.
- Apps > SkyDEX: connect to a market by its key.
- Wallet: create or restore a wallet, back up the recovery phrase, receive.
  Please use small amounts: this is a beta.
- Settings: language, theme, the app lock, logs and diagnostics.

Known limits in this beta:

- **device** While Skywire is connected, iOS shows the VPN icon in the
  status bar. The Skywire node runs inside Skywire's VPN extension, the only
  place iOS lets it keep running in the background. Your other traffic goes
  through it only when you turn SkyVPN on.
- **device** Incoming calls ring only while Skywire is open. A call to a
  phone where iOS has suspended Skywire does not ring (the app says so on
  the call screen).
- Use Settings > Logs & diagnostics > Export all to attach logs to a report.

## Beta App Review information

Contact, phone and email: `<REVIEW_CONTACT>` (the account holder).
Sign-in required: no. The app makes its own local account on the phone.
Notes for the reviewer: paste the text of `app-review.md`, "Notes for App
Review".

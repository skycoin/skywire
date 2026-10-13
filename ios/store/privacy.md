# Privacy

## Privacy policy URL

`<PRIVACY_POLICY_URL>`: a placeholder. App Store Connect needs a public URL
before the first external TestFlight build. **Decision:** the account holder
writes and publishes the policy. Besides the data flows below, guideline 5.4
needs it to say that the VPN service sells, uses for other purposes, and
discloses to third parties no user data.

## Where the app's data goes

What leaves the phone, read from the mobile core's module set
(`pkg/visor/init_modules_mobile.go`), its config and the app. Nothing here
uses an analytics, advertising or crash-reporting SDK, and the app registers
for no remote push.

| Data | Sent to | Kept there |
|---|---|---|
| the node's public key (made on the phone), the dmsg servers it uses | dmsg discovery (Skywire's deployment) | while the node is online, as the entry that lets peers find it |
| the public key with the phone's public IP address and port | the address resolver (Skywire's deployment), for direct transports | while bound |
| pairs of public keys that have a transport between them | transport discovery (Skywire's deployment) | while the transport exists |
| the phone's IP address | dmsg servers and STUN servers (Skywire's deployment), relays of a route | for the connection |
| chat messages, calls, mail | the other user's node, end to end encrypted; dmsg servers relay ciphertext | on the two phones; group posts also on the group's members |
| wallet addresses being looked up, signed transactions, the phone's IP | node.skycoin.com (Skycoin), mempool.space, ethereum-rpc.publicnode.com, eth.blockscout.com (third parties); each is user-replaceable | per each operator's policy |
| traffic the user sends through SkySOCKS or SkyVPN | the proxy or VPN server the user picked (third-party node operators) | per that operator |
| a connection test's request | ip.skycoin.com, only when a test runs | per Skycoin's policy |

Skipped on the phone, by design of the lite core: uptime reporting, the
system survey, the rewards system, metrics, and the local services a desktop
visor publishes.

## App Privacy answers (draft)

App Store Connect > App Privacy. **Decision:** whether the deployment
services' records above count as "collected" in Apple's sense (kept longer
than it takes to serve the request). They are run by Skycoin, the developer.

- If they do not count: *Data Not Collected.* This matches the privacy
  manifest today (`NSPrivacyCollectedDataTypes` is empty).
- If they count, the closest answer is *Identifiers > Device ID* (the node's
  public key, with the IP address the address resolver keeps), purpose *App
  Functionality*, *not linked* to the user's identity (there is no account),
  *not used for tracking*. Then `ios/Skywire/PrivacyInfo.xcprivacy` must list
  the same type, or App Store Connect will report a mismatch.

Tracking: none. `NSPrivacyTracking` is false and there are no tracking
domains.

## Privacy manifest

`ios/Skywire/PrivacyInfo.xcprivacy` declares the required-reason APIs the
app and its Go core call: UserDefaults (CA92.1), file timestamps (C617.1),
system boot time (35F9.1). *Open for Lane D:* the packet-tunnel extension
links the same Go core; check at the first upload whether App Store Connect
wants a manifest inside `PacketTunnel.appex` as well.

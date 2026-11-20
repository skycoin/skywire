# Gate G5 evidence — M5 wallet (author's side)

Collected by the author on 2026-10-02 on the dev Mac (Xcode 27.0 27A266a,
iOS 27.0 Simulator "iPhone 18 Pro", Swift 6.4, Go 1.26.4) on
`feat/skywire-mobile-ios` after G4 (`4f84ee71c`) and the develop merge
(`d71430e6c`). The gate record (`ios/gates/G5.md`) is the reviewer's to
write.

| File | What it is |
|---|---|
| `walletcore-tests.log` | `swift test` in `ios/Packages/WalletCore`: 38 tests, 34 pass, 4 skip (the 3 live tests and the cross-check dump, each behind its variable) |
| `walletcore-live.log` | `SKYWIRE_NET_TESTS=1 swift test --filter Live`, dated: Skycoin node, Ethereum RPC, Blockscout (ETH and USDT) |
| `crosscheck.txt` | `ios/scripts/wallet-crosscheck.sh`: Kotlin and Swift dumps identical (66 lines, 12 signed transactions, 3 seeds), and the dump itself |
| `secp256k1-verify.txt` | `ios/scripts/import-secp256k1.sh --verify`: the vendored libsecp256k1 is bitcoin-core's v0.8.0, file for file |
| `simulator-tests.log` | the app-hosted tests (WalletStoreTests, SecretStoreTests, QrBridgeTests), the string catalogue test, the WalletChecks flows and the WalletTour, and the log scan for seed words |
| `screens/` | WalletTour in English, Simplified Chinese and Spanish (ten screens each) and the WalletChecks steps. PNGs are gitignored: they go on the PR, except the two that show a phrase (`wallet-1-backup`, `wallet-2-quiz`), which stay on the Mac (a throwaway, never funded and since deleted, but a phrase all the same) |

## What each item was checked with

- **5.1 Primitives.** RIPEMD-160 and Keccak-256 are pure Swift: the
  published RIPEMD-160 vectors (and the 55/56/64-byte padding edges, from
  LibreSSL's `openssl dgst -rmd160`), Keccak-256 at 135/136/137/200/272/1000
  bytes around the 1088-bit rate (values from Go's
  `golang.org/x/crypto/sha3.NewLegacyKeccak256`, vendored in this repo).
  secp256k1 is bitcoin-core's library (D-2): every call is checked through
  the Kotlin vectors below, its refusals (zero/out-of-range keys, recovery
  id 4, r or s = 0 or n, x past p, a tweak not below n) come back as nil or a
  thrown error, never libsecp256k1's abort.
- **5.2/5.3 Coins and vectors.** The four offline Kotlin suites, test for
  test (24 tests: Skycoin golden seeds and stored signatures, the five
  `transaction.Create` fixtures from the Go reference implementation byte
  for byte, Trezor BIP 39, the BIP 84 chain, BIP 173/350 address forms, the
  BIP 143 example including its RFC 6979 signature byte for byte, EIP-55,
  the standard ETH derivation, RLP, the EIP-155 worked example byte for
  byte, EIP-1559 sign/recover, ERC-20 call data, amounts). Fixtures:
  `diff -r ios/Packages/WalletCore/Tests/Fixtures
  android/wallet-core/src/test/resources` is empty; the wordlist equals
  `android/wallet-core/src/main/resources/bip39/english.txt`.
- **5.4 Cross-check.** Three BIP 39 reference phrases (two 12-word, one
  24-word) × SKY/BTC/ETH addresses and a signed transaction per chain plus an
  ERC-20 transfer, from fixed made-up inputs: the Kotlin and Swift outputs
  are identical, so derivation and signing agree byte for byte.
- **5.5 Screens.** WalletChecks drives the G5 walk on the Simulator: create
  → backup → three-word quiz → Receive shows WalletCore's first address for
  the words read off the backup screen → reveal shows nothing until Face ID
  passes, then the same twelve words → remove → restore by typing → the same
  address → a send planned against node.skycoin.com refused with "balance is
  not sufficient"; a fresh phrase restored on Bitcoin and Ethereum shows the
  BIP 84 and BIP 44 first addresses, and USDT holds the ETH account without a
  second restore. WalletStoreTests read the stored phrase's Keychain
  attributes: `kSecAttrAccessibleWhenUnlockedThisDeviceOnly`, in the wallet's
  own access group (`<team>.com.skycoin.skywire.wallet`), which SecretStore's
  shared group cannot read.

## Proxies (for Lane D)

- The send's device-owner check and a real broadcast: no funded wallet on
  the bench. Signing is proven by the vectors and the cross-check; planning
  against a live node by its refusal.
- The camera scanner (VisionKit's DataScanner has no Simulator support): the
  photo path is what the Simulator runs.
- The capture cover under a real screen recording, and the screenshot
  warning: the Simulator raises neither `UIScreen.isCaptured` nor
  `userDidTakeScreenshotNotification`.

## Bench notes

- Face ID enrollment on a freshly booted Simulator may not take: check
  `xcrun simctl spawn booted notifyutil -g com.apple.BiometricKit.enrollmentChanged`
  reads 1 before a Face ID test. While it read 0, every prompt was the
  passcode only, which `faceid-matcher.sh` cannot answer.
- The wallet needs no core: a Simulator whose core does not start (here, a
  config from an untagged build) still runs every wallet test.
- Gradle (for the cross-check) needs Android Studio's JBR; the script finds
  it when `JAVA_HOME` is unset.

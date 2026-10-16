// swift-tools-version: 6.2
//
// WalletCore: the Swift port of Android's :wallet-core (android/wallet-core),
// file for file: key derivation, addresses, transaction building and signing
// for Skycoin/fiber, Bitcoin and Ethereum (+ ERC-20), and the node clients.
// The two must derive and sign byte for byte alike; the Kotlin suites'
// fixtures are copied here unchanged (Tests/Fixtures) and replayed.
//
// Plain Foundation + CryptoKit (no UIKit), so `swift test` runs on the Mac.
//
// Third-party crypto is bitcoin-core's libsecp256k1 and nothing else (playbook
// §6 D-2): vendored verbatim in Sources/CSecp256k1 by
// ios/scripts/import-secp256k1.sh, which also proves the copy unedited
// (--verify). SHA-256, SHA-512 and HMAC come from CryptoKit, PBKDF2 from
// CommonCrypto; RIPEMD-160 and Keccak-256 are pure Swift here, with vectors.

import PackageDescription

let package = Package(
    name: "WalletCore",
    platforms: [.iOS(.v16), .macOS(.v13)],
    products: [
        .library(name: "WalletCore", targets: ["WalletCore"]),
    ],
    targets: [
        // The library build upstream's src/CMakeLists.txt does: these three
        // units, the recovery module, the default table sizes (ecmult window
        // 15, the 86 KB signing table), symbols not exported. The defines
        // decide which files the import script copies: keep them in step.
        .target(
            name: "CSecp256k1",
            exclude: ["COPYING", "UPSTREAM.md"],
            sources: [
                "src/secp256k1.c",
                "src/precomputed_ecmult.c",
                "src/precomputed_ecmult_gen.c",
            ],
            publicHeadersPath: "include",
            cSettings: [
                .define("ENABLE_MODULE_RECOVERY", to: "1"),
                .define("ECMULT_WINDOW_SIZE", to: "15"),
                .define("COMB_BLOCKS", to: "43"),
                .define("COMB_TEETH", to: "6"),
                .define("SECP256K1_NO_API_VISIBILITY_ATTRIBUTES"),
                // SwiftPM turns on -Wshorten-64-to-32 for C; upstream's build
                // does not, and its narrowing casts are deliberate (limb and
                // counter arithmetic). The files are not ours to edit.
                .disableWarning("shorten-64-to-32"),
            ]
        ),
        .target(
            name: "WalletCore",
            dependencies: ["CSecp256k1"],
            resources: [.copy("Resources/bip39")]
        ),
        .testTarget(
            name: "WalletCoreTests",
            dependencies: ["WalletCore"],
            // Tests/ rather than Tests/WalletCoreTests/, so the fixtures sit at
            // Tests/Fixtures where the playbook (item 5.3) keeps them, beside
            // nothing but the copies of android/wallet-core/src/test/resources.
            path: "Tests",
            resources: [.copy("Fixtures")]
        ),
    ]
)

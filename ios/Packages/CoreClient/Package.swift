// swift-tools-version: 6.0
//
// CoreClient: how the app talks to the visor, the Swift port of Android's
// api/VisorApi.kt. Everything here is plain Foundation (no UIKit, no Go), so
// `swift test` runs it on the Mac; the app links it like any package.
//
// The tests start a small HTTP server in the test process and replay responses
// recorded from a real core (Tests/Fixtures/routes), so no core has to run.

import PackageDescription

let package = Package(
    name: "CoreClient",
    platforms: [.iOS(.v16), .macOS(.v13)],
    products: [
        .library(name: "CoreClient", targets: ["CoreClient"]),
    ],
    targets: [
        .target(name: "CoreClient"),
        .testTarget(
            name: "CoreClientTests",
            dependencies: ["CoreClient"],
            // Tests/ rather than Tests/CoreClientTests/, so the fixtures sit at
            // Tests/Fixtures where the playbook (item 2.2) keeps them.
            path: "Tests",
            resources: [.copy("Fixtures")]
        ),
    ]
)

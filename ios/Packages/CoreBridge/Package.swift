// swift-tools-version: 6.0
//
// CoreBridge: the Swift face of the Go core's C API. The core itself is
// SkywireCore.xcframework, which `make ios-core` builds from source into
// ios/Frameworks/ (a build output, never committed): run it before opening
// or building the project, and again whenever the Go side changes.
//
// The xcframework is iOS-only (device + Simulator slices), so this package
// builds inside the Xcode project, not with `swift build` on the Mac.

import PackageDescription

let package = Package(
    name: "CoreBridge",
    platforms: [.iOS(.v16)],
    products: [
        .library(name: "CoreBridge", targets: ["CoreBridge"]),
    ],
    targets: [
        .binaryTarget(
            name: "SkywireCore",
            path: "../../Frameworks/SkywireCore.xcframework"
        ),
        .target(
            name: "CoreBridge",
            dependencies: ["SkywireCore"],
            // What the c-archive leaves undefined beyond libSystem (every
            // other symbol it imports resolves there), read from `nm -u` on
            // both slices: libresolv for the Go resolver (res_9_n*), and
            // CoreFoundation + Security for crypto/x509's system root
            // verification (CF*, SecTrust*). Anything linking CoreBridge —
            // the app now, the packet-tunnel extension later — gets them.
            linkerSettings: [
                .linkedLibrary("resolv"),
                .linkedFramework("CoreFoundation"),
                .linkedFramework("Security"),
            ]
        ),
    ]
)

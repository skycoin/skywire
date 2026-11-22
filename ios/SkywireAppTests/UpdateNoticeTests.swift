@testable import Skywire
import XCTest

/// Where the update banner sends the user, and what each store is offering (Android:
/// UpdateNoticeTest). A wrong source sends a TestFlight install to an App Store page that
/// never had the build, or tells an Xcode build about a store it did not come from.
final class UpdateNoticeTests: XCTestCase {
    private let ios17 = OperatingSystemVersion(majorVersion: 17, minorVersion: 4, patchVersion: 0)

    func testTheInstallDecidesWhereUpdatesComeFrom() {
        XCTAssertEqual(UpdateNotice.source(simulator: false, provisioned: false, receipt: "receipt"), .appStore)
        XCTAssertEqual(UpdateNotice.source(simulator: false, provisioned: false, receipt: nil), .appStore)
        XCTAssertEqual(UpdateNotice.source(simulator: false, provisioned: false, receipt: "sandboxReceipt"), .testFlight)
        // Xcode, ad hoc and the Simulator: nothing to update through.
        XCTAssertNil(UpdateNotice.source(simulator: false, provisioned: true, receipt: "sandboxReceipt"))
        XCTAssertNil(UpdateNotice.source(simulator: true, provisioned: false, receipt: nil))
    }

    func testVersionsCompareAsNumbers() throws {
        XCTAssertGreaterThan(try XCTUnwrap(AppVersion("0.0.10")), try XCTUnwrap(AppVersion("0.0.9")))
        XCTAssertEqual(AppVersion("mobile-v1.2.3-rc1")?.description, "1.2.3")
        XCTAssertEqual(AppVersion("v1.2")?.description, "1.2.0")
        XCTAssertNil(AppVersion("latest"))
    }

    /// The App Store's own listing, as the lookup API answers it.
    func testTheAppStoreOffersOnlyANewerBuildThatRunsHere() throws {
        let installed = try XCTUnwrap(AppVersion("0.0.8"))
        let listed = Data(#"{"resultCount":1,"results":[{"version":"0.0.9","trackViewUrl":"https://apps.apple.com/us/app/skywire/id123?uo=4","minimumOsVersion":"16.0"}]}"#.utf8)
        let offer = try XCTUnwrap(UpdateNotice.selectAppStore(listed, installed: installed, os: ios17))
        XCTAssertEqual(offer, UpdateNotice.Offer(version: "0.0.9", source: .appStore, url: URL(string: "https://apps.apple.com/us/app/skywire/id123?uo=4")!, installed: "0.0.8"))
        XCTAssertNil(UpdateNotice.selectAppStore(listed, installed: try XCTUnwrap(AppVersion("0.0.9")), os: ios17))

        let needsNewerIOS = Data(#"{"resultCount":1,"results":[{"version":"0.0.9","trackViewUrl":"https://apps.apple.com/x","minimumOsVersion":"18.0"}]}"#.utf8)
        XCTAssertNil(UpdateNotice.selectAppStore(needsNewerIOS, installed: installed, os: ios17))
        // Not listed in this country, or not at all yet.
        XCTAssertNil(UpdateNotice.selectAppStore(Data(#"{"resultCount":0,"results":[]}"#.utf8), installed: installed, os: ios17))
        XCTAssertNil(UpdateNotice.selectAppStore(Data("not json".utf8), installed: installed, os: ios17))
    }

    /// The mobile tag line only: a desktop release, a draft or a pre-release is not a TestFlight build.
    func testTestFlightTakesTheNewestMobileTag() throws {
        let releases = Data(#"""
        [{"tag_name":"v1.3.95","draft":false,"prerelease":false},
         {"tag_name":"mobile-v0.0.11","draft":false,"prerelease":true},
         {"tag_name":"mobile-v0.0.12","draft":true,"prerelease":false},
         {"tag_name":"mobile-v0.0.10","draft":false,"prerelease":false},
         {"tag_name":"mobile-v0.0.9","draft":false,"prerelease":false}]
        """#.utf8)
        let offer = try XCTUnwrap(UpdateNotice.selectTestFlight(releases, installed: try XCTUnwrap(AppVersion("0.0.9"))))
        XCTAssertEqual(offer, UpdateNotice.Offer(version: "0.0.10", source: .testFlight, url: UpdateNotice.testFlightApp, installed: "0.0.9"))
        XCTAssertNil(UpdateNotice.selectTestFlight(releases, installed: try XCTUnwrap(AppVersion("0.0.10"))))
    }

    @MainActor
    func testAnswerRecordsTheVersion() {
        let defaults = UserDefaults.standard
        let before = defaults.string(forKey: UpdateNotice.seenKey)
        defer { defaults.set(before, forKey: UpdateNotice.seenKey) }
        UpdateNotice.shared.dismiss(UpdateNotice.Offer(version: "9.9.9", source: .testFlight, url: UpdateNotice.testFlightApp, installed: "0.0.1"))
        XCTAssertEqual(defaults.string(forKey: UpdateNotice.seenKey), "9.9.9")
    }
}

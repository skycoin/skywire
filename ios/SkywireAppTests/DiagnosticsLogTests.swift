import Foundation
@testable import Skywire
import XCTest

/// The two records the diagnostics export gained from Android: how the app's runs ended
/// (AppExits) and the chat page's own log (ChatPageLog). On files in a temporary directory.
final class DiagnosticsLogTests: XCTestCase {
    private var dir: URL!

    override func setUpWithError() throws {
        dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString, isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
    }

    override func tearDownWithError() throws {
        try? FileManager.default.removeItem(at: dir)
    }

    private func exits() -> [String] {
        ((try? String(contentsOf: AppExits.logFile(dir), encoding: .utf8)) ?? "").split(separator: "\n").map(String.init)
    }

    /// A first launch has nothing to report; each later one says how the run before it ended.
    func testEachLaunchRecordsHowThePreviousRunEnded() {
        let start = Date(timeIntervalSince1970: 1_800_000_000)
        AppExits.launched(now: start, dir: dir)
        XCTAssertEqual(exits(), [])
        AppExits.entered("active", now: start.addingTimeInterval(1), dir: dir)
        AppExits.entered("background", now: start.addingTimeInterval(60), dir: dir)
        AppExits.launched(now: start.addingTimeInterval(3_600), dir: dir)
        XCTAssertEqual(exits(), ["2027-01-15T08:00:00Z run ended background, its last change at 2027-01-15T08:01:00Z"])
        AppExits.entered("active", now: start.addingTimeInterval(3_601), dir: dir)
        AppExits.launched(now: start.addingTimeInterval(7_200), dir: dir)
        XCTAssertEqual(exits().first, "2027-01-15T09:00:00Z run ended active, its last change at 2027-01-15T09:00:01Z")
        XCTAssertTrue(AppExits.report(dir: dir).contains("run ended background"))
    }

    func testOnlyTheNewestExitsAreKept() {
        for run in 0..<(AppExits.maxExits + 5) {
            AppExits.launched(now: Date(timeIntervalSince1970: TimeInterval(1_800_000_000 + run * 60)), dir: dir)
        }
        XCTAssertEqual(exits().count, AppExits.maxExits)
    }

    func testAnExceptionLeavesItsStack() throws {
        AppExits.launched(dir: dir)
        let exception = NSException(name: .invalidArgumentException, reason: "test reason")
        AppExits.recordCrash(exception, dir: dir)
        let text = try String(contentsOf: AppExits.crashFile(dir), encoding: .utf8)
        XCTAssertTrue(text.contains("uncaught NSInvalidArgumentException: test reason"))
        AppExits.launched(dir: dir)
        XCTAssertTrue(exits().first?.contains("run ended crashed") == true)
    }

    func testThePageLogRollsOver() throws {
        let file = dir.appendingPathComponent("skychat-page.log")
        let line = String(repeating: "x", count: 1_000)
        for _ in 0..<300 {
            ChatPageLog.add(line, to: file)
        }
        ChatPageLog.flush()
        let previous = ChatPageLog.previous(of: file)
        XCTAssertTrue(FileManager.default.fileExists(atPath: previous.path), "a full log did not roll over")
        let size = try XCTUnwrap(FileManager.default.attributesOfItem(atPath: file.path)[.size] as? Int)
        XCTAssertLessThan(size, ChatPageLog.maxBytes)
        let last = try String(contentsOf: file, encoding: .utf8).split(separator: "\n").last
        XCTAssertTrue(last?.hasSuffix(" " + line) == true, "lines carry a timestamp and the text")
    }
}

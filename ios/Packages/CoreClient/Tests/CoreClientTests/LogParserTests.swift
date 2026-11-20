@testable import CoreClient
import XCTest

/// The log formats as the recorded core wrote them.
final class LogParserTests: XCTestCase {
    func testRuntimeEntriesFromTheRecording() throws {
        let delta = Fixture.named("runtime-logs").decode(RuntimeLogsDelta.self)
        let entries = delta.entries.map(LogParser.parseJSONEntry)
        XCTAssertEqual(entries.count, delta.entries.count)
        XCTAssertFalse(entries.contains { $0.level == .unknown }, "every recorded entry has a level")
        let first = try XCTUnwrap(entries.first)
        XCTAssertEqual(first.module, "visor")
        XCTAssertEqual(first.level, .info)
        XCTAssertEqual(first.message, "GOMEMLIMIT set to 40MiB (was none)")
        XCTAssertFalse(first.raw.hasSuffix("\n"))
        // Extra fields ride along in the message, core keys do not.
        let startup = try XCTUnwrap(entries.first { $0.message.hasPrefix("Begin startup.") })
        XCTAssertTrue(startup.message.contains("public_key=\(recordedPK)"))
        XCTAssertFalse(startup.message.contains("log_line"))
    }

    func testAppLinesFromTheRecording() throws {
        let page = Fixture.named("app-logs-skychat").decode(AppLogs.self)
        let entries = page.logs.map(LogParser.parseTextLine)
        XCTAssertFalse(entries.isEmpty)
        XCTAssertTrue(entries.allSatisfy { $0.module.hasPrefix("proc:skychat:") }, "\(entries.map(\.raw))")
        XCTAssertTrue(entries.contains { $0.level == .info && $0.message == "Successfully started skychat." })
        XCTAssertTrue(entries.allSatisfy { !$0.timestamp.isEmpty })
    }

    func testColourAndCallerTokensAreDropped() {
        let line = "\u{1B}[36m[2026-09-30T06:07:11.0882+08:00]\u{1B}[0m DEBUG ClientSession.DialStream [dmsgC]: dialing  \n"
        let entry = LogParser.parseTextLine(line)
        XCTAssertEqual(entry.timestamp, "2026-09-30T06:07:11.0882+08:00")
        XCTAssertEqual(entry.level, .debug)
        XCTAssertEqual(entry.module, "dmsgC")
        XCTAssertEqual(entry.message, "dialing")
        XCTAssertFalse(entry.raw.contains("\u{1B}"))
    }

    /// What the formatter did not write stays whole: a panic trace is often
    /// the line a reader needs.
    func testUnparsedLinesKeepTheirText() {
        let entry = LogParser.parseTextLine("goroutine 1 [running]:")
        XCTAssertEqual(entry.level, .unknown)
        XCTAssertEqual(entry.message, "goroutine 1 [running]:")
        XCTAssertEqual(LogParser.parseJSONEntry("not json").message, "not json")
    }

    func testStructuredFieldsRenderAsJSON() {
        let entry = LogParser.parseJSONEntry(#"{"level":"warning","msg":"m","ok":true,"n":3,"list":[1,"a"]}"#)
        XCTAssertEqual(entry.level, .warn)
        XCTAssertEqual(entry.message, #"m  list=[1,"a"] n=3 ok=true"#)
    }
}

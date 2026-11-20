import CoreClient
import NetworkExtension
@testable import Skywire
import XCTest

/// Playbook item 7.0: does the app's packet tunnel start here? Each step of
/// TunnelCoreHost's start is tried on its own and what iOS answered is
/// written to the log, whatever it was: the record is the result, so the test
/// fails only when a step it needs crashes. Opt in, since a run installs a VPN
/// configuration in the device's settings:
///
///   TEST_RUNNER_SKYWIRE_TUNNEL_PROBE=1 xcodebuild … -only-testing:SkywireAppTests/TunnelProbeTests
final class TunnelProbeTests: XCTestCase {
    func testTheTunnelStarts() async throws {
        guard ProcessInfo.processInfo.environment["SKYWIRE_TUNNEL_PROBE"] != nil else {
            throw XCTSkip("set TEST_RUNNER_SKYWIRE_TUNNEL_PROBE=1 to run the probe")
        }
        var record: [String] = []
        func note(_ line: String) {
            record.append(line)
            print("TUNNEL PROBE: \(line)")
        }
        let bundleID = AppModel.tunnelBundleID
        note("provider \(bundleID); app group \(Bundle.main.object(forInfoDictionaryKey: "SkywireAppGroup") as? String ?? "none")")
        if let group = Bundle.main.object(forInfoDictionaryKey: "SkywireAppGroup") as? String {
            note("App Group container: \(CorePaths.appGroup(group)?.dataDir.path ?? "nil (no container)")")
        }
        do {
            let all = try await NETunnelProviderManager.loadAllFromPreferences()
            note("loadAllFromPreferences: \(all.count) configuration(s)")
        } catch {
            note("loadAllFromPreferences threw: \(Self.describe(error))")
        }
        let manager: NETunnelProviderManager
        do {
            manager = try await TunnelCoreHost.installedManager(bundleID)
            note("saveToPreferences + loadFromPreferences: ok, status \(manager.connection.status.rawValue)")
        } catch {
            note("saveToPreferences threw: \(Self.describe(error))")
            attach(record)
            return
        }
        do {
            try manager.connection.startVPNTunnel()
            note("startVPNTunnel: accepted")
        } catch {
            note("startVPNTunnel threw: \(Self.describe(error))")
        }
        var seen: [Int] = []
        let deadline = Date().addingTimeInterval(30)
        while Date() < deadline {
            let status = manager.connection.status.rawValue
            if seen.last != status {
                seen.append(status)
                note("status \(status)")
            }
            if status == NEVPNStatus.connected.rawValue { break }
            try await Task.sleep(for: .milliseconds(250))
        }
        do {
            try await manager.connection.fetchLastDisconnectError()
            note("lastDisconnectError: none")
        } catch {
            note("lastDisconnectError: \(Self.describe(error))")
        }
        manager.connection.stopVPNTunnel()
        try? await manager.removeFromPreferences()
        note("statuses seen: \(seen) (0 invalid, 1 disconnected, 2 connecting, 3 connected, 4 reasserting, 5 disconnecting)")
        attach(record)
    }

    private func attach(_ record: [String]) {
        let attachment = XCTAttachment(string: record.joined(separator: "\n"))
        attachment.name = "tunnel-probe"
        attachment.lifetime = .keepAlways
        add(attachment)
    }

    private static func describe(_ error: any Error) -> String {
        let ns = error as NSError
        return "\(ns.domain) \(ns.code): \(ns.localizedDescription)"
    }
}

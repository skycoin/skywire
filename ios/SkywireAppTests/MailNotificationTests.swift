import CoreClient
@testable import Skywire
import UserNotifications
import XCTest

/// A mail that was read or deleted takes its own notification down and leaves the rest
/// (Android: NotificationBridge.clearMail). Posts real notifications, so it needs the permission.
final class MailNotificationTests: XCTestCase {
    private let center = UNUserNotificationCenter.current()

    func testOnlyTheReadMailsNotificationIsRemoved() async throws {
        guard await center.notificationSettings().authorizationStatus == .authorized else {
            throw XCTSkip("notifications are not allowed on this device")
        }
        let stamp = UUID().uuidString
        let read = "\(MailProfile.app):read-\(stamp)"
        let kept = ["\(MailProfile.app):kept-\(stamp)", "\(SkychatProfile.app):read-\(stamp)"]
        for id in [read] + kept {
            let content = UNMutableNotificationContent()
            content.title = "MailNotificationTests"
            content.body = id
            try await center.add(UNNotificationRequest(identifier: id, content: content, trigger: nil))
        }
        try await wait("the notifications were never delivered") { Set([read] + kept).isSubset(of: $0) }

        await NotificationBridge.clearMail { $0 == "read-\(stamp)" }
        try await wait("the read mail's notification stayed") { !$0.contains(read) }
        let left = await delivered()
        XCTAssertTrue(Set(kept).isSubset(of: left), "another notification was removed with it")
        center.removeDeliveredNotifications(withIdentifiers: kept)
    }

    private func delivered() async -> Set<String> {
        Set(await center.deliveredNotifications().map(\.request.identifier))
    }

    private func wait(_ failure: String, until done: (Set<String>) -> Bool) async throws {
        for _ in 0..<50 {
            if done(await delivered()) { return }
            try await Task.sleep(for: .milliseconds(200))
        }
        XCTFail(failure)
    }
}

import CoreClient
import os
import UIKit
import UserNotifications

/// The phone as a sink for the visor's notification hub (Android:
/// core/NotificationBridge.kt). The hub is the visor's, not any one app's: an
/// app publishes a notification and stops caring, and the hub hands it to
/// whichever sink can reach the user, here a subscribed host app. Everything
/// known about a notification is what the hub sends: the app, a title, a
/// body, an optional tag.
///
/// Nothing here is per feature. A notification that does not exist yet needs
/// one publish on the Go side and no change in this file; `presentation`
/// only gives the apps we know a label and a volume.
///
/// Runs whenever the core is connected, whichever screen is up. With the core
/// in this process, that is while the app runs (in the foreground or briefly
/// in the background): the proxy for the device, where the core in the
/// packet-tunnel extension keeps receiving while the app is suspended (Lane D).
///
/// Also skychat's unread count: the Chat tab's badge and the app icon's.
@MainActor
final class NotificationBridge: NSObject, ObservableObject {
    static let shared = NotificationBridge()

    /// skychat's unread estimate; nil when unknown (no badge beats a wrong one).
    @Published private(set) var unread: Int?

    /// The hub keeps no backlog, so a dropped stream is reopened promptly:
    /// anything published while it is down is gone.
    static let retryInterval: Duration = .seconds(2)
    static let unreadInterval: Duration = .seconds(10)

    private let center = UNUserNotificationCenter.current()
    /// skychat's own API while the core is connected (the unread count, and
    /// the focus the app clears when it leaves the foreground).
    private var skychat: SkychatClient?
    private let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "notify")

    private override init() {
        super.init()
        // Before the app finishes launching, so a tap that launched it is
        // delivered here.
        center.delegate = self
    }

    /// Asks once whether notifications may be shown, at the first Connect as
    /// Android asks for POST_NOTIFICATIONS there. The core runs either way.
    func requestAuthorization() {
        Task {
            do {
                _ = try await center.requestAuthorization(options: [.alert, .sound, .badge])
            } catch {
                log.error("notification permission request failed: \(error.localizedDescription, privacy: .public)")
            }
        }
    }

    /// Reads the hub and skychat's unread count for as long as the core is
    /// connected (SwiftUI's `.task(id: app.connected)` ends it).
    func run(_ app: AppModel) async {
        guard app.connected else {
            setUnread(nil)
            return
        }
        async let hub: Void = readHub(app)
        async let badge: Void = pollUnread(app)
        _ = await (hub, badge)
    }

    private func readHub(_ app: AppModel) async {
        while !Task.isCancelled {
            do {
                let events = try await app.client.notifications()
                logOpened()
                for try await event in events {
                    post(event)
                }
                log.notice("notification stream ended")
            } catch is CancellationError {
                return
            } catch {
                if app.handle(error) { return }
                log.notice("notification stream failed: \(error.localizedDescription, privacy: .public)")
            }
            do {
                try await Task.sleep(for: Self.retryInterval)
            } catch {
                return
            }
        }
    }

    /// The permission alongside "open", for reading a run's log back. Its
    /// own task: the settings call can wait until the system's permission
    /// prompt has been shown (minutes on a fresh Simulator, G3), and the
    /// stream must be read from the moment it opens.
    private func logOpened() {
        Task { [center, log] in
            let allowed = await center.notificationSettings().authorizationStatus
            log.notice("notification stream open (permission: \(allowed.rawValue, privacy: .public))")
        }
    }

    /// The app left the foreground: nothing is on screen any more, which the
    /// chat page, frozen with the app, cannot tell skychat itself. Runs in the
    /// app's background grace (AppModel.enteredBackground).
    func appLeft() {
        guard let skychat else { return }
        Task { [log] in
            await skychat.clearFocus()
            log.notice("told skychat nothing is on screen")
        }
    }

    private func pollUnread(_ app: AppModel) async {
        defer { skychat = nil }
        while !Task.isCancelled {
            if skychat == nil, let state = try? await app.client.app(SkychatProfile.app) {
                let origin = SkychatProfile.origin(port: SkychatProfile.listenPort(state.args))
                // The secret read per request, not kept: a rotated one
                // (ChatModel rewrites the gate's file) must not leave this
                // asking with the old.
                skychat = SkychatClient(transport: LoopbackTransport(origin: origin)) {
                    try SecretStore.app().password(.skychatPassword)
                }
            }
            if let skychat {
                setUnread(await skychat.unread())
            }
            do {
                try await Task.sleep(for: Self.unreadInterval)
            } catch {
                return
            }
        }
    }

    private func setUnread(_ count: Int?) {
        guard count != unread else { return }
        unread = count
        center.setBadgeCount(count ?? 0, withCompletionHandler: nil)
    }

    private func post(_ event: NotifyEvent) {
        let style = Self.presentation(of: event.app)
        let content = UNMutableNotificationContent()
        content.title = event.title.isEmpty ? style.label : event.title
        content.body = event.body
        content.sound = style.sound ? .default : nil
        content.interruptionLevel = style.level
        content.userInfo = ["app": event.app, "tag": event.tag]
        // The publisher's tag is its own "this again" (a conversation, a
        // market). The same identifier replaces the notification before it,
        // as Android's tag does, and it groups the thread in the list;
        // namespacing by app keeps two apps' tags apart. Untagged ones stack.
        let key = event.tag.isEmpty ? nil : "\(event.app):\(event.tag)"
        content.threadIdentifier = key ?? event.app
        let request = UNNotificationRequest(identifier: key ?? UUID().uuidString, content: content, trigger: nil)
        center.add(request) { [log] error in
            if let error {
                log.error("cannot post a notification from \(event.app, privacy: .public): \(error.localizedDescription, privacy: .public)")
            } else {
                log.info("posted a notification from \(event.app, privacy: .public)\(key == nil ? "" : " (tagged)", privacy: .public)")
            }
        }
    }

    /// How one app's notifications are presented. An unknown app is never
    /// dropped: it is named after itself, at the default volume.
    static func presentation(of app: String) -> (label: String, sound: Bool, level: UNNotificationInterruptionLevel) {
        switch app {
        // A message from a person interrupts: that is what a chat is.
        case SkychatProfile.app: (L10n.text("app_skychat"), true, .active)
        case SkydexProfile.app: ("SkyDEX", true, .active)
        // The visor's own events (transports, reachability, updates):
        // informational, and quieter than a message.
        case "visor", "":
            (L10n.text("app_name"), false, .passive)
        default: (app, true, .active)
        }
    }
}

extension NotificationBridge: UNUserNotificationCenterDelegate {
    /// In the foreground too: the chat page shows no notifications of its own
    /// (WebKit gives it no Notification API), and skychat already holds back
    /// the ones for the conversation on screen.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification,
        withCompletionHandler completionHandler: @escaping (UNNotificationPresentationOptions) -> Void
    ) {
        completionHandler([.banner, .list, .sound])
    }

    /// A tapped skychat notification opens its conversation.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        didReceive response: UNNotificationResponse,
        withCompletionHandler completionHandler: @escaping () -> Void
    ) {
        let info = response.notification.request.content.userInfo
        let app = info["app"] as? String ?? ""
        let tag = info["tag"] as? String ?? ""
        Task { @MainActor in
            if app == SkychatProfile.app {
                ChatRouter.shared.openConversation(tag: tag)
            }
        }
        completionHandler()
    }
}

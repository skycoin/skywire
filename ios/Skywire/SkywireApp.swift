import SwiftUI

@main
struct SkywireApp: App {
    @StateObject private var app = AppModel.inApp()
    @StateObject private var lock = AppLock()
    @StateObject private var router = ChatRouter.shared
    @StateObject private var notifications = NotificationBridge.shared

    init() {
        // Made now rather than with the first view: the notification centre's
        // delegate has to be set before the app finishes launching for a tap
        // that launched it to arrive.
        _ = NotificationBridge.shared
    }

    var body: some Scene {
        WindowGroup {
            RootView(settings: app.settings)
                .environmentObject(app)
                .environmentObject(lock)
                .environmentObject(router)
                .environmentObject(notifications)
                // skychat: links (Info.plist CFBundleURLTypes).
                .onOpenURL { url in router.offer(url) }
        }
    }
}

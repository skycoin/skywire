import SwiftUI

@main
struct SkywireApp: App {
    @StateObject private var app: AppModel
    @StateObject private var lock = AppLock()
    @StateObject private var router = ChatRouter.shared
    @StateObject private var notifications = NotificationBridge.shared
    @StateObject private var callModel: CallModel

    init() {
        // Made now rather than with the first view: the notification centre's
        // delegate has to be set before the app finishes launching for a tap
        // that launched it to arrive.
        _ = NotificationBridge.shared
        // The call model needs the client, which only exists once the app
        // model does.
        let app = AppModel.inApp()
        _app = StateObject(wrappedValue: app)
        _callModel = StateObject(wrappedValue: CallModel(client: app.client))
    }

    var body: some Scene {
        WindowGroup {
            RootView(settings: app.settings)
                .environmentObject(app)
                .environmentObject(lock)
                .environmentObject(router)
                .environmentObject(notifications)
                .environmentObject(callModel)
                // skychat: links (Info.plist CFBundleURLTypes).
                .onOpenURL { url in router.offer(url) }
        }
    }
}

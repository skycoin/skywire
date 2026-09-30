import SwiftUI

@main
struct SkywireApp: App {
    @StateObject private var app = AppModel.inApp()
    @StateObject private var lock = AppLock()

    var body: some Scene {
        WindowGroup {
            RootView(settings: app.settings)
                .environmentObject(app)
                .environmentObject(lock)
        }
    }
}

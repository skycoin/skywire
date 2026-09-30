import SwiftUI

/// The tabs, under the app lock. Chat, the apps hub and Wallet join Home and
/// Settings in later milestones (Android's bar: Home, Chat, hub, Wallet,
/// Settings).
struct RootView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock
    @ObservedObject private var settings: AppSettings
    @Environment(\.scenePhase) private var scenePhase

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        TabView {
            HomeView()
                .tabItem { Label("tab_home", systemImage: "house") }
            SettingsView()
                .tabItem { Label("tab_settings", systemImage: "gearshape") }
        }
        .tint(.skywire)
        .modifier(LockCover(lock: lock, enabled: settings.appLockEnabled))
        .onAppear { app.launch() }
        .onChange(of: scenePhase) { phase in
            switch phase {
            case .background:
                lock.didEnterBackground()
            case .active:
                app.becameActive()
            default:
                break
            }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIApplication.willEnterForegroundNotification)) { _ in
            lock.willEnterForeground()
        }
    }
}

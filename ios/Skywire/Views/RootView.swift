import SwiftUI

/// The tabs, under the app lock: Android's bar (Home, Chat, the apps hub,
/// Wallet, Settings), Wallet joining in M5.
struct RootView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock
    @EnvironmentObject private var router: ChatRouter
    @EnvironmentObject private var notifications: NotificationBridge
    @ObservedObject private var settings: AppSettings
    @Environment(\.scenePhase) private var scenePhase
    @State private var tab = AppTab.home

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        TabView(selection: $tab) {
            HomeView()
                .tabItem { Label("tab_home", systemImage: "house") }
                .tag(AppTab.home)
            ChatView()
                .tabItem { Label("tab_chat", systemImage: "bubble.left.and.bubble.right") }
                .badge(notifications.unread ?? 0)
                .tag(AppTab.chat)
            HubView(openChat: { tab = .chat })
                .tabItem { Label("tab_hub_description", systemImage: "square.grid.2x2") }
                .tag(AppTab.apps)
            SettingsView()
                .tabItem { Label("tab_settings", systemImage: "gearshape") }
                .tag(AppTab.settings)
        }
        .tint(.skywire)
        .modifier(LockCover(lock: lock, enabled: settings.appLockEnabled))
        .onAppear {
            app.launch()
            // A link or a tap that launched the app.
            if router.pending != nil { tab = .chat }
        }
        // Whichever screen is up: the hub is read while the core is connected.
        .task(id: app.connected) { await notifications.run(app) }
        .onChange(of: router.pending) { request in
            if request != nil { tab = .chat }
        }
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

/// The tab bar's tabs, in order.
enum AppTab: Hashable {
    case home, chat, apps, settings
}

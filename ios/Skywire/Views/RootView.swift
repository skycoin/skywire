import SwiftUI

/// The tabs, under the app lock: Android's bar (Home, Chat, the apps hub,
/// Wallet, Settings).
struct RootView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock
    @EnvironmentObject private var router: ChatRouter
    @EnvironmentObject private var notifications: NotificationBridge
    @ObservedObject private var settings: AppSettings
    @ObservedObject private var calls = VoiceCalls.shared
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
            WalletTab()
                .tabItem { Label("tab_wallet", systemImage: "wallet.pass") }
                .tag(AppTab.wallet)
            SettingsView()
                .tabItem { Label("tab_settings", systemImage: "gearshape") }
                .tag(AppTab.settings)
        }
        .tint(.skywire)
        // The call, full screen, over every tab: a call is what the phone
        // is doing, not something to notice inside a conversation list.
        // The state behind it is the visor's own list, so the screen cannot
        // disagree with it, and its dismissal is the buttons' business
        // (Decline, Hang up) — never a swipe or a tap outside.
        .fullScreenCover(isPresented: Binding(get: { calls.state.busy }, set: { _ in })) {
            CallScreen()
        }
        .modifier(LockCover(lock: lock, enabled: settings.appLockEnabled))
        .onAppear {
            app.launch()
            // A link or a tap that launched the app.
            if router.pending != nil { tab = .chat }
        }
        // Whichever screen is up: the hub is read while the core is connected.
        .task(id: app.connected) { await notifications.run(app) }
        // The calls are polled for as long as the core is connected, ringing
        // and connecting whether or not any screen is looking.
        .task(id: app.connected) { await CallCenter.shared.run(app) }
        .onChange(of: router.pending) { request in
            if request != nil { tab = .chat }
        }
        .onChange(of: scenePhase) { phase in
            switch phase {
            case .background:
                lock.didEnterBackground()
                app.enteredBackground()
                notifications.appLeft()
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
    case home, chat, apps, wallet, settings
}

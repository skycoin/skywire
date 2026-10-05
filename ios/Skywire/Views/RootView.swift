import SwiftUI

/// The app under the lock: Android's shell (SkywireApp.kt), the screen over the floating bar.
struct RootView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var lock: AppLock
    @EnvironmentObject private var router: ChatRouter
    @EnvironmentObject private var notifications: NotificationBridge
    @ObservedObject private var settings: AppSettings
    @ObservedObject private var calls = VoiceCalls.shared
    @Environment(\.scenePhase) private var scenePhase
    @StateObject private var navigator = Navigator()
    @StateObject private var dialogs = SkyDialogs()
    @State private var keyboardShown = false

    init(settings: AppSettings) {
        self.settings = settings
    }

    var body: some View {
        ZStack {
            ZStack(alignment: .bottom) {
                ZStack {
                    ForEach(AppTab.allCases, id: \.self) { tab in
                        if navigator.visited.contains(tab) {
                            let shown = navigator.tab == tab
                            TabStack(tab: tab, navigator: navigator).stackLayer(shown: shown)
                        }
                    }
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
                // The keyboard takes the bar's place, as Android's imePadding does.
                .padding(.bottom, keyboardShown ? 0 : SkyBottomBar.height)
                SkyBottomBar(navigator: navigator)
                    .ignoresSafeArea(.keyboard, edges: .bottom)
            }
            .background(Color.skyBackground.ignoresSafeArea())
            // A call owns the display, bar and all (Android swaps the UI for CallScreen).
            if calls.state.busy {
                CallScreen().accessibilityAddTraits(.isModal)
            }
            SkySheetLayer(dialogs: dialogs)
            SkyDialogLayer(dialogs: dialogs)
        }
        .environmentObject(navigator)
        .environmentObject(dialogs)
        .modifier(LockCover(lock: lock, enabled: settings.appLockEnabled))
        .onAppear {
            app.launch()
            // A link or a tap that launched the app.
            if router.pending != nil { navigator.select(.chat) }
        }
        // Whichever screen is up: the hub is read while the core is connected.
        .task(id: app.connected) { await notifications.run(app) }
        // Calls are polled while the core is connected, whether or not a screen looks.
        .task(id: app.connected) { await CallCenter.shared.run(app) }
        .onChange(of: router.pending) { request in
            if request != nil { navigator.select(.chat) }
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
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillShowNotification)) { _ in
            keyboardShown = true
        }
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillHideNotification)) { _ in
            keyboardShown = false
        }
    }
}

/// The bar's destinations (Android Routes: home, chat, hub, wallet, settings).
enum AppTab: CaseIterable, Hashable {
    case home, chat, hub, wallet, settings
}

/// Screens pushed over a tab (Android's non-tab routes).
enum Route: Hashable {
    case socks, dex, fleet, vpn
    case logs(LogSource)
}

/// Android's NavHost semantics (Routes.kt, SkywireApp.kt) over one stack per tab.
@MainActor
final class Navigator: ObservableObject {
    @Published private(set) var tab: AppTab = .home
    @Published private(set) var visited: Set<AppTab> = [.home]
    @Published private var stacks: [AppTab: [Route]] = [:]
    /// Where a tab root's back goes when it was opened from the hub.
    private var parent: [AppTab: AppTab] = [:]

    /// NavHost's default: a 700 ms cross-fade, FastOutSlowIn.
    static let fade = Animation.timingCurve(0.4, 0, 0.2, 1, duration: 0.7)

    func stack(_ tab: AppTab) -> [Route] {
        stacks[tab] ?? []
    }

    /// The highlighted bar slot: none on the hub, its app screens or a log viewer.
    var selectedSlot: AppTab? {
        if tab == .hub { return nil }
        if case .logs = stack(tab).last { return nil }
        return tab
    }

    /// A bar slot: that tab with its saved stack.
    func select(_ tab: AppTab) {
        parent[tab] = nil
        go(tab)
    }

    /// The cloud: always the hub's list, never an app screen left open under it.
    func openHub() {
        parent[.hub] = nil
        withAnimation(Self.fade) { stacks[.hub] = [] }
        go(.hub)
    }

    /// Hub ▸ SkyChat or Wallet: that tab's root, with back returning to the hub.
    func openFromHub(_ tab: AppTab) {
        withAnimation(Self.fade) { stacks[tab] = [] }
        parent[tab] = .hub
        go(tab)
    }

    func push(_ route: Route) {
        if stack(tab).last == route { return }
        withAnimation(Self.fade) { stacks[tab, default: []].append(route) }
    }

    /// Back: pop, or leave a tab root for the hub (if it came from there) or Home.
    func back() {
        if var stack = stacks[tab], !stack.isEmpty {
            stack.removeLast()
            withAnimation(Self.fade) { stacks[tab] = stack }
            return
        }
        guard tab != .home else { return }
        let target = parent[tab] ?? .home
        parent[tab] = nil
        go(target)
    }

    private func go(_ tab: AppTab) {
        withAnimation(Self.fade) {
            visited.insert(tab)
            self.tab = tab
        }
    }
}

/// One tab: its root and the screens pushed over it, the top one shown.
private struct TabStack: View {
    let tab: AppTab
    @ObservedObject var navigator: Navigator
    @EnvironmentObject private var app: AppModel

    var body: some View {
        let stack = navigator.stack(tab)
        ZStack {
            layer(root, shown: stack.isEmpty)
            ForEach(Array(stack.enumerated()), id: \.offset) { index, route in
                layer(destination(route), shown: index == stack.count - 1)
                    .transition(.opacity)
            }
        }
    }

    private func layer(_ view: some View, shown: Bool) -> some View {
        view.stackLayer(shown: shown)
    }

    @ViewBuilder private var root: some View {
        switch tab {
        case .home: HomeView()
        case .chat: ChatView()
        case .hub: HubView()
        case .wallet: WalletTab()
        case .settings: SettingsView()
        }
    }

    @ViewBuilder private func destination(_ route: Route) -> some View {
        switch route {
        case .socks: SocksView(settings: app.settings)
        case .dex: DexView()
        case .fleet: FleetView(settings: app.settings)
        case .vpn: VpnView(settings: app.settings)
        case .logs(let source): PendingRestyle { LogsView(source: source) }
        }
    }
}

/// A screen not yet rebuilt in Android's design: its own navigation bar, with back.
private struct PendingRestyle<Content: View>: View {
    @ViewBuilder let content: Content
    @EnvironmentObject private var navigator: Navigator

    var body: some View {
        NavigationStack {
            content
                .navigationBarTitleDisplayMode(.inline)
                .toolbar {
                    ToolbarItem(placement: .navigationBarLeading) {
                        Button { navigator.back() } label: { Image(systemName: "chevron.backward") }
                            .accessibilityLabel(Text("back"))
                    }
                }
        }
    }
}

extension View {
    /// A screen kept alive under the one on top: invisible, untouchable and out of accessibility.
    func stackLayer(shown: Bool) -> some View {
        modifier(StackLayer(shown: shown))
    }
}

private struct LayerVisibleKey: EnvironmentKey {
    static let defaultValue = true
}

private struct StackLayer: ViewModifier {
    let shown: Bool
    // A layer inside a hidden one (a tab's screen while another tab is up) is hidden too.
    @Environment(\.layerVisible) private var outer

    func body(content: Content) -> some View {
        let visible = shown && outer
        content
            .environment(\.layerVisible, visible)
            .opacity(shown ? 1 : 0)
            .allowsHitTesting(shown)
            // Hiding alone leaves scroll views' contents to VoiceOver; collapsing the layer first
            // takes them out, and hiding then drops the empty element left.
            .accessibilityElement(children: visible ? .contain : .ignore)
            .accessibilityHidden(!visible)
    }
}

extension EnvironmentValues {
    fileprivate var layerVisible: Bool {
        get { self[LayerVisibleKey.self] }
        set { self[LayerVisibleKey.self] = newValue }
    }
}

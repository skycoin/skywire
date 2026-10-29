import CoreClient
import SwiftUI

/// The pages inside the Chat tab; back pops one (Android: ui/chat/ChatViewModel.kt's ChatPage).
enum ChatRoute: Equatable {
    case home
    case thread(String)
    case contacts(pick: Bool)
    case contactSettings(String)
    case newContact(String)
    case myProfile
    case settings
    case groupSettings(String)
    case addAddress(String, fromLink: Bool)
    case newGroup(channel: Bool)
    case search
    case forward(from: String, message: String)
    case image(source: String, name: String)
    case video(source: String, name: String, start: Double)
}

/// The Chat tab's own navigation and per-thread drafts. Everything about the
/// conversations themselves lives in ChatRepository, which outlives this.
@MainActor
final class ChatNav: ObservableObject {
    @Published private(set) var pages: [ChatRoute] = [.home]
    @Published var replies: [String: ReplyRef] = [:]
    /// A message to scroll to once its thread shows it: the thread and the message's time.
    @Published var scrollTarget: (key: String, ts: Int64)?
    /// Unsent text per thread, so leaving a chat does not lose it.
    var drafts: [String: String] = [:]

    var top: ChatRoute { pages.last ?? .home }

    func push(_ page: ChatRoute) { pages.append(page) }

    /// Back: false when there was nothing left to pop.
    @discardableResult
    func pop() -> Bool {
        guard pages.count > 1 else { return false }
        pages.removeLast()
        return true
    }

    /// Opens a conversation from anywhere: it sits directly over the list.
    func openThread(_ key: String) { pages = [.home, .thread(key)] }

    func home() { pages = [.home] }

    /// Leaves the current page and shows `page` in its place.
    func replace(_ page: ChatRoute) {
        pages.removeLast()
        pages.append(page)
    }

    func setReply(_ key: String, _ reply: ReplyRef?) { replies[key] = reply }
}

/// The Chat tab: SkyChat drawn natively over skychat's HTTP API (Android:
/// ui/chat/ChatScreen.kt). The list is the screen until a conversation opens
/// over it; every other page stacks on top and back pops it.
struct ChatView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var router: ChatRouter
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @ObservedObject private var repo = ChatRepository.shared
    @StateObject private var nav = ChatNav()
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.layerVisible) private var onScreen

    var body: some View {
        Group {
            if repo.conn.ready {
                page.environmentObject(nav)
            } else {
                VStack(spacing: 0) {
                    SkyTopBar(title: Text("app_skychat"), onBack: { navigator.back() }, help: .chat)
                    ChatStatus(conn: repo.conn) { repo.retry() }
                }
            }
        }
        .background(Color.skyBackground)
        .environmentObject(repo)
        .onAppear { repo.requestApp() }
        .onChange(of: onScreen) { if $0 { repo.requestApp() } }
        .onChange(of: app.connected) { if $0 && onScreen { repo.requestApp() } }
        .onChange(of: repo.conn.ready) { _ in syncOpen() }
        .onChange(of: nav.pages) { _ in syncOpen() }
        .onChange(of: onScreen) { _ in syncOpen() }
        .onChange(of: scenePhase) { _ in syncOpen() }
        .onChange(of: repo.note) { note in
            guard let note else { return }
            dialogs.snackbar(Text(verbatim: note.text))
            repo.noteShown(note)
        }
        .task(id: DriveKey(request: router.pending, ready: repo.conn.ready)) {
            guard let request = router.pending, repo.conn.ready else { return }
            switch request.target {
            case let .address(address):
                nav.home()
                nav.push(.addAddress(address, fromLink: true))
            case let .conversation(key):
                if !key.isEmpty {
                    let id = repo.groups.first { $0.id.caseInsensitiveCompare(key) == .orderedSame }?.id ?? key.lowercased()
                    nav.openThread(id)
                }
            }
            router.handled(request)
        }
        .sheet(item: $repo.share) { file in
            ActivitySheet(items: [file.url]) { repo.shareDone(file) }
        }
    }

    private struct DriveKey: Equatable {
        let request: ChatRouter.Request?
        let ready: Bool
    }

    /// Only a thread actually in front of the user holds the focus lease.
    private func syncOpen() {
        let visible = onScreen && scenePhase == .active && repo.conn.ready
        if visible, case let .thread(key) = nav.top {
            repo.setOpen(key)
        } else {
            repo.setOpen(nil)
        }
    }

    private func back() {
        if !nav.pop() { navigator.back() }
    }

    @ViewBuilder private var page: some View {
        switch nav.top {
        case .home: ChatHome(onBack: { navigator.back() })
        case let .thread(key): ThreadView(key: key, onBack: back).id(key)
        case let .contacts(pick): ContactsView(pick: pick, onBack: back)
        case let .contactSettings(pk): ContactSettingsView(pk: pk, onBack: back).id(pk)
        case let .newContact(prefill): NewContactView(prefill: prefill, onBack: back)
        case .myProfile: MyProfileView(onBack: back)
        case .settings: ChatSettingsView(onBack: back)
        case let .groupSettings(gid): GroupSettingsView(gid: gid, onBack: back).id(gid)
        case let .addAddress(prefill, fromLink): AddAddressView(prefill: prefill, fromLink: fromLink, onBack: back).id(prefill)
        case let .newGroup(channel): NewGroupView(channel: channel, onBack: back)
        case .search: ChatSearchView(onBack: back)
        case let .forward(from, message): ForwardView(fromKey: from, msgKey: message, onBack: back)
        case let .image(source, name): ImageViewer(source: source, name: name, onBack: back)
        case let .video(source, name, start): VideoViewer(source: source, name: name, start: start, onBack: back)
        }
    }
}

/// What the tab shows until skychat answers.
private struct ChatStatus: View {
    @EnvironmentObject private var app: AppModel
    let conn: ChatRepository.Conn
    let retry: () -> Void

    private var connecting: Bool {
        conn.error == nil && (conn.starting || app.coreState == .starting || app.coreState == .running)
    }

    private var message: String {
        if let error = conn.error { return error }
        switch app.coreState {
        case .failed: return app.lastError ?? L10n.text("home_error_start")
        case .running where app.apiUp: return L10n.text("chat_starting")
        case .running, .starting: return L10n.text("chat_core_starting")
        case .stopped, .stopping: return L10n.text("chat_core_offline")
        }
    }

    var body: some View {
        VStack(spacing: 0) {
            if connecting {
                MaterialSpinner().padding(.bottom, 20)
            }
            Text(verbatim: message)
                .skyText(.bodyMedium)
                .foregroundStyle(Color.skyOnSurfaceVariant)
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("chat-status")
            if conn.error != nil, app.connected {
                // A closure literal, not `action: retry`: Xcode 26 crashed on a function passed here (M2).
                Button {
                    retry()
                } label: {
                    Text("socks_retry")
                }
                .buttonStyle(.tonal)
                .padding(.top, 8)
                .accessibilityIdentifier("chat-retry")
            }
        }
        .padding(32)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

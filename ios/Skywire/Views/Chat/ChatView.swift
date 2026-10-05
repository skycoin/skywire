import SwiftUI
import WebKit

/// The Chat tab: skychat's own web UI, embedded (Android: ChatScreen).
/// Everything the page cannot do for itself is ChatPage's; this screen
/// decides when there is a page to show, and what to say while there is not.
struct ChatView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var router: ChatRouter
    @StateObject private var model = ChatModel()
    @StateObject private var page = ChatPage()
    @EnvironmentObject private var navigator: Navigator
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        VStack(spacing: 0) {
            // Back is the page's own step while it has one, then the tab is left (Android).
            SkyTopBar(title: Text("app_skychat"), onBack: {
                if !page.goBack() { navigator.back() }
            }, help: .chat)
            content
        }
        .background(Color.skyBackground)
        .task(id: ChatModel.RunKey(connected: app.connected, attempt: model.attempt)) {
            await model.run(app)
        }
        // A link or a tapped notification waits until the page can take it, and is dropped
        // once it has: retrying one the page refused would only loop.
        .task(id: DriveKey(request: router.pending, ready: page.ready)) {
            guard let request = router.pending, page.ready else { return }
            _ = await page.perform(request.target)
            router.handled(request)
        }
    }

    private struct DriveKey: Equatable {
        let request: ChatRouter.Request?
        let ready: Bool
    }

    private var showsPage: Bool {
        model.url != nil && model.password != nil && page.error == nil
    }

    @ViewBuilder
    private var content: some View {
        if showsPage, let url = model.url, let password = model.password {
            ChatWebView(page: page, url: url, password: password, dark: colorScheme == .dark)
        } else {
            ChatStatus(model: model, pageError: page.error) {
                page.clearError()
                model.retry()
            }
        }
    }
}

/// The page's web view in SwiftUI. The view is ChatPage's; it is released
/// when this leaves the screen (the surface went, or an error replaced it),
/// not when another tab is selected.
private struct ChatWebView: UIViewRepresentable {
    let page: ChatPage
    let url: URL
    let password: String
    let dark: Bool

    func makeCoordinator() -> ChatPage {
        page
    }

    func makeUIView(context: Context) -> WKWebView {
        page.makeWebView()
    }

    func updateUIView(_ webView: WKWebView, context: Context) {
        page.show(url, password: password, dark: dark)
    }

    static func dismantleUIView(_ webView: WKWebView, coordinator page: ChatPage) {
        page.release()
    }
}

/// What the tab shows while there is no page to show: the core has to run
/// before skychat can, and skychat has to answer before the page is worth
/// loading.
private struct ChatStatus: View {
    @EnvironmentObject private var app: AppModel
    @ObservedObject var model: ChatModel
    let pageError: String?
    let retry: () -> Void

    private var error: String? { pageError ?? model.error }

    private var connecting: Bool {
        error == nil && (model.starting || app.coreState == .starting || (app.coreState == .running && !app.apiUp))
    }

    private var message: String {
        if let error { return error }
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
            if error != nil, app.connected {
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

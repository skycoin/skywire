import CoreClient
import SwiftUI
import WebKit

/// SkyDEX (Android: DexScreen): the market is chosen natively, the trading
/// UI is the page the desktop serves. Before a market is connected the
/// screen is the market form; once one is, a one-line header over the page.
struct DexView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var model = DexModel()
    @StateObject private var page = DexPage()

    var body: some View {
        Group {
            if model.connected, let url = model.uiURL, let password = model.password {
                VStack(spacing: 0) {
                    header
                    Divider()
                    DexWebView(page: page, url: url, password: password)
                }
            } else {
                form
            }
        }
        .navigationTitle(Text("app_skydex"))
        .navigationBarTitleDisplayMode(.inline)
        .task(id: app.connected) { await model.run(app) }
    }

    /// The market in use and its Disconnect, above the trading UI.
    private var header: some View {
        HStack(spacing: 8) {
            StatusDot(color: .success)
            Text(SavedMarket(pk: model.market?.marketPK ?? "", name: model.market?.marketName ?? "").label)
                .font(.footnote.monospaced())
                .lineLimit(1)
                .accessibilityIdentifier("dex-market")
            Spacer()
            if model.busy {
                ProgressView().controlSize(.small)
            }
            Button("disconnect") { model.disconnect(app) }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .disabled(model.busy)
                .accessibilityIdentifier("dex-disconnect")
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    private var form: some View {
        Form {
            Section {
                // One line: wrapped, the key was hyphenated where it broke,
                // and a hyphen read as part of a key is a wrong key.
                TextField(L10n.text("dex_market_label"), text: $model.entry)
                    .font(.body.monospaced())
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .accessibilityIdentifier("dex-market-field")
                if !model.entry.isEmpty, !model.entryValid {
                    Text("dex_market_invalid").font(.footnote).foregroundStyle(.red)
                }
                if !model.recents.isEmpty {
                    Menu {
                        ForEach(model.recents, id: \.self) { recent in
                            Button(recent.label) { model.pick(recent) }
                        }
                    } label: {
                        Label("dex_recents", systemImage: "clock.arrow.circlepath")
                    }
                }
            } header: {
                Text("dex_market_title")
            } footer: {
                Text("dex_market_hint")
            }
            Section {
                if !app.connected {
                    Text(app.coreState == .stopped ? L10n.key("dex_core_offline") : L10n.key("dex_core_starting"))
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                } else {
                    Button {
                        model.connect(app)
                    } label: {
                        HStack {
                            if model.busy {
                                ProgressView()
                                Text("dex_connecting")
                            } else {
                                Text("connect")
                            }
                        }
                        .frame(maxWidth: .infinity)
                    }
                    .buttonStyle(.borderedProminent)
                    .disabled(model.busy || !model.entryValid)
                    .accessibilityIdentifier("dex-connect")
                }
                if let error = model.error {
                    Text(error).font(.footnote).foregroundStyle(.red)
                        .accessibilityIdentifier("dex-error")
                }
            }
        }
    }
}

/// The trading UI's web view in SwiftUI; released with the market.
private struct DexWebView: UIViewRepresentable {
    let page: DexPage
    let url: URL
    let password: String

    func makeCoordinator() -> DexPage { page }

    func makeUIView(context: Context) -> WKWebView { page.makeWebView() }

    func updateUIView(_ webView: WKWebView, context: Context) {
        page.show(url, password: password)
    }

    static func dismantleUIView(_ webView: WKWebView, coordinator page: DexPage) {
        page.release()
    }
}

/// skydex-client's trading UI in a web view (Android: DexWebView): its gate
/// answered with the phone's secret (SkydexProfile), the page's JavaScript
/// dialogs, the page pinned at its own scale, and links off the page opened
/// outside rather than over it. Leaner than ChatPage: no QR bridge, no
/// downloads, no microphone.
@MainActor
final class DexPage: NSObject, ObservableObject {
    private(set) var webView: WKWebView?
    private var loadedURL: URL?
    private var password: String?

    func makeWebView() -> WKWebView {
        if let webView { return webView }
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        configuration.userContentController.addUserScript(
            WKUserScript(source: ChatPage.noZoomScript, injectionTime: .atDocumentEnd, forMainFrameOnly: true)
        )
        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = self
        view.uiDelegate = self
        view.isOpaque = false
        view.backgroundColor = .clear
        view.accessibilityIdentifier = "dex-webview"
        #if DEBUG
        if #available(iOS 16.4, *) { view.isInspectable = true }
        #endif
        webView = view
        return view
    }

    func show(_ url: URL, password: String) {
        self.password = password
        guard let webView, url != loadedURL else { return }
        loadedURL = url
        webView.load(URLRequest(url: url))
    }

    func release() {
        webView?.stopLoading()
        webView?.navigationDelegate = nil
        webView?.uiDelegate = nil
        webView = nil
        loadedURL = nil
    }

    private func isOwn(_ url: URL) -> Bool {
        guard let loadedURL else { return false }
        return url.scheme == loadedURL.scheme && url.host == loadedURL.host && url.port == loadedURL.port
    }
}

extension DexPage: WKNavigationDelegate {
    /// The gate; asked again after a failure means the secret is wrong.
    func webView(
        _ webView: WKWebView,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping @MainActor (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        let space = challenge.protectionSpace
        guard space.authenticationMethod == NSURLAuthenticationMethodHTTPBasic,
              space.host == loadedURL?.host, space.port == (loadedURL?.port ?? 0)
        else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        guard challenge.previousFailureCount == 0, let password else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(user: SkydexProfile.user, password: password, persistence: .forSession))
    }

    /// The trading UI is one page; anything off it opens outside.
    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void
    ) {
        guard let url = navigationAction.request.url,
              navigationAction.targetFrame?.isMainFrame ?? true,
              url.scheme != "about", !isOwn(url)
        else {
            decisionHandler(.allow)
            return
        }
        UIApplication.shared.open(url)
        decisionHandler(.cancel)
    }

    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        webView.reload()
    }
}

extension DexPage: WKUIDelegate {
    func webView(
        _ webView: WKWebView,
        createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction,
        windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        nil
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptAlertPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping @MainActor () -> Void
    ) {
        WebDialogs.alert(message, in: webView, completion: completionHandler)
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptConfirmPanelWithMessage message: String,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping @MainActor (Bool) -> Void
    ) {
        WebDialogs.confirm(message, in: webView, completion: completionHandler)
    }

    func webView(
        _ webView: WKWebView,
        runJavaScriptTextInputPanelWithPrompt prompt: String,
        defaultText: String?,
        initiatedByFrame frame: WKFrameInfo,
        completionHandler: @escaping @MainActor (String?) -> Void
    ) {
        WebDialogs.prompt(prompt, defaultText: defaultText, in: webView, completion: completionHandler)
    }
}

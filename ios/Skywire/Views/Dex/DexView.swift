import CoreClient
import SwiftUI
import UIKit
import WebKit

/// SkyDEX (Android: DexScreen): the market chosen natively, the trading UI the page the
/// desktop serves, behind a one-line header once a market is connected.
struct DexView: View {
    @EnvironmentObject private var app: AppModel
    @EnvironmentObject private var navigator: Navigator
    @EnvironmentObject private var dialogs: SkyDialogs
    @StateObject private var model = DexModel()
    @StateObject private var page = DexPage()
    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        VStack(spacing: 0) {
            // Back steps the page, then disconnects (back to the form), then leaves (Android).
            SkyTopBar(title: Text("app_skydex"), onBack: {
                if page.goBack() { return }
                if model.connected { model.disconnect(app) } else { navigator.back() }
            }, help: .dex)
            if model.connected, let url = model.uiURL, let password = model.password {
                header
                if let error = page.error {
                    VStack(spacing: 0) {
                        Text(verbatim: error).skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                            .multilineTextAlignment(.center)
                        Button { page.retry() } label: { Text("socks_retry") }.buttonStyle(.tonal).padding(.top, 8)
                    }
                    .padding(32)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                } else {
                    DexWebView(page: page, url: url, password: password, dark: colorScheme == .dark)
                }
            } else {
                ScrollView {
                    form.padding(.horizontal, 20).padding(.vertical, 16)
                }
            }
        }
        .background(Color.skyBackground)
        .task(id: app.connected) { await model.run(app) }
    }

    /// The market in use, tap to copy its key, and Disconnect.
    private var header: some View {
        let pk = model.market?.marketPK ?? ""
        let name = model.market?.marketName ?? ""
        return HStack(spacing: 0) {
            StatusDot(color: .skySuccess)
            Button {
                UIPasteboard.general.string = pk
                dialogs.toast(Text("copied_to_clipboard"))
            } label: {
                VStack(alignment: .leading, spacing: 0) {
                    (name.isEmpty ? Text("dex_market_title") : Text(verbatim: name))
                        .skyText(.titleSmall).lineLimit(1)
                    Text(verbatim: Self.short(pk)).skyText(.bodySmall, mono: true).foregroundStyle(Color.skyOnSurfaceVariant)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .contentShape(Rectangle())
            }
            .buttonStyle(PressStyle())
            .padding(.leading, 10)
            .accessibilityIdentifier("dex-market")
            Button { model.disconnect(app) } label: { Text("disconnect") }
                .buttonStyle(.tonal)
                .disabled(model.busy)
                .accessibilityIdentifier("dex-disconnect")
        }
        .padding(EdgeInsets(top: 4, leading: 20, bottom: 4, trailing: 8))
    }

    /// The market picker: a Card, not a SectionCard (16 pt corners, no border).
    private var form: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("dex_market_title").skyText(.labelMedium).foregroundStyle(Color.skyOnSurfaceVariant)
            MarketField(model: model, notch: .skySurfaceVariant).padding(.top, 10)
            if !model.entry.isEmpty, !model.entryValid {
                Text("dex_market_invalid").skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 6)
            }
            if let error = model.error {
                Text(verbatim: error).skyText(.bodySmall).foregroundStyle(Color.skyError).padding(.top, 8)
                    .accessibilityIdentifier("dex-error")
            }
            VStack(alignment: .leading, spacing: 0) {
                if !app.connected {
                    Text(app.coreState == .stopped ? L10n.key("dex_core_offline") : L10n.key("dex_core_starting"))
                        .skyText(.bodyMedium).foregroundStyle(Color.skyOnSurfaceVariant)
                } else {
                    Button { model.connect(app) } label: { Text("connect").frame(maxWidth: .infinity) }
                        .buttonStyle(.filled)
                        .disabled(model.busy || !model.entryValid)
                        .accessibilityIdentifier("dex-connect")
                    if model.busy {
                        HStack(spacing: 10) {
                            MaterialSpinner(size: 16, stroke: 2)
                            Text("dex_connecting").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant)
                        }
                        .padding(.top, 8)
                    } else {
                        Text("dex_market_hint").skyText(.bodySmall).foregroundStyle(Color.skyOnSurfaceVariant).padding(.top, 8)
                    }
                }
            }
            .padding(.top, 16)
        }
        .padding(20)
        .foregroundStyle(Color.skyOnSurface)
        .background(Color.skySurfaceVariant, in: .sky(SkyRadius.medium))
    }

    /// 8…6, the trading UI's own shortening.
    static func short(_ pk: String) -> String {
        pk.count <= 20 ? pk : "\(pk.prefix(8))…\(pk.suffix(6))"
    }
}

/// The key field with its recent-markets dropdown (Android's DropdownMenu).
private struct MarketField: View {
    @ObservedObject var model: DexModel
    let notch: Color
    @State private var open = false

    var body: some View {
        ZStack(alignment: .trailing) {
            SkyOutlinedTextField(label: L10n.key("dex_market_label"), text: $model.entry,
                                 isError: !model.entry.isEmpty && !model.entryValid, mono: true,
                                 notch: notch, identifier: "dex-market-field")
                .disabled(model.busy)
            if !model.recents.isEmpty {
                Button { open.toggle() } label: {
                    MaterialIcon(MI.filledArrowDropDown).foregroundStyle(Color.skyOnSurfaceVariant).frame(width: 48, height: 48)
                }
                .buttonStyle(PressStyle())
                .disabled(model.busy)
                .accessibilityLabel(Text("dex_recents"))
            }
        }
        .overlay(alignment: .topLeading) {
            if open {
                VStack(alignment: .leading, spacing: 0) {
                    ForEach(model.recents, id: \.self) { recent in
                        Button {
                            model.pick(recent)
                            open = false
                        } label: {
                            Text(verbatim: recent.label).skyText(.bodyMedium).lineLimit(1)
                                .frame(maxWidth: .infinity, minHeight: 48, alignment: .leading)
                                .padding(.horizontal, 12)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(PressStyle(layer: .skyOnSurface))
                    }
                }
                .padding(.vertical, 8)
                .frame(minWidth: 112, maxWidth: 280)
                .background(Color.skyContainer, in: .sky(SkyRadius.extraSmall))
                .skyElevation(3)
                .offset(y: 60)
            }
        }
        .zIndex(1)
    }
}

/// The trading UI's web view in SwiftUI; released with the market.
private struct DexWebView: UIViewRepresentable {
    let page: DexPage
    let url: URL
    let password: String
    let dark: Bool

    func makeCoordinator() -> DexPage { page }

    func makeUIView(context: Context) -> WKWebView { page.makeWebView() }

    func updateUIView(_ webView: WKWebView, context: Context) {
        page.show(url, password: password, dark: dark)
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
    private var dark = false
    /// The page failed: the WebView's own words, or the gate's refusal.
    @Published private(set) var error: String?

    func makeWebView() -> WKWebView {
        if let webView { return webView }
        let configuration = WKWebViewConfiguration()
        configuration.websiteDataStore = .default()
        // Pinch zoom stays on, as on Android; the phone stylesheet fits the page first.
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

    func show(_ url: URL, password: String, dark: Bool) {
        self.password = password
        guard let webView else { return }
        if dark != self.dark {
            self.dark = dark
            webView.evaluateJavaScript(DexInjections.theme(dark: dark))
        }
        guard url != loadedURL else { return }
        loadedURL = url
        error = nil
        webView.load(URLRequest(url: url))
    }

    /// One step back in the page; false when there is none.
    func goBack() -> Bool {
        guard let webView, webView.canGoBack else { return false }
        webView.goBack()
        return true
    }

    /// PageError's Retry: load the page again.
    func retry() {
        error = nil
        let url = loadedURL
        loadedURL = nil
        if let url, let password { show(url, password: password, dark: dark) }
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
            error = L10n.text("dex_error_password")
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

    // Android's injections: the theme when the page commits and again at finish, then the phone layout.
    func webView(_ webView: WKWebView, didCommit navigation: WKNavigation!) {
        webView.evaluateJavaScript(DexInjections.theme(dark: dark))
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        webView.evaluateJavaScript(DexInjections.theme(dark: dark))
        webView.evaluateJavaScript(DexInjections.phone)
        webView.evaluateJavaScript(DexInjections.tableCards)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: any Error) {
        failed(error)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: any Error) {
        failed(error)
    }

    private func failed(_ error: any Error) {
        if (error as NSError).code == NSURLErrorCancelled { return }
        self.error = error.localizedDescription
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

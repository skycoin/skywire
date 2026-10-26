import CoreClient
import os
import UIKit
import WebKit

/// skychat's own web UI in a web view, and everything around it the page
/// cannot do for itself (Android: ui/chat/ChatWebView.kt): the password gate,
/// the rule for what may leave the page, the microphone and camera, the page's
/// JavaScript dialogs, downloads, and its console.
///
/// Deliberately the same UI the desktop serves rather than a native rewrite:
/// one chat codebase, whose phone layout is a breakpoint inside it.
///
/// One web view per surface: made when the chat comes up and dropped with it
/// (the core stopped, or an error replaced it), because a page left loaded
/// keeps its event stream polling a listener that has gone, and a blank page
/// loaded over it would stay in the back history. Switching tabs keeps it: a
/// reload would drop the open conversation, the scroll position and the live
/// event stream.
@MainActor
final class ChatPage: NSObject, ObservableObject {
    /// The page finished loading and can be driven (the theme, links).
    @Published private(set) var ready = false
    /// The page broke after the surface came up: the gate refused the
    /// password, or the document itself failed to load.
    @Published private(set) var error: String?

    private(set) var webView: WKWebView?
    private let downloads = ChatDownloads()
    /// The address loaded, without the theme query.
    private var loadedURL: URL?
    private var loadedLang: String?
    private var dark = false
    private var password: String?
    /// The responsiveness ping in flight, and whether it has been reported late.
    private var pingSent: Date?
    private var pingLate = false

    static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "chat")
    private static let consoleHandler = "skywireConsole"

    override init() {
        super.init()
        downloads.credential = { [weak self] in self?.credential }
    }

    /// The web view for the surface, made on first use.
    func makeWebView() -> WKWebView {
        if let webView { return webView }
        let configuration = WKWebViewConfiguration()
        // Voice and video messages play in their bubble, not in the system
        // player.
        configuration.allowsInlineMediaPlayback = true
        // Nothing plays until the user taps it (Android's
        // mediaPlaybackRequiresUserGesture).
        configuration.mediaTypesRequiringUserActionForPlayback = .all
        // Persistent: Saved Messages, the tab choice and the notification
        // preferences live in the page's localStorage.
        configuration.websiteDataStore = .default()
        let content = configuration.userContentController
        content.addUserScript(WKUserScript(source: Self.consoleScript, injectionTime: .atDocumentStart, forMainFrameOnly: true))
        content.addUserScript(WKUserScript(source: Self.noZoomScript, injectionTime: .atDocumentEnd, forMainFrameOnly: true))
        content.add(WeakScriptHandler(self), name: Self.consoleHandler)
        QrBridge.install(in: content)

        let view = WKWebView(frame: .zero, configuration: configuration)
        view.navigationDelegate = self
        view.uiDelegate = self
        // The swipe from the left edge steps back through the page's own
        // history (a conversation open over the list, a video full screen),
        // as Android's back gesture does.
        view.allowsBackForwardNavigationGestures = true
        // Transparent until the page paints, so a dark page opens without a
        // white flash.
        view.isOpaque = false
        view.backgroundColor = .clear
        view.scrollView.backgroundColor = .clear
        view.accessibilityIdentifier = "chat-webview"
        #if DEBUG
        // Safari's Web Inspector on the Mac, for a debug build only (Android:
        // setWebContentsDebuggingEnabled on a debuggable APK).
        if #available(iOS 16.4, *) { view.isInspectable = true }
        #endif
        downloads.presenter = view
        webView = view
        return view
    }

    /// Shows the chat at `url`. Loads only when the address or the language
    /// changed: the theme and the language ride in the URL so the page picks
    /// them before it paints, and a theme change moves the open page over
    /// instead of reloading it. The page translates itself only on load, as
    /// Android's language change recreates the activity.
    func show(_ url: URL, password: String, dark: Bool, lang: String?) {
        self.password = password
        guard let webView else { return }
        if url != loadedURL || lang != loadedLang {
            loadedURL = url
            loadedLang = lang
            self.dark = dark
            ready = false
            error = nil
            webView.load(URLRequest(url: Self.themed(url, dark: dark, lang: lang)))
        } else if dark != self.dark {
            self.dark = dark
            applyTheme()
        }
    }

    /// Drops the web view with its surface. The error, if any, stays for the
    /// screen that replaced it.
    func release() {
        guard let webView else { return }
        ChatPageLog.add("webview: page closed")
        webView.stopLoading()
        webView.configuration.userContentController.removeAllScriptMessageHandlers()
        webView.navigationDelegate = nil
        webView.uiDelegate = nil
        self.webView = nil
        loadedURL = nil
        loadedLang = nil
        ready = false
    }

    /// One step back in the page (a conversation over the list); false when there is none.
    func goBack() -> Bool {
        guard let webView, webView.canGoBack else { return false }
        webView.goBack()
        return true
    }

    /// The error state's Retry: the next surface starts clean.
    func clearError() {
        error = nil
    }

    /// Drives the open page to `target` (ChatRouter): a link's address into
    /// the page's Add-by-address dialog (it shows it and stops there), or a
    /// notification's conversation opened. Driving the page rather than
    /// loading a URL for it keeps the open conversation, the scroll position
    /// and the event stream. Retried while the page's own object is not built
    /// yet (the load finished a moment ago) or, for a conversation, while the
    /// page has not loaded it: openThreadFromHost (Android's ChatWebView.openThread)
    /// opens only a peer or group the page already has, and the groups wait on
    /// the visor's RPC. False if the page never took it.
    func perform(_ target: ChatRouter.Target) async -> Bool {
        let script: String
        let argument: String
        let attempts: Int
        switch target {
        case let .address(address):
            script = """
            var chat = window.app;
            if (!chat || typeof chat.openAddressFromLink !== 'function') return 'wait';
            return chat.openAddressFromLink(value) ? 'ok' : 'ignored';
            """
            argument = address
            attempts = Self.driveAttempts
        case let .conversation(key):
            script = """
            var chat = window.app;
            if (!chat || typeof chat.openThreadFromHost !== 'function') return 'wait';
            if (!value) return 'ok';
            return chat.openThreadFromHost(value) ? 'ok' : 'wait';
            """
            argument = key
            attempts = Self.threadAttempts
        }
        for _ in 0..<attempts {
            guard let webView, ready else { return false }
            let answer = try? await webView.callAsyncJavaScript(script, arguments: ["value": argument], contentWorld: .page)
            switch answer as? String {
            case "ok": return true
            case "wait": break
            default: return false
            }
            do {
                try await Task.sleep(for: Self.driveRetry)
            } catch {
                return false
            }
        }
        Self.log.notice("the chat page never took \(String(describing: target), privacy: .public)")
        return false
    }

    /// The page's JavaScript, checked from outside while it is on screen: a page
    /// stuck in a loop or a wait looks like one ignoring taps. Logged when it
    /// goes `pageStall` without answering, and again when it answers (Android:
    /// ChatScreen's page ping; WebKit has no unresponsive callback either).
    func watchResponsiveness() async {
        pingSent = nil
        pingLate = false
        while !Task.isCancelled {
            do { try await Task.sleep(for: Self.pagePing) } catch { return }
            guard let webView, ready else { continue }
            if let sent = pingSent {
                let waited = Date().timeIntervalSince(sent)
                if !pingLate, waited >= Self.pageStall {
                    pingLate = true
                    ChatPageLog.add("webview: page has not answered for \(Int(waited)) s")
                }
                continue
            }
            let sent = Date()
            pingSent = sent
            webView.evaluateJavaScript("0") { [weak self] _, _ in
                guard let self, self.pingSent == sent else { return }
                if self.pingLate {
                    ChatPageLog.add("webview: page answering again after \(Int(Date().timeIntervalSince(sent))) s")
                }
                self.pingSent = nil
                self.pingLate = false
            }
        }
    }

    private static let pagePing: Duration = .seconds(2)
    private static let pageStall: TimeInterval = 5

    /// ~3 s: the page builds its object with the document.
    private static let driveAttempts = 20
    /// ~10 s, as Android: a conversation is known once the contacts and groups have loaded.
    private static let threadAttempts = 67
    private static let driveRetry: Duration = .milliseconds(150)

    /// `?theme=dark|light`, which the page reads on its first line, and
    /// `&lang=` when the page should speak a language other than English.
    static func themed(_ url: URL, dark: Bool, lang: String? = nil) -> URL {
        var components = URLComponents(url: url, resolvingAgainstBaseURL: false)
        components?.queryItems = [URLQueryItem(name: "theme", value: dark ? "dark" : "light")]
            + (lang.map { [URLQueryItem(name: "lang", value: $0)] } ?? [])
        return components?.url ?? url
    }

    /// The languages the page carries its own translations for (skychat's index.html).
    static let pageLanguages: Set<String> = ["zh", "es", "fr"]

    /// The page's language for the app's: the one the app draws in, the phone's when the setting
    /// is System, and nothing for English or a language the page does not ship.
    static func pageLang(_ language: AppLanguage) -> String? {
        let code = Locale(identifier: language.resolvedTag).language.languageCode?.identifier
        return code.flatMap { pageLanguages.contains($0) ? $0 : nil }
    }

    /// Sets the attribute the theme query sets, so the CSS reacts to one
    /// thing. Silent before the page has a document: the next load carries
    /// the theme in its URL anyway.
    private func applyTheme() {
        guard ready, let webView else { return }
        webView.evaluateJavaScript("document.documentElement.dataset.theme = '\(dark ? "dark" : "light")';", completionHandler: nil)
    }

    private var credential: URLCredential? {
        password.map { URLCredential(user: SkychatProfile.user, password: $0, persistence: .forSession) }
    }

    /// The loaded surface's origin: scheme, host and port.
    private func isOwn(_ url: URL) -> Bool {
        guard let loadedURL else { return false }
        return url.scheme == loadedURL.scheme && url.host == loadedURL.host && url.port == loadedURL.port
    }

    private func isOwn(_ origin: WKSecurityOrigin) -> Bool {
        guard let loadedURL else { return false }
        return origin.protocol == loadedURL.scheme && origin.host == loadedURL.host && origin.port == (loadedURL.port ?? 0)
    }

    private func isOwn(_ space: URLProtectionSpace) -> Bool {
        guard let loadedURL else { return false }
        return space.host == loadedURL.host && space.port == (loadedURL.port ?? 0)
    }

    /// What a main-frame navigation means. The page never navigates: it is
    /// one document driven by pushState, so a navigation is an attachment
    /// (a `download` link, `/files/…`, `/export/…`), which would otherwise
    /// replace the chat, or a link off the page, which belongs in the browser
    /// rather than in skychat's origin.
    private func policy(for action: WKNavigationAction) -> WKNavigationActionPolicy {
        guard let url = action.request.url else { return .allow }
        if action.shouldPerformDownload { return .download }
        // Frames inside the page: it has none today, and they cannot replace
        // it. A nil target is a new window, decided like the main frame.
        guard action.targetFrame?.isMainFrame ?? true else { return .allow }
        if url.scheme == "about" { return .allow }
        if isOwn(url) {
            // The document itself (the first load, a reload) is not a download.
            return url.path.isEmpty || url.path == "/" ? .allow : .download
        }
        UIApplication.shared.open(url)
        return .cancel
    }

    private func loadFailed(_ error: any Error) {
        let error = error as NSError
        // A navigation the policy cancelled or turned into a download ends
        // here too; neither is a broken page.
        if error.domain == NSURLErrorDomain, error.code == NSURLErrorCancelled { return }
        if error.domain == "WebKitErrorDomain", error.code == Self.frameLoadInterruptedByPolicyChange { return }
        self.error = error.localizedDescription
    }

    /// WebKitErrorFrameLoadInterruptedByPolicyChange, which Swift does not
    /// import.
    private static let frameLoadInterruptedByPolicyChange = 102

    /// Pins the page at its own scale (Android's setSupportZoom(false)). The
    /// page ships its phone layout at `width=device-width, initial-scale=1`
    /// with no upper bound, and its composer's text is 14 px: WebKit on iOS
    /// zooms into any focused field under 16 px and stays zoomed after the
    /// keyboard goes, which left the conversation magnified and scrolled
    /// sideways, the other side's bubbles off the left edge and the header off
    /// the top. A pinch or a double tap did the same. `maximum-scale=1` stops
    /// all three (WKWebView honours the limit unless `ignoresViewportScaleLimits`
    /// is set). Added here rather than in the page, which desktop browsers
    /// share.
    static let noZoomScript = """
    (function () {
      var meta = document.querySelector('meta[name="viewport"]');
      if (!meta) {
        meta = document.createElement('meta');
        meta.name = 'viewport';
        (document.head || document.documentElement).appendChild(meta);
      }
      var content = meta.getAttribute('content') || 'width=device-width, initial-scale=1';
      if (!/maximum-scale/.test(content)) content += ', maximum-scale=1';
      meta.setAttribute('content', content);
    })();
    """

    /// Relays the page's console and uncaught errors to the unified log
    /// (category "chat"): chat problems on a phone are otherwise invisible
    /// (Android logs them to logcat the same way).
    private static let consoleScript = """
    (function () {
      var post = function (level, args) {
        try {
          var text = Array.prototype.map.call(args, function (a) {
            if (a instanceof Error) return a.stack || String(a);
            if (a !== null && typeof a === 'object') { try { return JSON.stringify(a); } catch (e) { return String(a); } }
            return String(a);
          }).join(' ');
          window.webkit.messageHandlers.\(consoleHandler).postMessage({ level: level, text: text.slice(0, 2000) });
        } catch (e) {}
      };
      ['log', 'info', 'warn', 'error', 'debug'].forEach(function (level) {
        var original = console[level];
        console[level] = function () { post(level, arguments); return original.apply(console, arguments); };
      });
      window.addEventListener('error', function (e) { post('error', [e.message + ' (' + e.filename + ':' + e.lineno + ')']); });
      window.addEventListener('unhandledrejection', function (e) { post('error', ['unhandled rejection:', e.reason]); });
    })();
    """
}

extension ChatPage: WKNavigationDelegate {
    /// The password gate (cmd/apps/skychat/commands/auth.go), answered with
    /// the device-local secret. Asked again after a failure means the secret
    /// is wrong; answering again would loop.
    func webView(
        _ webView: WKWebView,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping @MainActor (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        let space = challenge.protectionSpace
        guard space.authenticationMethod == NSURLAuthenticationMethodHTTPBasic, isOwn(space) else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        guard challenge.previousFailureCount == 0, let credential else {
            completionHandler(.cancelAuthenticationChallenge, nil)
            error = L10n.text("chat_error_password")
            return
        }
        completionHandler(.useCredential, credential)
    }

    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationAction: WKNavigationAction,
        decisionHandler: @escaping @MainActor (WKNavigationActionPolicy) -> Void
    ) {
        decisionHandler(policy(for: navigationAction))
    }

    /// A document the page cannot show, or one the server says to save, is a
    /// download too.
    func webView(
        _ webView: WKWebView,
        decidePolicyFor navigationResponse: WKNavigationResponse,
        decisionHandler: @escaping @MainActor (WKNavigationResponsePolicy) -> Void
    ) {
        guard navigationResponse.isForMainFrame else {
            decisionHandler(.allow)
            return
        }
        let disposition = (navigationResponse.response as? HTTPURLResponse)?
            .value(forHTTPHeaderField: "Content-Disposition")?.lowercased() ?? ""
        let save = !navigationResponse.canShowMIMEType || disposition.hasPrefix("attachment")
        decisionHandler(save ? .download : .allow)
    }

    func webView(_ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload) {
        downloads.adopt(download)
    }

    func webView(_ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload) {
        downloads.adopt(download)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        ready = webView.url.map(isOwn) ?? false
        if ready { ChatPageLog.add("webview: page loaded") }
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: any Error) {
        loadFailed(error)
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: any Error) {
        loadFailed(error)
    }

    /// The system reclaimed the page's process (memory pressure, mostly in
    /// the background); the view would stay blank until something reloads it.
    func webViewWebContentProcessDidTerminate(_ webView: WKWebView) {
        Self.log.notice("chat page's web process ended; reloading")
        ChatPageLog.add("webview: the page's web process ended, reloading")
        ready = false
        webView.reload()
    }
}

extension ChatPage: WKUIDelegate {
    /// The page opens no windows; one asked for is refused rather than
    /// loaded over the chat.
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

    /// The microphone for voice messages and the camera for video messages
    /// and QR scanning, for the chat's own origin only. Granted here without
    /// WebKit's own per-page prompt: the system's permission prompt (the
    /// Info.plist usage strings) is the user's one decision, as Android's
    /// runtime permission is there.
    func webView(
        _ webView: WKWebView,
        requestMediaCapturePermissionFor origin: WKSecurityOrigin,
        initiatedByFrame frame: WKFrameInfo,
        type: WKMediaCaptureType,
        decisionHandler: @escaping @MainActor (WKPermissionDecision) -> Void
    ) {
        let grant = isOwn(origin)
        let device = switch type {
        case .camera: "camera"
        case .microphone: "microphone"
        default: "camera and microphone"
        }
        ChatPageLog.add("webview: page asked for the \(device), \(grant ? "granted" : "refused, not the chat's origin")")
        decisionHandler(grant ? .grant : .deny)
    }
}

extension ChatPage: WKScriptMessageHandler {
    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        guard message.name == Self.consoleHandler, let body = message.body as? [String: Any] else { return }
        let level = body["level"] as? String ?? "log"
        let text = body["text"] as? String ?? ""
        Self.log.debug("page \(level, privacy: .public): \(text, privacy: .public)")
        ChatPageLog.add("console \(level) \(text)")
    }
}

/// WKUserContentController keeps its handlers strongly and the web view keeps
/// the controller; this breaks the cycle through the page.
final class WeakScriptHandler: NSObject, WKScriptMessageHandler {
    private weak var target: (any WKScriptMessageHandler)?

    init(_ target: any WKScriptMessageHandler) {
        self.target = target
    }

    func userContentController(_ userContentController: WKUserContentController, didReceive message: WKScriptMessage) {
        target?.userContentController(userContentController, didReceive: message)
    }
}

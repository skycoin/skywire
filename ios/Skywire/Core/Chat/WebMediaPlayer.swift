import Foundation
import SwiftUI
import WebKit

/// Plays what AVFoundation cannot open: the desktop page records voice as
/// `.weba` and video as `.webm` (Opus, VP8/VP9 in WebM), which WebKit's media
/// stack decodes. A media element in a web view of its own, driven from Swift,
/// so the bubbles keep their native controls (index.html recorder formats).
enum WebMedia {
    /// Containers AVFoundation does not read.
    static func needs(_ name: String) -> Bool {
        ["webm", "weba", "mkv", "ogv"].contains((name as NSString).pathExtension.lowercased())
    }

    /// A page with one media element filling it, the file read from the cache folder.
    static func page(video: Bool, controls: Bool = false) -> String {
        let fit = controls ? "contain" : "cover"
        let tag = video
            ? "<video id=m playsinline webkit-playsinline preload=auto \(controls ? "controls" : "") style=\"width:100%;height:100%;object-fit:\(fit);background:#000\"></video>"
            : "<audio id=m preload=auto></audio>"
        return "<!doctype html><meta name=viewport content=\"width=device-width,initial-scale=1\"><body style=\"margin:0;background:#000;overflow:hidden\">\(tag)</body>"
    }

    @MainActor
    static func makeView(video: Bool) -> WKWebView {
        let configuration = WKWebViewConfiguration()
        configuration.allowsInlineMediaPlayback = true
        configuration.mediaTypesRequiringUserActionForPlayback = []
        let view = WKWebView(frame: CGRect(x: 0, y: 0, width: 2, height: 2), configuration: configuration)
        view.isOpaque = false
        view.backgroundColor = .black
        view.scrollView.isScrollEnabled = false
        return view
    }
}

/// One WebKit media element: load a file, play, pause, seek, rate, and its state polled.
@MainActor
final class WebMediaElement: NSObject, WKNavigationDelegate {
    let view: WKWebView
    private var loaded: CheckedContinuation<Void, Never>?
    private var ready = false

    init(video: Bool, controls: Bool = false) {
        view = WebMedia.makeView(video: video)
        super.init()
        view.navigationDelegate = self
        // A file URL page may read the cache and upload folders, where the clips are.
        let base = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0]
        let page = base.appendingPathComponent("chat-media-player-\(video ? "v" : "a")\(controls ? "c" : "").html")
        try? WebMedia.page(video: video, controls: controls).write(to: page, atomically: true, encoding: .utf8)
        view.loadFileURL(page, allowingReadAccessTo: base)
    }

    /// Audio plays from a view in no window only while WebKit lets it: park it, invisible, in the window.
    func attachHidden() {
        guard view.window == nil,
              let window = UIApplication.shared.connectedScenes.compactMap({ ($0 as? UIWindowScene)?.keyWindow }).first
        else { return }
        view.frame = CGRect(x: 0, y: 0, width: 1, height: 1)
        view.alpha = 0.01
        view.isUserInteractionEnabled = false
        view.accessibilityElementsHidden = true
        window.addSubview(view)
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        ready = true
        loaded?.resume()
        loaded = nil
    }

    private func waitLoaded() async {
        if ready { return }
        await withCheckedContinuation { loaded = $0 }
    }

    private func run(_ script: String, _ arguments: [String: Any] = [:]) async -> Any? {
        await waitLoaded()
        return try? await view.callAsyncJavaScript(script, arguments: arguments, contentWorld: .page)
    }

    /// Loads `file` and waits for its length; nil when WebKit cannot play it either.
    func load(_ file: URL) async -> Double? {
        let script = """
        var m = document.getElementById('m');
        m.src = src;
        return await new Promise(function (done) {
          m.onloadedmetadata = function () { done(isFinite(m.duration) ? m.duration : 0); };
          m.onerror = function () { done(-1); };
          m.load();
        });
        """
        guard let duration = await run(script, ["src": file.absoluteString]) as? Double, duration >= 0 else { return nil }
        return duration
    }

    func play(rate: Float) async {
        _ = await run("var m = document.getElementById('m'); m.playbackRate = r; await m.play(); return 1;", ["r": Double(rate)])
    }

    func pause() async {
        _ = await run("document.getElementById('m').pause(); return 1;")
    }

    func seek(_ seconds: Double) async {
        _ = await run("document.getElementById('m').currentTime = t; return 1;", ["t": seconds])
    }

    func setRate(_ rate: Float) async {
        _ = await run("document.getElementById('m').playbackRate = r; return 1;", ["r": Double(rate)])
    }

    /// Position, length, and whether it is playing.
    func state() async -> (position: Double, duration: Double, playing: Bool)? {
        guard let values = await run("var m = document.getElementById('m'); return [m.currentTime || 0, isFinite(m.duration) ? m.duration : 0, (!m.paused && !m.ended) ? 1 : 0];") as? [Any],
              values.count == 3
        else { return nil }
        let number = { (v: Any) in (v as? NSNumber)?.doubleValue ?? 0 }
        return (number(values[0]), number(values[1]), number(values[2]) > 0)
    }

    func stop() {
        view.evaluateJavaScript("var m = document.getElementById('m'); if (m) { m.pause(); m.removeAttribute('src'); m.load(); }")
    }
}

/// A WebKit video element as a SwiftUI view, for WebM clips.
struct WebVideoSurface: UIViewRepresentable {
    let element: WebMediaElement

    func makeUIView(context: Context) -> WKWebView { element.view }

    func updateUIView(_ view: WKWebView, context: Context) {}
}

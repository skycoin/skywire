import CoreClient
import Foundation
import WebKit

/// One-time move of what the old embedded page kept in WebKit's localStorage
/// (Android: chat/LegacyImport.kt): Saved Messages, DM caches with their
/// ticks, contact pictures, the address book list, pin, mutes and the small
/// preferences. The storage is only readable from the page's own origin, so a
/// hidden web view loads a page there and hands the storage back as JSON.
@MainActor
enum ChatLegacyImport {
    static func maybeRun(_ repo: ChatRepository) {
        guard !repo.store.state.legacyImported else { return }
        let webKit = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0].appendingPathComponent("WebKit", isDirectory: true)
        guard FileManager.default.fileExists(atPath: webKit.path) else {
            repo.store.update { $0.legacyImported = true }
            return
        }
        Task {
            guard let storage = await readStorage(repo) else { return }
            apply(repo, storage)
            repo.store.update { $0.legacyImported = true }
        }
    }

    private static func readStorage(_ repo: ChatRepository) async -> [String: String]? {
        guard let base = repo.skychatURL, let password = try? SkychatOrigin.password() else { return nil }
        return await LocalStorageReader.read(base.appendingPathComponent("status"), user: SkychatProfile.user, password: password)
    }

    private static func apply(_ repo: ChatRepository, _ ls: [String: String]) {
        let me = repo.myPk
        if let owner = ls.first(where: { $0.key.hasPrefix("skychat_owner:") })?.value.lowercased(), owner != me {
            ChatPageLog.add("chat: legacy storage belongs to another identity, not imported")
            return
        }
        func parse(_ key: String) -> Any? { ls[key].flatMap { try? JSONSerialization.jsonObject(with: Data($0.utf8), options: .fragmentsAllowed) } }
        func strings(_ key: String) -> [String] { (parse(key) as? [Any])?.compactMap { $0 as? String } ?? [] }

        let recipients = strings("r").map { $0.lowercased() }.filter { Skychat.isPk($0) && $0 != me }
        let contacts = strings("skychat_contacts").map { $0.lowercased() }.filter { Skychat.isPk($0) }
        let seen = (parse("s") as? [String: Any])?.compactMapValues { ($0 as? NSNumber)?.intValue } ?? [:]
        var readUpTo: [String: Int64] = [:]
        for (key, value) in ls where key.hasPrefix("c_") {
            let thread = String(key.dropFirst(2)).lowercased()
            guard thread == Skychat.savedKey || (Skychat.isPk(thread) && thread != me),
                  let items = (try? JSONSerialization.jsonObject(with: Data(value.utf8))) as? [Any]
            else { continue }
            let msgs = items.compactMap { ($0 as? [String: Any]).flatMap(message) }
            guard !msgs.isEmpty else { continue }
            repo.mutate(thread) { current in
                var merged = current
                for m in msgs where !merged.contains(where: { e in
                    (!m.dmWireId.isEmpty && e.dmWireId == m.dmWireId) || (e.mine == m.mine && e.text == m.text && abs(e.ts - m.ts) < 120_000)
                }) {
                    merged.append(m)
                }
                return merged.sorted { $0.ts < $1.ts }
            }
            if thread != Skychat.savedKey {
                let count = seen[thread] ?? msgs.count
                if count >= 1 && count <= msgs.count { readUpTo[thread] = msgs[count - 1].ts }
            }
        }
        for (key, value) in ls where key.hasPrefix("i_") && value.hasPrefix("data:") {
            let pk = String(key.dropFirst(2)).lowercased()
            if let bytes = DataURL.decode(value), !repo.store.hasAvatar(pk) { repo.store.setAvatar(pk, bytes) }
        }
        let tabs: Set<String> = ["all", "dm", "groups", "channels", "calls"]
        repo.store.update { s in
            var known = Set(s.recipients)
            s.recipients += recipients.filter { known.insert($0).inserted }
            var book = Set(s.contacts)
            s.contacts += contacts.filter { book.insert($0).inserted }
            s.readUpTo = readUpTo.merging(s.readUpTo) { _, kept in kept }
            if let tab = ls["skychat_tab"], tabs.contains(tab) { s.tab = tab }
            if let pinned = ls["skychat_pinned"] { s.pinned = pinned }
            s.hiddenGroup.formUnion(strings("skychat_hidden_group"))
            if let mode = ls["skychat_media_mode"], mode == "video" || mode == "mic" { s.mediaMode = mode }
            if let facing = ls["skychat_video_facing"] { s.videoFacingFront = facing != "environment" }
            if let shape = ls["skychat_video_shape"] { s.videoSquare = shape == "square" }
            if let speed = ls["skychat_speed"].flatMap(Float.init) { s.speed = speed }
            s.pairContacts.formUnion(strings("skychat_pair_contacts"))
        }
        for pk in recipients { repo.ensureRecipient(pk) }
        if let notif = parse("skychat_notif") as? [String: Any] {
            repo.store.updateNotif { n in
                if let enabled = notif["enabled"] as? Bool { n.enabled = enabled }
                if let preview = notif["preview"] as? Bool { n.preview = preview }
                if let muted = notif["muted"] as? [String: Any] { n.muted.formUnion(muted.keys) }
            }
        }
        if let names = parse("skychat_names") as? [String: Any] {
            var map: [String: String] = [:]
            for (key, value) in names {
                if let text = (value as? String)?.trimmingCharacters(in: .whitespaces), !text.isEmpty { map[key.lowercased()] = String(text.prefix(Skychat.nameMax)) }
            }
            Task {
                if !map.isEmpty { try? await repo.api.importContacts(map) }
                await repo.syncNames()
            }
        }
        repo.recomputeAll()
        ChatPageLog.add("chat: imported the old page's storage (\(recipients.count) chats, \(contacts.count) contacts)")
    }

    private static func message(_ o: [String: Any]) -> ChatMessage? {
        if o["date"] != nil && o["from"] == nil { return nil }
        func s(_ key: String, _ source: [String: Any]? = nil) -> String { (source ?? o)[key] as? String ?? "" }
        guard let ts = Skychat.parseTime(s("ts")) else { return nil }
        let from = s("from").lowercased().isEmpty ? Skychat.me : s("from").lowercased()
        var file: ChatFile?
        if let f = o["file"] as? [String: Any] {
            var url = s("url", f)
            var local = ""
            if url.hasPrefix("data:") {
                if let bytes = DataURL.decode(url) {
                    let folder = UUID().uuidString
                    let dir = ChatStore.uploads.appendingPathComponent(folder, isDirectory: true)
                    try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
                    if (try? bytes.write(to: dir.appendingPathComponent("image.jpg"))) != nil { local = folder + "/image.jpg" }
                }
                url = ""
            } else if !url.hasPrefix("/files/") {
                url = ""
            }
            file = ChatFile(
                id: s("id", f), name: s("name", f).isEmpty ? "file" : s("name", f), size: (f["size"] as? NSNumber)?.int64Value ?? 0,
                status: s("status", f), url: url, from: s("from", f).lowercased(), localPath: local
            )
        }
        let reply = (o["reply"] as? [String: Any]).map { ReplyRef(sender: s("sender", $0).lowercased(), ts: s("ts", $0), preview: s("preview", $0)) }
        var status = s("status")
        if status == "queued" || status == "pending" { status = "failed" }
        return ChatMessage(
            ts: ts, from: from == "me" ? Skychat.me : from, text: s("text"), viaPair: o["viaPair"] as? Bool ?? false,
            file: file, reply: reply, forwarded: o["forwarded"] as? Bool ?? false, forwardFrom: s("forwardFrom"),
            wireId: s("wireId"), status: from == Skychat.me ? status : "", readSent: o["_readSent"] as? Bool ?? false
        )
    }
}

/// What an old embedded page kept in WebKit's localStorage. It is readable only
/// from the page's own origin, so a hidden web view loads a page there, answers
/// the password gate, and hands the storage back as JSON.
@MainActor
final class LocalStorageReader: NSObject, WKNavigationDelegate {
    private static let timeout: Duration = .seconds(20)
    private static let dump = "(function(){var o={};for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);o[k]=localStorage.getItem(k);}return JSON.stringify(o);})()"

    private let user: String
    private let password: String
    private var view: WKWebView?
    private var done: CheckedContinuation<String?, Never>?

    private init(user: String, password: String) {
        self.user = user
        self.password = password
    }

    /// The storage of `url`'s origin, or nil when it could not be read within 20 s.
    static func read(_ url: URL, user: String, password: String) async -> [String: String]? {
        let reader = LocalStorageReader(user: user, password: password)
        defer { reader.close() }
        let raw = await withTaskGroup(of: String?.self) { group in
            group.addTask { await reader.load(url) }
            group.addTask {
                try? await Task.sleep(for: timeout)
                return nil
            }
            let first = await group.next() ?? nil
            group.cancelAll()
            return first
        }
        guard let raw, let object = (try? JSONSerialization.jsonObject(with: Data(raw.utf8))) as? [String: Any] else { return nil }
        return object.compactMapValues { $0 as? String }
    }

    private func load(_ url: URL) async -> String? {
        await withCheckedContinuation { continuation in
            done = continuation
            let configuration = WKWebViewConfiguration()
            configuration.websiteDataStore = .default()
            let view = WKWebView(frame: .zero, configuration: configuration)
            view.navigationDelegate = self
            self.view = view
            view.load(URLRequest(url: url))
        }
    }

    private func close() {
        view?.stopLoading()
        view?.navigationDelegate = nil
        view = nil
        finish(nil)
    }

    private func finish(_ value: String?) {
        done?.resume(returning: value)
        done = nil
    }

    func webView(_ webView: WKWebView, didFinish navigation: WKNavigation!) {
        webView.evaluateJavaScript(Self.dump) { [weak self] result, _ in
            MainActor.assumeIsolated { self?.finish(result as? String) }
        }
    }

    func webView(_ webView: WKWebView, didFail navigation: WKNavigation!, withError error: any Error) {
        finish(nil)
    }

    func webView(_ webView: WKWebView, didFailProvisionalNavigation navigation: WKNavigation!, withError error: any Error) {
        finish(nil)
    }

    func webView(
        _ webView: WKWebView,
        didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping @MainActor (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodHTTPBasic, challenge.previousFailureCount == 0 else {
            completionHandler(.performDefaultHandling, nil)
            return
        }
        completionHandler(.useCredential, URLCredential(user: user, password: password, persistence: .forSession))
    }
}

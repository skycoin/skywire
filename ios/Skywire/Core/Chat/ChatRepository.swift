import CoreClient
import Foundation
import os
import UIKit

/// Process-wide SkyChat state for the native screens (Android: chat/ChatRepository.kt):
/// it brings skychat up, holds every conversation, follows `/sse`, and does
/// what the web page did in the browser: local threads, unread counts,
/// receipts, the focus lease.
@MainActor
final class ChatRepository: ObservableObject {
    static let shared = ChatRepository()

    /// How far the chat surface is from usable.
    struct Conn: Equatable {
        var starting = false
        var ready = false
        var error: String?
    }

    /// CXO pairing as the list and the DM header need it.
    struct PairUi: Equatable {
        var available = false
        var paired: Set<String> = []
        var records: [String: PairRecord] = [:]
        var invites: [String] = []
    }

    /// One list row's live facts.
    struct ThreadSummary: Equatable {
        var last: ChatMessage?
        var unread = 0
    }

    /// Group backlog paging.
    struct HistPage: Equatable {
        var cursor: String?
        var done = false
        var loading = false
        var loaded = false
    }

    struct Note: Equatable, Identifiable {
        let id = UUID()
        let text: String
        let error: Bool
    }

    struct Upload: Equatable {
        var sent: Int64
        var total: Int64
    }

    /// One thread's messages, observed by its own screen only.
    final class Thread: ObservableObject {
        @Published var messages: [ChatMessage]

        init(_ messages: [ChatMessage] = []) {
            self.messages = messages
        }
    }

    struct Outgoing {
        let msgKey: String
        let wire: String
        let network: String
    }

    @Published var conn = Conn()
    @Published var sseUp = false
    @Published var myPk = ""
    @Published var names: [String: String] = [:]
    @Published var profile: SkychatProfileInfo?
    @Published var profileAvailable: Bool?
    @Published var groups: [GroupInfo] = []
    @Published var groupsAvailable: Bool?
    @Published var pair = PairUi()
    @Published var privacy: PrivacyState?
    @Published var presence: [String: PresencePeer] = [:]
    @Published var presenceAvailable: Bool?
    @Published var voiceAvailable = false
    @Published var links: [String: LinkState] = [:]
    @Published var uploads: [String: Upload] = [:]
    @Published var groupUnread: [String: Int] = [:]
    @Published var hist: [String: HistPage] = [:]
    @Published var summaries: [String: ThreadSummary] = [:]
    /// Route for DM sends: `skynet`, `dmsg` or `cxo`.
    @Published var network = "skynet"
    /// The conversation on screen.
    @Published private(set) var openKey: String?
    @Published private(set) var note: Note?
    /// A file written out for the share sheet.
    @Published var share: SharedFile?
    /// Bumped when a group changed in a way its open settings page reloads for.
    @Published private(set) var groupChanged: (gid: String, tick: Int) = ("", 0)

    let store: ChatStore
    let media: ChatMediaCache
    let api: SkychatClient
    private let origin = SkychatOrigin()

    /// skychat's address while it answers.
    var skychatURL: URL? { origin.url }

    /// Whether the open thread's newest message is in view.
    var atBottom = true
    private(set) var threads: [String: Thread] = [:]
    var orphanStatus: [(id: String, status: String)] = []
    var outbox: [String: [Outgoing]] = [:]
    var linkOff = false
    var groupMetaAskedAt: [String: Date] = [:]
    var linkWatch: (pk: String, task: Task<Void, Never>)?

    private weak var app: AppModel?
    private var session: Task<Void, Never>?
    private var wantApp = false
    private var postedUnread = -1
    private var changeTick = 0

    nonisolated static let log = Logger(subsystem: Bundle.main.bundleIdentifier ?? "skywire", category: "chat")

    init(store: ChatStore = ChatStore()) {
        self.store = store
        api = SkychatClient(transport: origin) { try SkychatOrigin.password() }
        media = ChatMediaCache(api: api)
    }

    // MARK: Notes

    func note(_ text: String, error: Bool = false) {
        note = Note(text: text, error: error)
    }

    func noteShown(_ shown: Note) {
        if note == shown { note = nil }
    }

    /// A server error body as it explained itself, or a plain "no reply" for a dropped connection.
    func errorText(_ error: any Error) -> String {
        switch error {
        case let error as SkychatError: error.message
        case is URLError: L10n.text("chat_error_no_reply")
        default: error.localizedDescription
        }
    }

    func signalGroupChanged(_ gid: String) {
        changeTick += 1
        groupChanged = (gid, changeTick)
    }

    // MARK: Lifecycle

    /// Follows the core for as long as the calling task runs (RootView's
    /// `.task(id: app.connected)`): whenever it is connected, skychat is
    /// followed if running, and started once the Chat tab asks for it.
    func follow(_ app: AppModel) async {
        self.app = app
        guard app.connected else {
            stop()
            return
        }
        launch()
        while !Task.isCancelled {
            try? await Task.sleep(for: .seconds(3600))
        }
        stop()
    }

    /// The Chat tab is on screen: start skychat if it is not running.
    func requestApp() {
        wantApp = true
        if !conn.ready && !conn.starting { launch() }
    }

    func retry() {
        conn.error = nil
        launch()
    }

    private func launch() {
        guard let app, app.connected else { return }
        session?.cancel()
        session = Task { await runSession(app) }
    }

    private func stop() {
        session?.cancel()
        session = nil
        linkWatch?.task.cancel()
        linkWatch = nil
        conn = Conn()
        sseUp = false
        origin.set(nil)
    }

    /// One live session: every loop is its child, so cancelling the session stops them all.
    private func runSession(_ app: AppModel) async {
        sseUp = false
        guard let url = await bringUp(app), !Task.isCancelled else { return }
        // The origin alone: request paths start with "/", and Go's router redirects "//status".
        origin.set(URL(string: url.absoluteString.trimmingCharacters(in: CharacterSet(charactersIn: "/"))))
        SkychatOrigin.reset()
        guard let me = try? await api.status(), !me.visorPk.isEmpty else {
            if !Task.isCancelled { conn = Conn(error: L10n.format("chat_error_no_answer", url.absoluteString)) }
            return
        }
        adopt(me.visorPk.lowercased())
        conn = Conn(ready: true)
        await withTaskGroup(of: Void.self) { group in
            group.addTask { await self.syncNames() }
            group.addTask { await self.syncPairs() }
            group.addTask { await self.syncPrivacy() }
            group.addTask { await self.syncGroups() }
            group.addTask { await self.syncProfile() }
            group.addTask { await self.sseLoop() }
            group.addTask { await self.presenceLoop() }
            group.addTask { await self.pairLoop() }
            group.addTask { await self.voiceLoop() }
            group.addTask { await self.focusLoop() }
            group.addTask { await self.unreadLoop() }
        }
    }

    /// skychat's own listener, started when it is not running and the tab asked; nil when it never answered.
    private func bringUp(_ app: AppModel) async -> URL? {
        conn = Conn(starting: true)
        let secret: String
        do {
            secret = try SecretStore.app().password(.skychatPassword)
        } catch {
            conn = Conn(error: error.localizedDescription)
            return nil
        }
        var startError: String?
        let state: AppState
        do {
            let current = try await app.client.app(SkychatProfile.app)
            let running = current.status == AppState.statusRunning || current.status == AppState.statusStarting
            if !running && !wantApp {
                conn = Conn()
                return nil
            }
            if !running {
                do {
                    _ = try await app.client.updateApp(SkychatProfile.app, status: CoreClient.appStart)
                } catch is CancellationError {
                    return nil
                } catch {
                    startError = error.localizedDescription
                }
            }
            state = current
        } catch is CancellationError {
            return nil
        } catch {
            if !app.handle(error), !Task.isCancelled { conn = Conn(error: error.localizedDescription) }
            return nil
        }
        let port = SkychatProfile.listenPort(state.args)
        let url = SkychatProfile.baseURL(port: port)
        let probe = SkychatClient(transport: LoopbackTransport(origin: SkychatProfile.origin(port: port))) { secret }
        var reloadedGate = false
        var last: Int?? = .none
        for _ in 0..<Self.readyAttempts {
            let status = await probe.probe()
            if last != .some(status) {
                last = .some(status)
                ChatPageLog.add("chat: skychat probe on \(port) answered \(status.map(String.init) ?? "nothing")")
            }
            switch status {
            case 200:
                return url
            case 401 where !reloadedGate:
                // The running skychat holds an older password file: write it for the current secret and restart once.
                reloadedGate = true
                do {
                    try PasswordFile.ensure(at: app.paths.skychatPasswordFile, password: secret)
                    _ = try await app.client.updateApp(SkychatProfile.app, status: CoreClient.appStop)
                    _ = try await app.client.updateApp(SkychatProfile.app, status: CoreClient.appStart)
                } catch is CancellationError {
                    return nil
                } catch {
                    startError = error.localizedDescription
                }
            default:
                break
            }
            do {
                try await Task.sleep(for: Self.readyInterval)
            } catch {
                return nil
            }
        }
        conn = Conn(error: startError ?? L10n.format("chat_error_no_answer", url.absoluteString))
        return nil
    }

    /// Opens this identity's local store and loads its threads.
    private func adopt(_ pk: String) {
        myPk = pk
        let fresh = store.open(owner: pk)
        if !fresh && !threads.isEmpty { return }
        threads = [:]
        summaries = [:]
        for key in store.threadKeys() where key != pk {
            // A message held for a link when the app died will never go by itself.
            let list = store.loadThread(key).map { m -> ChatMessage in
                var m = m
                if m.status == "queued" { m.status = "failed" }
                return m
            }
            threads[key] = Thread(list)
        }
        if threads[Skychat.savedKey] == nil { threads[Skychat.savedKey] = Thread() }
        // The page's old bug: our own mirror filed as a conversation with ourselves.
        store.update { $0.recipients.removeAll { $0 == pk } }
        recomputeAll()
        ChatLegacyImport.maybeRun(self)
    }

    // MARK: Threads

    func thread(_ key: String) -> Thread {
        if let thread = threads[key] { return thread }
        let thread = Thread()
        threads[key] = thread
        return thread
    }

    func messages(_ key: String) -> [ChatMessage] { threads[key]?.messages ?? [] }

    var threadKeys: [String] { Array(threads.keys) }

    func isGroupKey(_ key: String) -> Bool { Skychat.isGroupKey(key) }

    /// Changes one thread; DM and Saved threads are written to disk.
    func mutate(_ key: String, _ change: ([ChatMessage]) -> [ChatMessage]) {
        let box = thread(key)
        let next = change(box.messages)
        if next == box.messages { return }
        box.messages = next
        if !isGroupKey(key) { store.saveThread(key, next) }
        recompute(key)
    }

    func updateMessage(_ key: String, _ msgKey: String, _ change: (inout ChatMessage) -> Void) {
        mutate(key) { list in
            guard let i = list.firstIndex(where: { $0.key == msgKey }) else { return list }
            var copy = list
            change(&copy[i])
            return copy
        }
    }

    func findMessage(_ key: String, _ msgKey: String) -> ChatMessage? {
        threads[key]?.messages.first { $0.key == msgKey }
    }

    func appendMessage(_ key: String, _ m: ChatMessage) {
        mutate(key) { list in
            guard let last = list.last, last.ts > m.ts else { return list + [m] }
            var copy = list
            copy.insert(m, at: copy.firstIndex { $0.ts > m.ts } ?? copy.count)
            return copy
        }
    }

    func removeThread(_ key: String) {
        threads[key] = nil
        summaries[key] = nil
    }

    func recompute(_ key: String) {
        let list = threads[key]?.messages ?? []
        let unread: Int
        if isGroupKey(key) {
            unread = groupUnread[key] ?? 0
        } else {
            let seen = store.state.readUpTo[key] ?? 0
            unread = list.filter { !$0.mine && $0.ts > seen }.count
        }
        let summary = ThreadSummary(last: list.last, unread: unread)
        if summaries[key] != summary { summaries[key] = summary }
    }

    func recomputeAll() {
        for key in threads.keys { recompute(key) }
    }

    /// The thread was seen up to its newest message.
    func markRead(_ key: String) {
        if isGroupKey(key) {
            if (groupUnread[key] ?? 0) != 0 { groupUnread[key] = nil }
            recompute(key)
            return
        }
        guard let newest = threads[key]?.messages.last?.ts, (store.state.readUpTo[key] ?? 0) < newest else { return }
        store.update { $0.readUpTo[key] = newest }
        recompute(key)
    }

    /// Adds a DM row for `pk` if it has none.
    func ensureRecipient(_ pk: String) {
        let p = pk.lowercased()
        guard Skychat.isPk(p), p != myPk else { return }
        if !store.state.recipients.contains(p) { store.update { $0.recipients.append(p) } }
        if threads[p] == nil {
            threads[p] = Thread()
            recompute(p)
        }
    }

    var totalUnread: Int {
        summaries.filter { Skychat.isPk($0.key) }.values.reduce(0) { $0 + $1.unread }
    }

    // MARK: The open conversation

    /// The screen shows `key` (or no thread at all with nil).
    func setOpen(_ key: String?) {
        guard key != openKey else { return }
        openKey = key
        atBottom = true
        watchLink(key.flatMap { Skychat.isPk($0) ? $0 : nil })
        guard let key else { return }
        markRead(key)
        if key == Skychat.savedKey { return }
        Task {
            if Skychat.isPk(key) {
                await loadDmHistory(key)
                await sendReadReceipts(key)
            } else {
                await openGroup(key)
            }
        }
    }

    func setAtBottom(_ key: String, _ bottom: Bool) {
        guard openKey == key else { return }
        let was = atBottom
        atBottom = bottom
        if bottom && !was {
            markRead(key)
            if Skychat.isPk(key) { Task { await sendReadReceipts(key) } }
        }
    }

    func sendReadReceipts(_ pk: String) async {
        let pending = messages(pk).filter { !$0.mine && !$0.viaPair && !$0.wireId.isEmpty && !$0.readSent }
        guard !pending.isEmpty else { return }
        let ids = Set(pending.map(\.wireId))
        mutate(pk) { list in
            list.map { m in
                guard ids.contains(m.wireId), !m.mine else { return m }
                var m = m
                m.readSent = true
                return m
            }
        }
        try? await api.readReceipt(pk: pk, ids: Array(ids))
    }

    // MARK: DM history

    func loadDmHistory(_ pk: String) async {
        guard let records = try? await api.history(peer: pk, limit: 100) else { return }
        mergeHistory(pk, records)
    }

    /// After every (re)connect: the newest stored DMs of every peer, so nothing missed while away stays missing.
    func catchUp() async {
        if let peers = try? await api.historyPeers() {
            for pk in peers { ensureRecipient(pk) }
        }
        guard let records = try? await api.recentHistory(limit: Self.catchUp) else { return }
        for (pk, list) in Dictionary(grouping: records, by: { $0.peer.lowercased() }) where Skychat.isPk(pk) {
            ensureRecipient(pk)
            mergeHistory(pk, list)
        }
        if !store.state.initialized {
            // First run: what is already stored is history, not news.
            var now: [String: Int64] = [:]
            for (key, box) in threads {
                if let last = box.messages.last { now[key] = last.ts }
            }
            store.update { state in
                state.initialized = true
                state.readUpTo = now.merging(state.readUpTo) { _, kept in kept }
            }
            recomputeAll()
        }
    }

    func mergeHistory(_ pk: String, _ records: [HistoryRecord]) {
        guard !records.isEmpty else { return }
        mutate(pk) { current in
            var out = current
            var changed = false
            for record in records {
                guard let m = fromHistory(pk, record) else { continue }
                if let i = out.firstIndex(where: { Self.same($0, m) }) {
                    let existing = out[i]
                    if var f = existing.file, let nf = m.file, (f.url.isEmpty && !nf.url.isEmpty) || f.id.isEmpty {
                        if f.url.isEmpty { f.url = nf.url }
                        if f.id.isEmpty { f.id = nf.id }
                        out[i].file = f
                        changed = true
                    }
                    if existing.wireId.isEmpty && !m.wireId.isEmpty {
                        out[i].wireId = m.wireId
                        if existing.mine && Skychat.statusRank(existing.status) < Skychat.statusRank("sent") { out[i].status = "sent" }
                        changed = true
                    }
                    continue
                }
                out.append(m)
                changed = true
            }
            return changed ? out.sorted { $0.ts < $1.ts } : current
        }
    }

    private static func same(_ a: ChatMessage, _ b: ChatMessage) -> Bool {
        if !a.dmWireId.isEmpty && !b.dmWireId.isEmpty { return a.dmWireId == b.dmWireId }
        return a.mine == b.mine && a.text == b.text && abs(a.ts - b.ts) < sameWindowMs
    }

    private func fromHistory(_ pk: String, _ r: HistoryRecord) -> ChatMessage? {
        guard let ts = Skychat.parseTime(r.timestamp) else { return nil }
        let callOut = r.outgoing && r.text.hasPrefix("📞")
        let from = r.outgoing || callOut ? Skychat.me : (r.from.isEmpty ? r.peer : r.from).lowercased()
        let file = r.fileName.isEmpty ? nil : ChatFile(
            id: r.fileId, name: r.fileName, size: r.fileSize, status: r.fileStatus, url: r.fileUrl,
            from: r.outgoing ? "" : from
        )
        let id = r.id.isEmpty ? r.fileId : r.id
        return ChatMessage(
            key: "h:" + (id.isEmpty ? "\(ts):\(r.text.hashValue)" : id),
            ts: ts, from: from, text: r.text, file: file,
            reply: Skychat.replyOf(sender: r.replyToSender, ts: r.replyToTs, preview: r.replyToPreview),
            forwarded: r.forwarded, forwardFrom: r.forwardFrom, wireId: id,
            // A stored own message reached the wire; the server keeps no receipts.
            status: r.outgoing ? "sent" : ""
        )
    }

    // MARK: Event stream

    private func sseLoop() async {
        while !Task.isCancelled {
            do {
                let events = try await api.events()
                sseUp = true
                ChatPageLog.add("chat: event stream open")
                // The replay ring cannot be trusted to hold everything; REST is.
                Task { await resync() }
                for try await message in events {
                    handle(message)
                }
            } catch is CancellationError {
                break
            } catch {
                Self.log.debug("sse ended: \(error.localizedDescription, privacy: .public)")
            }
            sseUp = false
            ChatPageLog.add("chat: event stream closed")
            try? await Task.sleep(for: Self.sseRetry)
        }
        sseUp = false
    }

    private func resync() async {
        await catchUp()
        await syncGroups()
        await syncPairs()
        if let key = openKey, isGroupKey(key) { await refreshGroupHead(key) }
    }

    // MARK: Periodic work

    private func presenceLoop() async {
        while !Task.isCancelled {
            let s = store.state
            var seen = Set<String>()
            let pks = (s.recipients + s.contacts).filter { Skychat.isPk($0) && seen.insert($0).inserted }.prefix(64)
            if !pks.isEmpty && presenceAvailable != false {
                do {
                    presence = try await api.presence(Array(pks))
                    presenceAvailable = true
                } catch let error as SkychatError where error.status == 404 || error.status == 503 {
                    presenceAvailable = false
                    presence = [:]
                } catch {}
            }
            try? await Task.sleep(for: Self.presenceInterval)
        }
    }

    private func pairLoop() async {
        while !Task.isCancelled {
            try? await Task.sleep(for: Self.pairInterval)
            if pair.available { await pollPairEpochs() }
        }
    }

    private func voiceLoop() async {
        while !Task.isCancelled {
            voiceAvailable = await api.voiceAvailable()
            try? await Task.sleep(for: Self.voiceInterval)
        }
    }

    /// The lease that keeps skychat from notifying about the thread on screen (osnotify.go).
    private func focusLoop() async {
        var sent: String?
        var sentAt = Date.distantPast
        while !Task.isCancelled {
            let key = UIApplication.shared.applicationState == .active ? (openKey ?? "") : ""
            if key != sent || (!key.isEmpty && Date().timeIntervalSince(sentAt) >= Self.focusRefresh) {
                if (try? await api.notifyFocus(key)) != nil {
                    sent = key
                    sentAt = Date()
                }
            }
            try? await Task.sleep(for: Self.focusInterval)
        }
    }

    /// The hub card's badge reads this total back from skychat.
    private func unreadLoop() async {
        while !Task.isCancelled {
            let total = totalUnread
            if total != postedUnread, (try? await api.reportUnread(total)) != nil { postedUnread = total }
            try? await Task.sleep(for: Self.unreadInterval)
        }
    }

    // MARK: Syncs

    func syncNames() async {
        guard let map = try? await api.contactNames() else { return }
        names = Dictionary(map.map { ($0.key.lowercased(), $0.value) }, uniquingKeysWith: { a, _ in a })
    }

    func syncPrivacy() async {
        do {
            applyPrivacy(try await api.privacy())
        } catch {
            privacy = nil
        }
    }

    func applyPrivacy(_ p: PrivacyState) {
        privacy = PrivacyState(approval: p.approval, blocked: p.blocked.map { $0.lowercased() }, requests: p.requests.map { $0.lowercased() })
        for pk in p.requests { ensureRecipient(pk) }
    }

    func syncGroups() async {
        for attempt in 0...Self.retries {
            do {
                let list = try await api.groups()
                groups = list
                groupsAvailable = true
                for g in list where threads[g.id] == nil { threads[g.id] = Thread() }
                return
            } catch let error as SkychatError where error.status == 404 {
                groupsAvailable = false
                return
            } catch is CancellationError {
                return
            } catch {}
            if attempt == Self.retries { return }
            try? await Task.sleep(for: .milliseconds(min(5000, 1000 * (attempt + 1))))
        }
    }

    func syncProfile() async {
        for attempt in 0...Self.retries {
            do {
                profile = try await api.profile()
                profileAvailable = true
                return
            } catch let error as SkychatError where error.status == 404 {
                profileAvailable = false
                return
            } catch is CancellationError {
                return
            } catch {}
            if attempt == Self.retries { return }
            try? await Task.sleep(for: .milliseconds(min(5000, 1000 * (attempt + 1))))
        }
    }

    func syncPairs() async {
        let records: [PairRecord]
        do {
            records = try await api.pairs()
        } catch is SkychatError {
            pair = PairUi()
            return
        } catch {
            return
        }
        let live = records.filter { !$0.peerPk.isEmpty && $0.status != "revoked" }
        for r in live {
            ensureRecipient(r.peerPk)
            if r.status == "active" { contactFromPair(r.peerPk.lowercased(), since: r.establishedAt) }
        }
        pair.available = true
        pair.paired = Set(live.map { $0.peerPk.lowercased() })
        pair.records = Dictionary(records.map { ($0.peerPk.lowercased(), $0) }, uniquingKeysWith: { _, b in b })
        await syncInvites()
    }

    func syncInvites() async {
        let invites = (try? await api.pairInvites()) ?? []
        var seen = Set<String>()
        pair.invites = invites.map { $0.lowercased() }.filter { seen.insert($0).inserted }
    }

    private func pollPairEpochs() async {
        guard let records = try? await api.pairs() else { return }
        for r in records where r.status == "active" { contactFromPair(r.peerPk.lowercased(), since: r.establishedAt) }
        pair.records = Dictionary(records.map { ($0.peerPk.lowercased(), $0) }, uniquingKeysWith: { _, b in b })
    }

    /// An accepted pairing adds its peer to the address book once per pairing.
    func contactFromPair(_ pk: String, since: String) {
        let mark = "\(pk) \(since)"
        guard !store.state.pairContacts.contains(mark) else { return }
        store.update { $0.pairContacts.insert(mark) }
        addContact(pk)
    }

    // MARK: Names

    /// Nickname, else the short key: the label every list and header shows.
    func displayName(_ pk: String) -> String {
        let p = pk.lowercased()
        if p == myPk && !p.isEmpty {
            if let name = profile?.name, !name.isEmpty { return name }
            return Skychat.abbrevPk(p)
        }
        if let name = names[p], !name.isEmpty { return name }
        return Skychat.abbrevPk(p)
    }

    func groupInfo(_ gid: String) -> GroupInfo? { groups.first { $0.id == gid } }

    func isContact(_ pk: String) -> Bool { store.state.contacts.contains(pk.lowercased()) }

    @discardableResult
    func addContact(_ pk: String, name: String? = nil, avatar: Data? = nil) -> Bool {
        let p = pk.lowercased()
        guard Skychat.isPk(p) else { return false }
        if let name, !name.trimmingCharacters(in: .whitespaces).isEmpty, (names[p] ?? "").isEmpty { setName(p, name) }
        if let avatar, !store.hasAvatar(p) { store.setAvatar(p, avatar) }
        if !store.state.contacts.contains(p) { store.update { $0.contacts.append(p) } }
        return true
    }

    func setName(_ pk: String, _ name: String) {
        let p = pk.lowercased()
        let clean = String(name.split(whereSeparator: { $0.isWhitespace }).joined(separator: " ").prefix(Skychat.nameMax))
        if clean.isEmpty { names[p] = nil } else { names[p] = clean }
        Task { try? await api.setContactName(pk: p, name: clean) }
    }

    static let readyAttempts = 30
    static let readyInterval: Duration = .milliseconds(500)
    static let sseRetry: Duration = .seconds(2)
    static let presenceInterval: Duration = .seconds(60)
    static let pairInterval: Duration = .seconds(15)
    static let voiceInterval: Duration = .seconds(15)
    static let focusInterval: Duration = .seconds(2)
    static let focusRefresh: TimeInterval = 5
    static let unreadInterval: Duration = .seconds(2)
    static let retries = 15
    static let catchUp = 300
    static let sameWindowMs: Int64 = 120_000
}

/// skychat's origin once it answers, nil before: requests made without one
/// fail as a dropped connection, as Android's `baseUrl == null` does.
final class SkychatOrigin: RequestTransport, @unchecked Sendable {
    private let lock = OSAllocatedUnfairLock<LoopbackTransport?>(initialState: nil)
    private static let secret = OSAllocatedUnfairLock<String?>(initialState: nil)

    func set(_ url: URL?) {
        lock.withLock { $0 = url.map { LoopbackTransport(origin: $0) } }
    }

    var url: URL? { lock.withLock { $0?.origin } }

    private var current: LoopbackTransport {
        get throws {
            guard let transport = lock.withLock({ $0 }) else { throw URLError(.cannotConnectToHost) }
            return transport
        }
    }

    func send(_ request: HTTPRequest) async throws -> HTTPResponse { try await current.send(request) }

    func stream(_ request: HTTPRequest) async throws -> HTTPStream { try await current.stream(request) }

    func upload(_ request: HTTPRequest, producing: @escaping @Sendable (OutputStream) -> Void) async throws -> HTTPStream {
        try await current.upload(request, producing: producing)
    }

    /// The gate's secret, read from the Keychain once per session.
    static func password() throws -> String {
        if let cached = secret.withLock({ $0 }) { return cached }
        let value = try SecretStore.app().password(.skychatPassword)
        secret.withLock { $0 = value }
        return value
    }

    static func reset() {
        secret.withLock { $0 = nil }
    }
}

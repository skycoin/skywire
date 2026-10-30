import CoreClient
import Foundation

/// Notification preferences; mutes are thread keys (peer key, group id).
struct NotifPrefs: Codable, Equatable {
    var enabled = true
    var preview = true
    var muted: Set<String> = []

    init() {}

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? true
        preview = try c.decodeIfPresent(Bool.self, forKey: .preview) ?? true
        muted = try c.decodeIfPresent(Set<String>.self, forKey: .muted) ?? []
    }

    func isMuted(_ key: String) -> Bool { muted.contains(key) || muted.contains(key.lowercased()) }
}

/// The chat state that lives on this phone only, what the web page kept in
/// localStorage (Android: chat/ChatLocalStore.kt). Owned by one visor key: a
/// different identity starts empty.
struct LocalChatState: Codable, Equatable {
    var owner = ""
    /// DM peers shown in the list even without stored history.
    var recipients: [String] = []
    /// Saved contacts (lower-case keys); names live on the server.
    var contacts: [String] = []
    /// Thread key to the newest message time (ms) the user has seen.
    var readUpTo: [String: Int64] = [:]
    var tab = "all"
    /// `dm:<pk>` or `grp:<gid>`.
    var pinned: String?
    /// Group messages deleted for me, as `<gid>:<ts_nano>`.
    var hiddenGroup: Set<String> = []
    var mediaMode = "mic"
    var videoFacingFront = true
    var videoSquare = false
    var speed: Float = 1
    /// `<pk> <established_at>` pairings already turned into a contact.
    var pairContacts: Set<String> = []
    /// Set once the first catch-up ran, so a fresh install does not show the whole past as unread.
    var initialized = false
    var legacyImported = false

    init() {}

    init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        owner = try c.decodeIfPresent(String.self, forKey: .owner) ?? ""
        recipients = try c.decodeIfPresent([String].self, forKey: .recipients) ?? []
        contacts = try c.decodeIfPresent([String].self, forKey: .contacts) ?? []
        readUpTo = try c.decodeIfPresent([String: Int64].self, forKey: .readUpTo) ?? [:]
        tab = try c.decodeIfPresent(String.self, forKey: .tab) ?? "all"
        pinned = try c.decodeIfPresent(String.self, forKey: .pinned)
        hiddenGroup = try c.decodeIfPresent(Set<String>.self, forKey: .hiddenGroup) ?? []
        mediaMode = try c.decodeIfPresent(String.self, forKey: .mediaMode) ?? "mic"
        videoFacingFront = try c.decodeIfPresent(Bool.self, forKey: .videoFacingFront) ?? true
        videoSquare = try c.decodeIfPresent(Bool.self, forKey: .videoSquare) ?? false
        speed = try c.decodeIfPresent(Float.self, forKey: .speed) ?? 1
        pairContacts = try c.decodeIfPresent(Set<String>.self, forKey: .pairContacts) ?? []
        initialized = try c.decodeIfPresent(Bool.self, forKey: .initialized) ?? false
        legacyImported = try c.decodeIfPresent(Bool.self, forKey: .legacyImported) ?? false
    }
}

/// File-backed store under Application Support, written off the main thread.
/// DM threads and Saved Messages are one JSON file each; contact pictures are
/// separate files so the state document stays small.
@MainActor
final class ChatStore: ObservableObject {
    @Published private(set) var state = LocalChatState()
    @Published private(set) var notif: NotifPrefs
    /// Bumped on every contact picture change, so avatars reload.
    @Published private(set) var avatarVersion = 0

    let root: URL
    private(set) var dir: URL?
    private let writer = DispatchQueue(label: "chat-store", qos: .utility)

    nonisolated static let maxThread = 1000

    init(root: URL = ChatStore.defaultRoot) {
        self.root = root
        notif = (try? JSONDecoder().decode(NotifPrefs.self, from: Data(contentsOf: root.appendingPathComponent("notifications.json")))) ?? NotifPrefs()
    }

    nonisolated static var defaultRoot: URL {
        FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask)[0].appendingPathComponent("skychat-native", isDirectory: true)
    }

    /// Where our own uploads keep their copy (relative paths in ChatFile.localPath).
    nonisolated static var uploads: URL {
        FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask)[0].appendingPathComponent("chat-up", isDirectory: true)
    }

    nonisolated static func uploadURL(_ relative: String) -> URL? {
        relative.isEmpty ? nil : uploads.appendingPathComponent(relative)
    }

    nonisolated static func hasUpload(_ relative: String) -> Bool {
        uploadURL(relative).map { FileManager.default.fileExists(atPath: $0.path) } ?? false
    }

    var ready: Bool { dir != nil }

    /// Loads the store for `owner`, the visor's key. False when it was already open for it.
    @discardableResult
    func open(owner: String) -> Bool {
        let pk = owner.lowercased()
        if state.owner == pk && dir != nil { return false }
        let folder = root.appendingPathComponent(pk, isDirectory: true)
        try? FileManager.default.createDirectory(at: folder.appendingPathComponent("threads"), withIntermediateDirectories: true)
        dir = folder
        var loaded = (try? JSONDecoder().decode(LocalChatState.self, from: Data(contentsOf: folder.appendingPathComponent("state.json")))) ?? LocalChatState()
        loaded.owner = pk
        state = loaded
        return true
    }

    func update(_ change: (inout LocalChatState) -> Void) {
        var next = state
        change(&next)
        guard next != state else { return }
        state = next
        guard let dir else { return }
        let target = dir.appendingPathComponent("state.json")
        let snapshot = next
        writer.async { Self.write(snapshot, to: target) }
    }

    func updateNotif(_ change: (inout NotifPrefs) -> Void) {
        var next = notif
        change(&next)
        guard next != notif else { return }
        notif = next
        let target = root.appendingPathComponent("notifications.json")
        let snapshot = next
        writer.async {
            try? FileManager.default.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
            Self.write(snapshot, to: target)
        }
    }

    // MARK: Threads

    private func threadFile(_ key: String) -> URL? {
        let safe = key.lowercased().filter { $0.isLetter || $0.isNumber || $0 == "-" }
        return dir?.appendingPathComponent("threads").appendingPathComponent(safe + ".json")
    }

    func threadKeys() -> [String] {
        guard let dir, let names = try? FileManager.default.contentsOfDirectory(atPath: dir.appendingPathComponent("threads").path) else { return [] }
        return names.filter { $0.hasSuffix(".json") }.map { String($0.dropLast(5)) }
    }

    func loadThread(_ key: String) -> [ChatMessage] {
        guard let file = threadFile(key), let data = try? Data(contentsOf: file) else { return [] }
        return (try? JSONDecoder().decode([ChatMessage].self, from: data)) ?? []
    }

    func saveThread(_ key: String, _ messages: [ChatMessage]) {
        guard let file = threadFile(key) else { return }
        let snapshot = Array(messages.suffix(Self.maxThread))
        writer.async { Self.write(snapshot, to: file) }
    }

    func deleteThread(_ key: String) {
        guard let file = threadFile(key) else { return }
        writer.async { try? FileManager.default.removeItem(at: file) }
    }

    // MARK: Pictures

    private func avatarFile(_ key: String) -> URL? {
        guard let dir else { return nil }
        let folder = dir.appendingPathComponent("avatars", isDirectory: true)
        try? FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        return folder.appendingPathComponent(key.lowercased().filter { $0.isLetter || $0.isNumber })
    }

    /// A locally chosen picture for a peer, as image bytes.
    func avatar(_ key: String) -> Data? {
        avatarFile(key).flatMap { try? Data(contentsOf: $0) }
    }

    func hasAvatar(_ key: String) -> Bool {
        avatarFile(key).map { FileManager.default.fileExists(atPath: $0.path) } ?? false
    }

    func setAvatar(_ key: String, _ bytes: Data?) {
        guard let file = avatarFile(key) else { return }
        if let bytes {
            try? bytes.write(to: file, options: .atomic)
        } else {
            try? FileManager.default.removeItem(at: file)
        }
        avatarVersion += 1
    }

    /// Waits for every queued write, for tests and an identity reset.
    func flush() {
        writer.sync {}
    }

    nonisolated private static func write<T: Encodable>(_ value: T, to target: URL) {
        guard let data = try? JSONEncoder().encode(value) else { return }
        try? data.write(to: target, options: .atomic)
    }
}

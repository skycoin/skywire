import CoreClient
import Foundation

/// The identity with its channels and groups, as one zip (Android: ConfigManager's writeBackup and
/// restoreBackup, same entries, so a backup moves between the two apps). A key alone brings the
/// identity back but not `local/skychat`: the group store, its sealed keys, the feeds and their
/// history. Both run with the core stopped, because those are open databases while it runs.
enum Backup {
    static let configEntry = "skywire-config.json"
    static let groupsDir = "skychat"
    static let fileName = "skywire-backup.zip"

    /// Writes the config and the group state to a zip in the temporary directory.
    static func write(config: String, paths: CorePaths) throws -> URL {
        var entries = [(name: configEntry, file: URL?.none, data: Data(config.utf8))]
        let dir = paths.localDir.appendingPathComponent(groupsDir, isDirectory: true)
        let files = FileManager.default
        // Both resolved: the container is under /var, which links to /private/var.
        let base = dir.resolvingSymlinksInPath().path + "/"
        if let walk = files.enumerator(at: dir, includingPropertiesForKeys: [.isRegularFileKey]) {
            for case let url as URL in walk where (try? url.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true {
                let full = url.resolvingSymlinksInPath().path
                guard full.hasPrefix(base) else { continue }
                entries.append((name: "\(groupsDir)/\(full.dropFirst(base.count))", file: url, data: Data()))
            }
        }
        let out = files.temporaryDirectory.appendingPathComponent(fileName)
        try? files.removeItem(at: out)
        guard files.createFile(atPath: out.path, contents: nil) else { throw unreadable }
        let handle = try FileHandle(forWritingTo: out)
        defer { try? handle.close() }
        var zip = ZipWriter(handle: handle)
        for entry in entries {
            try zip.add(name: entry.name, data: try entry.file.map { try Data(contentsOf: $0) } ?? entry.data)
        }
        try zip.finish()
        return out
    }

    /// The secret key a backup holds, or nil when `url` is not one.
    static func secretKey(in url: URL) -> String? {
        guard let entries = try? ZipReader.entries(try Data(contentsOf: url)) else { return nil }
        return entries.first { $0.name == configEntry }.flatMap { secretKey(ofConfig: $0.data) }
    }

    /// Installs a backup's identity, then its group state, and returns the public key.
    static func restore(from url: URL, paths: CorePaths, vault: ConfigVault) async throws -> String {
        let files = FileManager.default
        let staged = files.temporaryDirectory.appendingPathComponent("restore", isDirectory: true)
        try? files.removeItem(at: staged)
        try files.createDirectory(at: staged, withIntermediateDirectories: true)
        defer { try? files.removeItem(at: staged) }
        var secretKey: String?
        for entry in try ZipReader.entries(try Data(contentsOf: url)) {
            if entry.name == configEntry {
                secretKey = Self.secretKey(ofConfig: entry.data)
            } else if entry.name.hasPrefix("\(groupsDir)/"), !entry.name.hasSuffix("/") {
                // An entry naming ../ must not land outside the staging dir.
                guard !entry.name.split(separator: "/").contains("..") else { throw unreadable }
                let target = staged.appendingPathComponent(entry.name)
                try files.createDirectory(at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
                try entry.data.write(to: target)
            }
        }
        guard let secretKey else { throw unreadable }
        let pk = try await Identity.replace(secretKey: secretKey, paths: paths, vault: vault)
        let groups = staged.appendingPathComponent(groupsDir, isDirectory: true)
        if files.fileExists(atPath: groups.path) {
            let target = paths.localDir.appendingPathComponent(groupsDir, isDirectory: true)
            try? files.removeItem(at: target)
            try files.copyItem(at: groups, to: target)
        }
        return pk
    }

    static var unreadable: Identity.IdentityError { Identity.IdentityError(message: L10n.text("settings_backup_unreadable")) }

    private static func secretKey(ofConfig data: Data) -> String? {
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
              let sk = json["sk"] as? String, !sk.trimmingCharacters(in: .whitespaces).isEmpty
        else { return nil }
        return sk
    }
}

/// A zip of stored entries, written as it goes (APPNOTE.TXT 4.3, no ZIP64: a backup is far below 4 GiB).
struct ZipWriter {
    private let handle: FileHandle
    private var central = Data()
    private var count: UInt16 = 0
    private var offset: UInt32 = 0

    init(handle: FileHandle) {
        self.handle = handle
    }

    mutating func add(name: String, data: Data) throws {
        let nameBytes = Data(name.utf8)
        let crc = CRC32.of(data)
        let size = UInt32(data.count)
        var local = Data()
        local.le32(0x0403_4B50)
        local.le16(20) // version needed
        local.le16(0x0800) // UTF-8 names
        local.le16(0) // stored
        local.le16(0)
        local.le16(0x21) // 1980-01-01
        local.le32(crc)
        local.le32(size)
        local.le32(size)
        local.le16(UInt16(nameBytes.count))
        local.le16(0)
        local.append(nameBytes)
        try handle.write(contentsOf: local)
        try handle.write(contentsOf: data)

        central.le32(0x0201_4B50)
        central.le16(20) // made by
        central.le16(20)
        central.le16(0x0800)
        central.le16(0)
        central.le16(0)
        central.le16(0x21)
        central.le32(crc)
        central.le32(size)
        central.le32(size)
        central.le16(UInt16(nameBytes.count))
        central.le16(0)
        central.le16(0)
        central.le16(0)
        central.le16(0)
        central.le32(0)
        central.le32(offset)
        central.append(nameBytes)
        offset += UInt32(local.count) + size
        count += 1
    }

    mutating func finish() throws {
        var end = Data()
        end.le32(0x0605_4B50)
        end.le16(0)
        end.le16(0)
        end.le16(count)
        end.le16(count)
        end.le32(UInt32(central.count))
        end.le32(offset)
        end.le16(0)
        try handle.write(contentsOf: central)
        try handle.write(contentsOf: end)
    }
}

/// Reads a zip's entries through its central directory: stored, or deflated as Java's
/// ZipOutputStream writes them (sizes in a data descriptor, so only the directory has them).
enum ZipReader {
    struct Entry {
        let name: String
        let data: Data
    }

    struct Malformed: Error {}

    static func entries(_ zip: Data) throws -> [Entry] {
        let bytes = [UInt8](zip)
        guard bytes.count >= 22 else { throw Malformed() }
        var end = bytes.count - 22
        while end >= 0 && bytes.u32(end) != 0x0605_4B50 { end -= 1 }
        guard end >= 0 else { throw Malformed() }
        let total = Int(bytes.u16(end + 10))
        var at = Int(bytes.u32(end + 16))
        var out: [Entry] = []
        for _ in 0..<total {
            guard at + 46 <= bytes.count, bytes.u32(at) == 0x0201_4B50 else { throw Malformed() }
            let method = bytes.u16(at + 10)
            let crc = bytes.u32(at + 16)
            let packed = Int(bytes.u32(at + 20))
            let size = Int(bytes.u32(at + 24))
            let nameLength = Int(bytes.u16(at + 28))
            let skip = nameLength + Int(bytes.u16(at + 30)) + Int(bytes.u16(at + 32))
            let local = Int(bytes.u32(at + 42))
            guard at + 46 + nameLength <= bytes.count, local + 30 <= bytes.count, bytes.u32(local) == 0x0403_4B50 else { throw Malformed() }
            let name = String(decoding: bytes[(at + 46)..<(at + 46 + nameLength)], as: UTF8.self)
            let start = local + 30 + Int(bytes.u16(local + 26)) + Int(bytes.u16(local + 28))
            guard start + packed <= bytes.count else { throw Malformed() }
            let raw = Data(bytes[start..<(start + packed)])
            let data: Data
            switch method {
            case 0: data = raw
            // Apple's zlib is raw DEFLATE, what a zip entry holds (no zlib header).
            case 8: data = packed == 0 ? Data() : try (raw as NSData).decompressed(using: .zlib) as Data
            default: throw Malformed()
            }
            guard data.count == size, CRC32.of(data) == crc else { throw Malformed() }
            out.append(Entry(name: name, data: data))
            at += 46 + skip
        }
        return out
    }
}

/// The zip checksum (ISO 3309 CRC-32, reflected, polynomial 0xEDB88320).
enum CRC32 {
    private static let table: [UInt32] = (0..<256).map { n in
        (0..<8).reduce(UInt32(n)) { c, _ in c & 1 == 1 ? 0xEDB8_8320 ^ (c >> 1) : c >> 1 }
    }

    static func of(_ data: Data) -> UInt32 {
        data.withUnsafeBytes { buffer in
            var crc: UInt32 = 0xFFFF_FFFF
            for byte in buffer {
                crc = table[Int((crc ^ UInt32(byte)) & 0xFF)] ^ (crc >> 8)
            }
            return crc ^ 0xFFFF_FFFF
        }
    }
}

private extension Data {
    mutating func le16(_ v: UInt16) {
        append(UInt8(v & 0xFF))
        append(UInt8(v >> 8))
    }

    mutating func le32(_ v: UInt32) {
        le16(UInt16(v & 0xFFFF))
        le16(UInt16(v >> 16))
    }
}

private extension [UInt8] {
    func u16(_ i: Int) -> UInt16 { UInt16(self[i]) | UInt16(self[i + 1]) << 8 }
    func u32(_ i: Int) -> UInt32 { UInt32(u16(i)) | UInt32(u16(i + 2)) << 16 }
}

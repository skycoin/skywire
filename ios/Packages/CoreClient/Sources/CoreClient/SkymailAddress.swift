import Foundation

/// Skymail addresses: `<anything>@<label>.dmsg` or `.skynet`, where the label is
/// the visor's public key in lowercase unpadded base32 (pkg/cipher/dnslabel.go),
/// 53 characters for 33 bytes. Any local part reaches the same mailbox.
/// The port of Android's core/SkymailAddress.kt.
public enum SkymailAddress {
    public static let dmsg = ".dmsg"
    public static let skynet = ".skynet"
    static let labelLength = 53
    static let pkHexLength = 66
    static let alphabet = Array("abcdefghijklmnopqrstuvwxyz234567")

    /// The DNS label of a hex public key, or nil when it is not one.
    public static func label(_ pkHex: String) -> String? {
        let hex = pkHex.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard hex.count == pkHexLength, let bytes = bytes(hex) else { return nil }
        var out = ""
        var buffer = 0
        var bits = 0
        for byte in bytes {
            buffer = (buffer << 8) | Int(byte)
            bits += 8
            while bits >= 5 {
                out.append(alphabet[(buffer >> (bits - 5)) & 0x1f])
                bits -= 5
            }
            buffer &= (1 << bits) - 1
        }
        if bits > 0 {
            out.append(alphabet[(buffer << (5 - bits)) & 0x1f])
        }
        return out
    }

    /// The hex public key a label names, or nil when it is not one.
    public static func pk(ofLabel label: String) -> String? {
        let text = label.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard text.count == labelLength else { return nil }
        var out: [UInt8] = []
        var buffer = 0
        var bits = 0
        for character in text {
            guard let value = alphabet.firstIndex(of: character) else { return nil }
            buffer = (buffer << 5) | value
            bits += 5
            if bits >= 8 {
                out.append(UInt8((buffer >> (bits - 8)) & 0xff))
                bits -= 8
            }
            buffer &= (1 << bits) - 1
        }
        guard out.count == pkHexLength / 2 else { return nil }
        return out.map { String(format: "%02x", $0) }.joined()
    }

    /// The mailbox address of a hex public key, over dmsg by default.
    public static func of(_ pkHex: String, local: String = "mail", suffix: String = dmsg) -> String? {
        label(pkHex).map { "\(local)@\($0)\(suffix)" }
    }

    /// The public key an address delivers to, or nil when it is not a Skymail address.
    public static func pk(ofAddress address: String) -> String? {
        let trimmed = address.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let at = trimmed.lastIndex(of: "@") else { return nil }
        let domain = trimmed[trimmed.index(after: at)...].lowercased()
        let host: Substring
        if domain.hasSuffix(dmsg) {
            host = domain.dropLast(dmsg.count)
        } else if domain.hasSuffix(skynet) {
            host = domain.dropLast(skynet.count)
        } else {
            return nil
        }
        let last = host.split(separator: ".", omittingEmptySubsequences: false).last ?? host
        return pk(ofLabel: String(last))
    }

    /// What a typed recipient means: an address as given, or a bare public key
    /// (hex or skychat://) turned into an address over `suffix`. Nil when it is
    /// neither. An address keeps the suffix it was written with: that is the
    /// sender's choice of network.
    public static func recipient(_ input: String, suffix: String = dmsg) -> String? {
        var text = input.trimmingCharacters(in: .whitespacesAndNewlines)
        if text.hasPrefix("skychat://") {
            text.removeFirst("skychat://".count)
        }
        while text.hasSuffix("/") {
            text.removeLast()
        }
        if text.contains("@") {
            return pk(ofAddress: text) == nil ? nil : text
        }
        return of(text, suffix: suffix)
    }

    /// `text` with every SkyMail address in it moved onto the `suffix` network.
    public static func withNetwork(_ text: String, suffix: String) -> String {
        let range = NSRange(text.startIndex..., in: text)
        let template = "$1" + NSRegularExpression.escapedTemplate(for: suffix)
        return addressHost.stringByReplacingMatches(in: text, range: range, withTemplate: template)
    }

    private static let addressHost = try! NSRegularExpression(
        pattern: #"(@(?:[a-z0-9-]+\.)*[a-z2-7]{53})(?:\.dmsg|\.skynet)\b"#,
        options: [.caseInsensitive]
    )

    /// A key typed or pasted as a key, a skychat link or an address, as lowercase hex.
    public static func pk(ofInput input: String) -> String? {
        let text = input.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        if let pk = pk(ofAddress: text) { return pk }
        var key = text
        if key.hasPrefix("skychat://") {
            key.removeFirst("skychat://".count)
        }
        while key.hasSuffix("/") {
            key.removeLast()
        }
        return label(key) == nil ? nil : key
    }

    private static func bytes(_ hex: String) -> [UInt8]? {
        guard hex.allSatisfy({ ("0"..."9").contains($0) || ("a"..."f").contains($0) }) else { return nil }
        var out: [UInt8] = []
        out.reserveCapacity(hex.count / 2)
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2)
            guard let byte = UInt8(hex[index..<next], radix: 16) else { return nil }
            out.append(byte)
            index = next
        }
        return out
    }
}

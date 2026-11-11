import CoreClient
import Foundation
import Network

/// Wallet requests to Skywire mesh names, as skycoin-web's --socks5-proxy does on the desktop
/// (Android: wallet/WalletMesh.kt). A host ending in .dmsg goes through the visor's resolver on
/// loopback, which dials it over dmsg; every other host goes direct. The resolver runs only while
/// Skywire is connected.
enum WalletMesh {
    static func isMesh(_ host: String) -> Bool { host.lowercased().hasSuffix(".dmsg") }

    /// Whether `url` names a .dmsg node.
    static func isMeshURL(_ url: String) -> Bool {
        URL(string: normalize(url))?.host.map(isMesh) ?? false
    }

    /// The session .dmsg requests take: a SOCKS5 proxy to the resolver, which reads the name
    /// itself. Nil before iOS 17, which has no SOCKS proxy for a URLSession that could be tested here.
    static func makeSession() -> URLSession? {
        guard #available(iOS 17, *) else { return nil }
        let config = WalletStore.makeSession().configuration
        let port = NWEndpoint.Port(rawValue: UInt16(PhoneProfile.dmsgWebPort))!
        config.proxyConfigurations = [ProxyConfiguration(socksv5Proxy: .hostPort(host: "127.0.0.1", port: port))]
        return URLSession(configuration: config)
    }

    private static let hexHost = try! NSRegularExpression(pattern: "^(https?://)([0-9a-f]{66})(\\.dmsg)(?=[:/?#]|$)", options: .caseInsensitive)

    /// A node address with its public key written as 66 hex characters, as it is usually shared,
    /// rewritten to the 53-character base32 label (pkg/cipher/dnslabel.go). A DNS label holds at
    /// most 63 characters (RFC 1035 2.3.4), so no URL parser takes the hex form as a host.
    static func normalize(_ url: String) -> String {
        let trimmed = url.trimmingCharacters(in: .whitespacesAndNewlines)
        let range = NSRange(trimmed.startIndex..., in: trimmed)
        guard let m = hexHost.firstMatch(in: trimmed, range: range),
              let scheme = Range(m.range(at: 1), in: trimmed), let key = Range(m.range(at: 2), in: trimmed),
              let all = Range(m.range, in: trimmed)
        else { return trimmed }
        return String(trimmed[scheme]) + dnsLabel(String(trimmed[key])) + ".dmsg" + trimmed[all.upperBound...]
    }

    /// RFC 4648 base32 of the key's bytes, lowercase and unpadded, as cipher.PubKey.DNSLabel writes it.
    static func dnsLabel(_ hex: String) -> String {
        let alphabet = Array("abcdefghijklmnopqrstuvwxyz234567")
        var out = ""
        var buffer = 0
        var bits = 0
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2)
            buffer = (buffer << 8) | Int(hex[index..<next], radix: 16)!
            bits += 8
            while bits >= 5 {
                out.append(alphabet[(buffer >> (bits - 5)) & 31])
                bits -= 5
            }
            buffer &= (1 << bits) - 1
            index = next
        }
        if bits > 0 { out.append(alphabet[(buffer << (5 - bits)) & 31]) }
        return out
    }

    /// A .dmsg node's 53-character key label shortened in the middle, so a row still shows whose node it is.
    static func shortURL(_ url: String) -> String {
        guard let host = URL(string: url)?.host, isMesh(host) else { return url }
        let label = String(host.dropLast(".dmsg".count))
        guard label.count > 16 else { return url }
        return url.replacingOccurrences(of: label, with: "\(label.prefix(8))…\(label.suffix(6))")
    }
}

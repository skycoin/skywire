import Foundation

/// Argv editing for the app flags the phone owns in the visor config: the
/// port of Android's core/AppArgs.kt, plus the config's quoting.
///
/// On disk an app's argv is one string, which the visor splits and joins
/// shell-style (pkg/visor/visorconfig/args.go: splitArgs, joinArgs). Android
/// splits on bare spaces, which is safe there because no path it writes has
/// one. iOS paths do ("Library/Application Support"), and the visor writes a
/// quoted token back whenever it flushes the config, so here the string is
/// read and written with the visor's own rules.
public enum AppArgs {
    /// The host every listener the phone starts must bind: on a shared
    /// loopback (another app on the phone, the Mac under the Simulator) any
    /// wider address is reachable by more than this app.
    public static let loopbackHost = "127.0.0.1"

    /// The argv in `string`, split as the visor's splitArgs does:
    /// whitespace-separated, "double" and 'single' quoted tokens, backslash
    /// escapes only inside double quotes. Nil for an unclosed quote, which
    /// the visor also rejects.
    public static func split(_ string: String) -> [String]? {
        var out: [String] = []
        var current: [UInt8] = []
        var inDouble = false, inSingle = false, inToken = false
        let bytes = Array(string.utf8)
        var i = 0
        while i < bytes.count {
            let c = bytes[i]
            if inDouble {
                if c == UInt8(ascii: "\\"), i + 1 < bytes.count {
                    current.append(bytes[i + 1])
                    i += 2
                    continue
                }
                if c == UInt8(ascii: "\"") {
                    inDouble = false
                } else {
                    current.append(c)
                }
            } else if inSingle {
                if c == UInt8(ascii: "'") {
                    inSingle = false
                } else {
                    current.append(c)
                }
            } else {
                switch c {
                case UInt8(ascii: " "), UInt8(ascii: "\t"), UInt8(ascii: "\n"):
                    if inToken {
                        out.append(String(decoding: current, as: UTF8.self))
                        current = []
                        inToken = false
                    }
                case UInt8(ascii: "\""):
                    inDouble = true
                    inToken = true
                case UInt8(ascii: "'"):
                    inSingle = true
                    inToken = true
                default:
                    current.append(c)
                    inToken = true
                }
            }
            i += 1
        }
        if inDouble || inSingle { return nil }
        if inToken {
            out.append(String(decoding: current, as: UTF8.self))
        }
        return out
    }

    /// `args` as the visor's joinArgs renders them: a token with whitespace,
    /// a quote or a backslash in it is double-quoted with `\` and `"`
    /// escaped, an empty one is `""`.
    public static func join(_ args: [String]) -> String {
        args.map { token in
            if token.isEmpty { return "\"\"" }
            if token.contains(where: { " \t\n\"'\\".contains($0) }) {
                let escaped = token.replacingOccurrences(of: "\\", with: "\\\\").replacingOccurrences(of: "\"", with: "\\\"")
                return "\"\(escaped)\""
            }
            return token
        }.joined(separator: " ")
    }

    /// Sets `flag`'s value to what `next` returns for the current one (nil
    /// when the flag is absent), appending the flag if it was not there. A nil
    /// from `next` leaves the value as it is. Both spellings the config can
    /// carry are read: `--flag value` and `--flag=value`.
    public static func pinValue(_ args: inout [String], _ flag: [String], _ next: (String?) -> String?) {
        guard let i = args.firstIndex(where: { token in flag.contains { token == $0 || token.hasPrefix("\($0)=") } }) else {
            if let value = next(nil) {
                args += [flag[0], value]
            }
            return
        }
        if let equals = args[i].firstIndex(of: "=") {
            if let value = next(String(args[i][args[i].index(after: equals)...])) {
                args[i] = "\(flag[0])=\(value)"
            }
        } else if i + 1 < args.count {
            if let value = next(args[i + 1]) {
                args[i + 1] = value
            }
        } else if let value = next(nil) {
            // A trailing flag with no value: a broken argv the visor would
            // reject anyway. Complete it rather than shift everything.
            args.append(value)
        }
    }

    /// The first value of any of `flags` in `args`, or nil.
    public static func value(_ args: [String], _ flags: [String]) -> String? {
        for (i, token) in args.enumerated() {
            for flag in flags {
                if token.hasPrefix("\(flag)=") {
                    return String(token.dropFirst(flag.count + 1))
                }
                if token == flag, i + 1 < args.count {
                    return args[i + 1]
                }
            }
        }
        return nil
    }

    /// Whether any of `flags` is present, in either spelling.
    static func contains(_ args: [String], _ flags: [String]) -> Bool {
        args.contains { token in flags.contains { token == $0 || token.hasPrefix("\($0)=") } }
    }

    /// `<host>:<port>` with the host forced to loopback, keeping the port
    /// `current` carries and falling back to `defaultPort` when there is no
    /// value at all. Nil for a value it cannot read: a malformed address only
    /// gets worse from rewriting, and the visor reports it.
    public static func loopbackAddress(_ current: String?, defaultPort: Int) -> String? {
        guard let current else { return "\(loopbackHost):\(defaultPort)" }
        let portText = current.split(separator: ":", omittingEmptySubsequences: false).last.map(String.init) ?? current
        guard let port = Int(portText) else { return nil }
        return "\(loopbackHost):\(port)"
    }

    /// The port an app listens on, from its `--addr` value, or `defaultPort`.
    static func listenPort(_ args: [String], defaultPort: Int) -> Int {
        guard let address = value(args, ["--addr", "-addr"]),
              let port = address.split(separator: ":", omittingEmptySubsequences: false).last.flatMap({ Int($0) })
        else { return defaultPort }
        return port
    }
}

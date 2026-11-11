import Foundation

/// Payment request URIs, written and read.
///
/// Fiber chains use the skycoin-web form, `skycoin:ADDR?amount=1.5&hours=10&message=…`,
/// with the coin's own name as the scheme. Bitcoin is BIP 21. Ethereum is
/// EIP-681, whose amounts are integers in the chain's smallest unit: `value`
/// in wei for ether, and a `transfer` call with `uint256` for a token.
public enum PaymentUri {

    public struct Scanned: Equatable, Sendable {
        public let scheme: String?
        /// For a token transfer, the recipient rather than the contract the
        /// URI names first.
        public let address: String
        /// The coin amount as a decimal number of whole coins; nil when none
        /// is asked.
        public let amount: Decimal?
        public let hours: UInt64?
        public let message: String?
        /// EIP-681 token transfer: the token's contract. Nil for every other
        /// request.
        public let contract: String?
        /// EIP-681 token transfer: the amount in the token's smallest unit.
        public let tokenUnits: BigUInt?

        public init(
            scheme: String?, address: String, amount: Decimal?, hours: UInt64?, message: String?,
            contract: String? = nil, tokenUnits: BigUInt? = nil
        ) {
            self.scheme = scheme
            self.address = address
            self.amount = amount
            self.hours = hours
            self.message = message
            self.contract = contract
            self.tokenUnits = tokenUnits
        }
    }

    /// A scheme from a coin's name: lowercase letters, digits and `+.-`,
    /// starting with a letter.
    public static func schemeFor(_ coinName: String) -> String {
        var s = ""
        for c in coinName.lowercased().unicodeScalars where ("a"..."z").contains(c) || ("0"..."9").contains(c) || "+.-".unicodeScalars.contains(c) {
            s.unicodeScalars.append(c)
        }
        return s.unicodeScalars.first.map { ("a"..."z").contains($0) } == true ? s : "skycoin"
    }

    public static func fiber(scheme: String, address: String, amount: String?, hours: UInt64?, message: String?) -> String {
        build(scheme, address, [("amount", amount), ("hours", hours.map(String.init)), ("message", message)])
    }

    public static func bitcoin(address: String, amount: String?, message: String?) -> String {
        build("bitcoin", address, [("amount", amount), ("message", message)])
    }

    public static func ether(address: String, wei: BigUInt?) -> String {
        build("ethereum", address, [("value", wei?.description)])
    }

    public static func erc20(contract: String, to: String, units: BigUInt?) -> String {
        build("ethereum", "\(contract)/transfer", [("address", to), ("uint256", units?.description)])
    }

    private static func build(_ scheme: String, _ path: String, _ params: [(String, String?)]) -> String {
        let query = params.compactMap { k, v in v.flatMap { $0.isEmpty ? nil : "\(k)=\(encode($0))" } }
            .joined(separator: "&")
        return query.isEmpty ? "\(scheme):\(path)" : "\(scheme):\(path)?\(query)"
    }

    /// Java's URLEncoder over UTF-8 with a space as "%20" rather than "+",
    /// which BIP 21 readers take literally.
    private static func encode(_ v: String) -> String {
        let hex = Array("0123456789ABCDEF".utf8)
        var out = [UInt8]()
        for b in v.utf8 {
            switch b {
            case UInt8(ascii: "a")...UInt8(ascii: "z"), UInt8(ascii: "A")...UInt8(ascii: "Z"),
                 UInt8(ascii: "0")...UInt8(ascii: "9"), UInt8(ascii: "."), UInt8(ascii: "-"),
                 UInt8(ascii: "*"), UInt8(ascii: "_"):
                out.append(b)
            default:
                out += [UInt8(ascii: "%"), hex[Int(b >> 4)], hex[Int(b & 0x0f)]]
            }
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// Reads a scanned code: a bare address, or any of the URIs above.
    /// Values that do not parse are dropped rather than guessed at, so the
    /// worst a malformed code does is leave a field empty.
    public static func parse(_ text: String) -> Scanned {
        var rest = text.trimmingCharacters(in: .whitespacesAndNewlines)
        let scheme = schemePrefix(rest)?.lowercased()
        if let scheme {
            rest = String(String.UnicodeScalarView(rest.unicodeScalars.dropFirst(scheme.unicodeScalars.count + 1)))
            if rest.hasPrefix("//") { rest = String(String.UnicodeScalarView(rest.unicodeScalars.dropFirst(2))) }
        }
        // Split on single characters by scalar, as Kotlin splits by char:
        // a combining mark after a "?" must not hide it.
        let path = before(rest, "?")
        let query = after(rest, "?") ?? ""
        var params = [String: String]()
        for pair in split(query, "&") {
            guard let value = after(pair, "=") else { continue }
            params[before(pair, "=").lowercased()] = decode(value)
        }
        // EIP-681: ADDRESS[@chain][/function].
        let target = before(before(path, "/"), "@")
        let function = after(path, "/").flatMap { $0.isEmpty ? nil : $0 }
        if scheme == "ethereum" {
            if function == "transfer" {
                return Scanned(
                    scheme: scheme,
                    address: params["address"] ?? "",
                    amount: nil,
                    hours: nil,
                    message: nil,
                    contract: target,
                    tokenUnits: params["uint256"].flatMap(integer)
                )
            }
            let wei = params["value"].flatMap(integer)
            return Scanned(scheme: scheme, address: target, amount: wei.flatMap { exactDecimal(digits: $0.description, exponent: -18) }, hours: nil, message: nil)
        }
        return Scanned(
            scheme: scheme,
            address: target,
            amount: params["amount"].flatMap(decimal),
            hours: params["hours"].flatMap(unsignedLong),
            message: params["message"].flatMap { $0.isEmpty ? nil : $0 }
        )
    }

    /// Kotlin's substringBefore: the text before the first `c`, or all of it.
    private static func before(_ s: String, _ c: Unicode.Scalar) -> String {
        guard let i = s.unicodeScalars.firstIndex(of: c) else { return s }
        return String(s.unicodeScalars[..<i])
    }

    /// The text after the first `c`, or nil when there is none.
    private static func after(_ s: String, _ c: Unicode.Scalar) -> String? {
        guard let i = s.unicodeScalars.firstIndex(of: c) else { return nil }
        return String(s.unicodeScalars[s.unicodeScalars.index(after: i)...])
    }

    /// Kotlin's split(c): every piece, empty ones kept.
    private static func split(_ s: String, _ c: Unicode.Scalar) -> [String] {
        s.unicodeScalars.split(separator: c, omittingEmptySubsequences: false).map { String($0) }
    }

    /// The `^([a-zA-Z][a-zA-Z0-9+.-]*):` prefix's scheme, or nil.
    private static func schemePrefix(_ s: String) -> String? {
        let scalars = Array(s.unicodeScalars)
        guard let first = scalars.first, first.isASCII, first.properties.isAlphabetic else { return nil }
        for (i, c) in scalars.enumerated().dropFirst() {
            if c == ":" { return String(String.UnicodeScalarView(scalars[0..<i])) }
            let ok = c.isASCII && (c.properties.isAlphabetic || ("0"..."9").contains(c) || "+.-".unicodeScalars.contains(c))
            if !ok { return nil }
        }
        return nil
    }

    /// Java's URLDecoder over UTF-8: "+" is a space, "%XX" a byte, and a
    /// malformed escape leaves the value as it came.
    private static func decode(_ v: String) -> String {
        let bytes = Array(v.utf8)
        var out = [UInt8]()
        var i = 0
        while i < bytes.count {
            switch bytes[i] {
            case UInt8(ascii: "+"):
                out.append(0x20)
                i += 1
            case UInt8(ascii: "%"):
                guard i + 2 < bytes.count, let b = [UInt8](hex: String(decoding: bytes[(i + 1)...(i + 2)], as: UTF8.self))
                else { return v }
                out += b
                i += 3
            default:
                out.append(bytes[i])
                i += 1
            }
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// `^\d*\.?\d+$` (ASCII digits) read exactly, as BigDecimal(v) is.
    private static func decimal(_ v: String) -> Decimal? {
        let parts = v.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count <= 2 else { return nil }
        let whole = parts.count == 2 ? String(parts[0]) : ""
        let frac = parts.count == 2 ? String(parts[1]) : String(parts[0])
        guard !frac.isEmpty, (whole + frac).utf8.allSatisfy({ (UInt8(ascii: "0")...UInt8(ascii: "9")).contains($0) })
        else { return nil }
        return exactDecimal(digits: whole + frac, exponent: parts.count == 2 ? -frac.count : 0)
    }

    /// digits × 10^exponent as a Decimal, or nil when it needs more than the
    /// 38 significant digits a Decimal holds exactly.
    private static func exactDecimal(digits: String, exponent: Int) -> Decimal? {
        var d = Substring(digits.drop { $0 == "0" })
        var e = exponent
        while d.hasSuffix("0") {
            d.removeLast()
            e += 1
        }
        if d.isEmpty { return 0 }
        guard d.count <= 38, (-128...127).contains(e), let significand = Decimal(string: String(d)) else { return nil }
        let value = Decimal(sign: .plus, exponent: e, significand: significand)
        return value.isNaN ? nil : value
    }

    /// EIP-681 numbers may use an exponent ("1.5e18"); only whole results
    /// are amounts. BigDecimal(v)'s grammar, read exactly; a number past
    /// 4096 digits is dropped.
    private static func integer(_ v: String) -> BigUInt? {
        var s = Substring(v)
        var negative = false
        if let sign = s.first, sign == "+" || sign == "-" {
            negative = sign == "-"
            s.removeFirst()
        }
        var exponent = 0
        if let e = s.firstIndex(where: { $0 == "e" || $0 == "E" }) {
            var exp = s[s.index(after: e)...]
            var expNegative = false
            if let sign = exp.first, sign == "+" || sign == "-" {
                expNegative = sign == "-"
                exp.removeFirst()
            }
            // BigDecimal takes an exponent that fits an int.
            guard !exp.isEmpty, let n = javaDigits(exp), n <= UInt64(Int32.max) else { return nil }
            exponent = expNegative ? -Int(n) : Int(n)
            s = s[..<e]
        }
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count <= 2 else { return nil }
        let whole = String(parts[0])
        let frac = parts.count == 2 ? String(parts[1]) : ""
        guard !(whole + frac).isEmpty, let all = javaDigitString(whole + frac) else { return nil }
        let scale = frac.count - exponent
        var significant = Substring(all.drop { $0 == "0" })
        if significant.isEmpty { return .zero }
        if negative { return nil }
        if scale > 0 {
            // Exact only when the dropped places are zeros.
            guard significant.count > scale ? significant.suffix(scale).allSatisfy({ $0 == "0" }) : false else { return nil }
            significant.removeLast(scale)
            return BigUInt(decimal: String(significant))
        }
        guard significant.count - scale <= 4096 else { return nil }
        return BigUInt(decimal: String(significant) + String(repeating: "0", count: -scale))
    }

    /// Kotlin's String.toULongOrNull: an optional "+", then decimal digits
    /// (any script's, as Character.digit reads them); nil on overflow.
    private static func unsignedLong(_ v: String) -> UInt64? {
        var s = Substring(v)
        if s.first == "+" && s.count > 1 { s.removeFirst() }
        guard !s.isEmpty, let n = javaDigits(s) else { return nil }
        return n
    }

    /// The value of a run of decimal digits, or nil on another character or
    /// overflow.
    private static func javaDigits(_ s: Substring) -> UInt64? {
        guard let ascii = javaDigitString(String(s)) else { return nil }
        return UInt64(ascii)
    }

    /// Decimal digits of any script mapped to ASCII, as Character.digit(c,
    /// 10) reads a UTF-16 unit; nil on anything else.
    private static func javaDigitString(_ s: String) -> String? {
        var out = ""
        for u in s.unicodeScalars {
            guard u.value <= 0xFFFF, u.properties.generalCategory == .decimalNumber,
                  let n = u.properties.numericValue, n >= 0, n <= 9
            else { return nil }
            out.append(Character(String(Int(n))))
        }
        return out
    }
}

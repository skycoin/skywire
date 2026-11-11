/// Fixed-point parsing and rendering for base-unit integer amounts.
public enum Amounts {

    /// Parse a decimal string into base units with the given exponent (6 for
    /// droplets, 8 for satoshis). Nil on malformed input, overflow, or more
    /// fractional digits than the exponent allows.
    ///
    /// Digits are any Unicode decimal digits, as Kotlin's isDigit/toULong
    /// read them: a keyboard in Persian or Arabic types ۱۲ or ١٢, and those
    /// are the amounts the user meant.
    public static func parse(_ text: String, exponent: Int) -> UInt64? {
        let t = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if t.isEmpty || t.filter({ $0 == "." }).count > 1 { return nil }
        let parts = t.split(separator: ".", omittingEmptySubsequences: false)
        guard let whole = asciiDigits(parts[0].isEmpty ? "0" : parts[0]),
              let frac = asciiDigits(parts.count == 2 ? parts[1] : "")
        else { return nil }
        if frac.count > exponent { return nil }
        let fracPadded = frac + String(repeating: "0", count: exponent - frac.count)

        guard let w = UInt64(whole) else { return nil }
        let (scaled, overflow) = w.multipliedReportingOverflow(by: pow10(exponent))
        if overflow { return nil }
        let f = fracPadded.isEmpty ? 0 : UInt64(fracPadded)!
        let (sum, overflow2) = scaled.addingReportingOverflow(f)
        return overflow2 ? nil : sum
    }

    /// Count of decimal places actually used (validating against a chain's
    /// max precision). As Kotlin computes it: the '.' found in the text as
    /// given, the length taken after trimming.
    public static func decimals(_ text: String) -> Int {
        guard let dot = text.firstIndex(of: ".") else { return 0 }
        let i = text.distance(from: text.startIndex, to: dot)
        return text.trimmingCharacters(in: .whitespacesAndNewlines).count - i - 1
    }

    /// Render base units at full precision, trailing zeros kept to
    /// minDecimals (all of them when minDecimals is negative).
    public static func format(_ units: UInt64, exponent: Int, minDecimals: Int = -1) -> String {
        let scale = pow10(exponent)
        let whole = units / scale
        let frac = units % scale
        let keep = minDecimals >= 0 ? minDecimals : exponent
        var fracStr = String(frac)
        if fracStr.count < exponent { fracStr = String(repeating: "0", count: exponent - fracStr.count) + fracStr }
        while fracStr.count > keep && fracStr.hasSuffix("0") { fracStr.removeLast() }
        let wholeStr = groupThousands(String(whole))
        return fracStr.isEmpty ? wholeStr : "\(wholeStr).\(fracStr)"
    }

    public static func groupThousands(_ digits: String) -> String {
        var out = ""
        let chars = Array(digits)
        for (i, c) in chars.enumerated() {
            if i > 0 && (chars.count - i) % 3 == 0 { out.append(",") }
            out.append(c)
        }
        return out
    }

    static func pow10(_ n: Int) -> UInt64 {
        var v: UInt64 = 1
        for _ in 0..<n { v &*= 10 }
        return v
    }

    /// The text's digits as ASCII, or nil when a character is not a decimal
    /// digit (Unicode category Nd).
    private static func asciiDigits<S: StringProtocol>(_ s: S) -> String? {
        var out = ""
        for scalar in s.unicodeScalars {
            guard scalar.properties.generalCategory == .decimalNumber,
                  let value = scalar.properties.numericValue, value >= 0, value <= 9
            else { return nil }
            out.append(Character(String(Int(value))))
        }
        return out
    }
}

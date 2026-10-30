/// Bitcoin-alphabet base58, as used by Skycoin addresses and legacy BTC
/// addresses. Byte-wise base conversion (the Kotlin port goes through
/// BigInteger; the results are the same: one '1' per leading zero byte, then
/// the number's minimal digits).
public enum Base58 {
    private static let alphabet = Array("123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz".utf8)
    private static let indexes: [Int8] = {
        var idx = [Int8](repeating: -1, count: 128)
        for (i, c) in alphabet.enumerated() { idx[Int(c)] = Int8(i) }
        return idx
    }()

    public static func encode(_ input: [UInt8]) -> String {
        if input.isEmpty { return "" }
        var zeros = 0
        while zeros < input.count && input[zeros] == 0 { zeros += 1 }

        // Base-58 digits of the number, least significant first.
        var digits = [UInt8]()
        for byte in input[zeros...] {
            var carry = Int(byte)
            for i in 0..<digits.count {
                carry += Int(digits[i]) << 8
                digits[i] = UInt8(carry % 58)
                carry /= 58
            }
            while carry > 0 {
                digits.append(UInt8(carry % 58))
                carry /= 58
            }
        }
        var out = [UInt8](repeating: UInt8(ascii: "1"), count: zeros)
        for d in digits.reversed() { out.append(alphabet[Int(d)]) }
        return String(decoding: out, as: UTF8.self)
    }

    /// Nil on any character outside the alphabet, or an empty string.
    public static func decode(_ input: String) -> [UInt8]? {
        let chars = Array(input.utf8)
        if chars.isEmpty { return nil }
        var zeros = 0
        while zeros < chars.count && chars[zeros] == UInt8(ascii: "1") { zeros += 1 }

        // Base-256 bytes of the number, least significant first.
        var bytes = [UInt8]()
        for c in chars {
            guard c < 128, indexes[Int(c)] >= 0 else { return nil }
            var carry = Int(indexes[Int(c)])
            for i in 0..<bytes.count {
                carry += Int(bytes[i]) * 58
                bytes[i] = UInt8(carry & 0xff)
                carry >>= 8
            }
            while carry > 0 {
                bytes.append(UInt8(carry & 0xff))
                carry >>= 8
            }
        }
        // The leading '1's contributed zero to the number; their bytes are
        // the explicit zeros in front.
        return [UInt8](repeating: 0, count: zeros) + bytes.reversed()
    }
}

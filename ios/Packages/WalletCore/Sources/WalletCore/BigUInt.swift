/// An unsigned integer of any size: what the Kotlin port gets from
/// java.math.BigInteger for Ethereum's wei and raw token units (2⁶⁴ wei is
/// only ~18.4 ETH) and Skycoin's hour split. Only the operations those use:
/// + − × ÷ %, comparison, powers of ten, and conversion to and from
/// big-endian bytes, hex and decimal text. Not constant-time; it never
/// touches a key.
public struct BigUInt: Comparable, Hashable, Sendable, CustomStringConvertible {
    /// Little-endian 32-bit limbs, no zero limb at the top (zero is []).
    private var limbs: [UInt32]

    public static let zero = BigUInt(limbs: [])

    private init(limbs: [UInt32]) {
        self.limbs = limbs
        normalize()
    }

    public init(_ value: UInt64) {
        self.init(limbs: [UInt32(truncatingIfNeeded: value), UInt32(truncatingIfNeeded: value >> 32)])
    }

    /// The number a big-endian byte string spells (Kotlin's BigInteger(1, bytes)).
    public init(bigEndian bytes: [UInt8]) {
        var out = [UInt32]()
        var i = bytes.count
        while i > 0 {
            var limb: UInt32 = 0
            for k in 0..<4 where i - 1 - k >= 0 {
                limb |= UInt32(bytes[i - 1 - k]) << (8 * UInt32(k))
            }
            out.append(limb)
            i -= 4
        }
        self.init(limbs: out)
    }

    /// Hex digits, no prefix; an empty string is zero. Nil on a non-hex digit.
    public init?(hex: String) {
        var value = BigUInt.zero
        for c in hex.utf8 {
            let d: UInt32
            switch c {
            case UInt8(ascii: "0")...UInt8(ascii: "9"): d = UInt32(c - UInt8(ascii: "0"))
            case UInt8(ascii: "a")...UInt8(ascii: "f"): d = UInt32(c - UInt8(ascii: "a") + 10)
            case UInt8(ascii: "A")...UInt8(ascii: "F"): d = UInt32(c - UInt8(ascii: "A") + 10)
            default: return nil
            }
            value = value.multiplied(bySmall: 16).adding(small: d)
        }
        self = value
    }

    /// ASCII decimal digits, as BigInteger(String) reads them for a
    /// non-negative number. Nil when empty or on any other character.
    public init?(decimal: String) {
        if decimal.isEmpty { return nil }
        var value = BigUInt.zero
        for c in decimal.utf8 {
            guard c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9") else { return nil }
            value = value.multiplied(bySmall: 10).adding(small: UInt32(c - UInt8(ascii: "0")))
        }
        self = value
    }

    public var isZero: Bool { limbs.isEmpty }

    /// Bits needed to write the number (0 for zero).
    public var bitWidth: Int {
        guard let top = limbs.last else { return 0 }
        return limbs.count * 32 - top.leadingZeroBitCount
    }

    /// Minimal big-endian bytes; zero is empty (RLP's integer encoding).
    public var bigEndianBytes: [UInt8] {
        var out = [UInt8]()
        for limb in limbs.reversed() {
            for k in stride(from: 3, through: 0, by: -1) { out.append(UInt8(truncatingIfNeeded: limb >> (8 * UInt32(k)))) }
        }
        let firstNonZero = out.firstIndex(where: { $0 != 0 }) ?? out.count
        return Array(out[firstNonZero...])
    }

    /// Lowercase hex without leading zeros; "0" for zero.
    public var hex: String {
        if isZero { return "0" }
        let s = bigEndianBytes.hex
        return s.hasPrefix("0") ? String(s.dropFirst()) : s
    }

    public var description: String {
        if isZero { return "0" }
        var digits = [UInt8]()
        var v = self
        while !v.isZero {
            let (q, r) = v.divided(bySmall: 10)
            digits.append(UInt8(ascii: "0") + UInt8(r))
            v = q
        }
        return String(decoding: digits.reversed(), as: UTF8.self)
    }

    /// The value when it fits 64 bits, else UInt64.max (Kotlin's
    /// toULongClamped; there is no negative here).
    public var clampedUInt64: UInt64 {
        if limbs.count > 2 { return .max }
        var v: UInt64 = 0
        for (i, limb) in limbs.enumerated() { v |= UInt64(limb) << (32 * UInt64(i)) }
        return v
    }

    public static func pow10(_ n: Int) -> BigUInt {
        var v = BigUInt(1)
        for _ in 0..<n { v = v.multiplied(bySmall: 10) }
        return v
    }

    // MARK: arithmetic

    public static func + (a: BigUInt, b: BigUInt) -> BigUInt {
        var out = [UInt32]()
        var carry: UInt64 = 0
        for i in 0..<max(a.limbs.count, b.limbs.count) {
            let s = UInt64(i < a.limbs.count ? a.limbs[i] : 0) + UInt64(i < b.limbs.count ? b.limbs[i] : 0) + carry
            out.append(UInt32(truncatingIfNeeded: s))
            carry = s >> 32
        }
        if carry > 0 { out.append(UInt32(carry)) }
        return BigUInt(limbs: out)
    }

    /// a − b; b must not exceed a (there is no negative here: every caller
    /// compares first, as the Kotlin code does before it subtracts).
    public static func - (a: BigUInt, b: BigUInt) -> BigUInt {
        precondition(a >= b, "BigUInt subtraction would go negative")
        var out = [UInt32]()
        var borrow: Int64 = 0
        for i in 0..<a.limbs.count {
            var d = Int64(a.limbs[i]) - Int64(i < b.limbs.count ? b.limbs[i] : 0) - borrow
            if d < 0 {
                d += 1 << 32
                borrow = 1
            } else {
                borrow = 0
            }
            out.append(UInt32(d))
        }
        return BigUInt(limbs: out)
    }

    public static func * (a: BigUInt, b: BigUInt) -> BigUInt {
        if a.isZero || b.isZero { return .zero }
        var out = [UInt32](repeating: 0, count: a.limbs.count + b.limbs.count)
        for i in 0..<a.limbs.count {
            var carry: UInt64 = 0
            for j in 0..<b.limbs.count {
                let t = UInt64(a.limbs[i]) * UInt64(b.limbs[j]) + UInt64(out[i + j]) + carry
                out[i + j] = UInt32(truncatingIfNeeded: t)
                carry = t >> 32
            }
            out[i + b.limbs.count] = UInt32(carry)
        }
        return BigUInt(limbs: out)
    }

    public static func / (a: BigUInt, b: BigUInt) -> BigUInt { a.quotientAndRemainder(dividingBy: b).quotient }

    public static func % (a: BigUInt, b: BigUInt) -> BigUInt { a.quotientAndRemainder(dividingBy: b).remainder }

    /// Shift-and-subtract long division: plain and plainly correct, and the
    /// numbers here are at most a few hundred bits.
    public func quotientAndRemainder(dividingBy d: BigUInt) -> (quotient: BigUInt, remainder: BigUInt) {
        precondition(!d.isZero, "BigUInt division by zero")
        if self < d { return (.zero, self) }
        if d.limbs.count == 1 {
            let (q, r) = divided(bySmall: d.limbs[0])
            return (q, BigUInt(UInt64(r)))
        }
        var quotient = [UInt32](repeating: 0, count: limbs.count)
        var remainder = BigUInt.zero
        for bit in stride(from: bitWidth - 1, through: 0, by: -1) {
            remainder = remainder.shiftedLeftOne(orBit: (limbs[bit / 32] >> UInt32(bit % 32)) & 1)
            if remainder >= d {
                remainder = remainder - d
                quotient[bit / 32] |= 1 << UInt32(bit % 32)
            }
        }
        return (BigUInt(limbs: quotient), remainder)
    }

    public static func < (a: BigUInt, b: BigUInt) -> Bool {
        if a.limbs.count != b.limbs.count { return a.limbs.count < b.limbs.count }
        for i in stride(from: a.limbs.count - 1, through: 0, by: -1) where a.limbs[i] != b.limbs[i] {
            return a.limbs[i] < b.limbs[i]
        }
        return false
    }

    // MARK: helpers

    private mutating func normalize() {
        while let last = limbs.last, last == 0 { limbs.removeLast() }
    }

    private func multiplied(bySmall m: UInt32) -> BigUInt {
        var out = [UInt32]()
        var carry: UInt64 = 0
        for limb in limbs {
            let t = UInt64(limb) * UInt64(m) + carry
            out.append(UInt32(truncatingIfNeeded: t))
            carry = t >> 32
        }
        if carry > 0 { out.append(UInt32(carry)) }
        return BigUInt(limbs: out)
    }

    private func adding(small s: UInt32) -> BigUInt { self + BigUInt(UInt64(s)) }

    private func divided(bySmall d: UInt32) -> (BigUInt, UInt32) {
        var out = [UInt32](repeating: 0, count: limbs.count)
        var rem: UInt64 = 0
        for i in stride(from: limbs.count - 1, through: 0, by: -1) {
            let cur = rem << 32 | UInt64(limbs[i])
            out[i] = UInt32(cur / UInt64(d))
            rem = cur % UInt64(d)
        }
        return (BigUInt(limbs: out), UInt32(rem))
    }

    private func shiftedLeftOne(orBit bit: UInt32) -> BigUInt {
        var out = [UInt32]()
        var carry = bit
        for limb in limbs {
            out.append(limb << 1 | carry)
            carry = limb >> 31
        }
        if carry > 0 { out.append(carry) }
        return BigUInt(limbs: out)
    }
}

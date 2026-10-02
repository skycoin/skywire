import Foundation

// Byte helpers the Kotlin module gets from ByteArray and its toHex /
// hexToBytes extensions (Hashes.kt).

extension Array where Element == UInt8 {
    /// Lowercase hex, two digits per byte.
    public var hex: String {
        let digits = Array("0123456789abcdef".utf8)
        var out = [UInt8]()
        out.reserveCapacity(count * 2)
        for b in self {
            out.append(digits[Int(b >> 4)])
            out.append(digits[Int(b & 0x0f)])
        }
        return String(decoding: out, as: UTF8.self)
    }

    /// Bytes from hex (either case, no prefix); nil on an odd length or a
    /// character that is not a hex digit.
    public init?(hex: String) {
        let chars = Array(hex.utf8)
        guard chars.count % 2 == 0 else { return nil }
        var out = [UInt8]()
        out.reserveCapacity(chars.count / 2)
        var i = 0
        while i < chars.count {
            guard let hi = hexValue(chars[i]), let lo = hexValue(chars[i + 1]) else { return nil }
            out.append(hi << 4 | lo)
            i += 2
        }
        self = out
    }
}

private func hexValue(_ c: UInt8) -> UInt8? {
    switch c {
    case UInt8(ascii: "0")...UInt8(ascii: "9"): return c - UInt8(ascii: "0")
    case UInt8(ascii: "a")...UInt8(ascii: "f"): return c - UInt8(ascii: "a") + 10
    case UInt8(ascii: "A")...UInt8(ascii: "F"): return c - UInt8(ascii: "A") + 10
    default: return nil
    }
}

/// Little-endian writer for the wire formats (Skycoin's skyencoder layout,
/// Bitcoin's serialization).
struct ByteWriter {
    private(set) var bytes: [UInt8] = []

    mutating func u8(_ v: UInt8) { bytes.append(v) }

    mutating func u32(_ v: UInt32) {
        bytes.append(UInt8(truncatingIfNeeded: v))
        bytes.append(UInt8(truncatingIfNeeded: v >> 8))
        bytes.append(UInt8(truncatingIfNeeded: v >> 16))
        bytes.append(UInt8(truncatingIfNeeded: v >> 24))
    }

    mutating func u64(_ v: UInt64) {
        var x = v
        for _ in 0..<8 {
            bytes.append(UInt8(truncatingIfNeeded: x))
            x >>= 8
        }
    }

    /// Bitcoin's CompactSize.
    mutating func varInt(_ v: UInt64) {
        switch v {
        case ..<0xfd:
            u8(UInt8(v))
        case ...0xffff:
            u8(0xfd)
            bytes.append(UInt8(truncatingIfNeeded: v))
            bytes.append(UInt8(truncatingIfNeeded: v >> 8))
        case ...0xffff_ffff:
            u8(0xfe)
            u32(UInt32(v))
        default:
            u8(0xff)
            u64(v)
        }
    }

    mutating func append(_ b: [UInt8]) { bytes.append(contentsOf: b) }
}

/// Keccak-256 as Ethereum uses it: the original Keccak submission (padding
/// 0x01 … 0x80), NOT the FIPS 202 SHA3-256 (padding 0x06), which CryptoKit
/// would offer if it offered either. Pure Swift over Keccak-f[1600], rate
/// 1088 bits; vectors in Keccak256Tests.
enum Keccak256 {

    private static let rate = 136

    static func hash(_ message: [UInt8]) -> [UInt8] {
        var state = [UInt64](repeating: 0, count: 25)

        var data = message
        data.append(0x01)
        while data.count % rate != 0 { data.append(0) }
        data[data.count - 1] |= 0x80

        var offset = 0
        while offset < data.count {
            for lane in 0..<(rate / 8) {
                var v: UInt64 = 0
                for b in 0..<8 { v |= UInt64(data[offset + 8 * lane + b]) << (8 * UInt64(b)) }
                state[lane] ^= v
            }
            permute(&state)
            offset += rate
        }

        var out = [UInt8]()
        out.reserveCapacity(32)
        for lane in 0..<4 {
            for b in 0..<8 { out.append(UInt8(truncatingIfNeeded: state[lane] >> (8 * UInt64(b)))) }
        }
        return out
    }

    /// Keccak-f[1600]; lane (x, y) is state[x + 5y].
    private static func permute(_ a: inout [UInt64]) {
        var c = [UInt64](repeating: 0, count: 5)
        var b = [UInt64](repeating: 0, count: 25)
        for round in 0..<24 {
            // θ
            for x in 0..<5 { c[x] = a[x] ^ a[x + 5] ^ a[x + 10] ^ a[x + 15] ^ a[x + 20] }
            for x in 0..<5 {
                let d = c[(x + 4) % 5] ^ rol(c[(x + 1) % 5], 1)
                for y in 0..<5 { a[x + 5 * y] ^= d }
            }
            // ρ and π: B[y, 2x + 3y] = rot(A[x, y], r[x, y])
            for x in 0..<5 {
                for y in 0..<5 {
                    b[y + 5 * ((2 * x + 3 * y) % 5)] = rol(a[x + 5 * y], rho[x + 5 * y])
                }
            }
            // χ
            for x in 0..<5 {
                for y in 0..<5 {
                    a[x + 5 * y] = b[x + 5 * y] ^ (~b[(x + 1) % 5 + 5 * y] & b[(x + 2) % 5 + 5 * y])
                }
            }
            // ι
            a[0] ^= roundConstants[round]
        }
    }

    private static func rol(_ v: UInt64, _ n: Int) -> UInt64 {
        n == 0 ? v : (v << UInt64(n)) | (v >> UInt64(64 - n))
    }

    /// Rotation offsets r[x, y] at index x + 5y.
    private static let rho: [Int] = [
        0, 1, 62, 28, 27,
        36, 44, 6, 55, 20,
        3, 10, 43, 25, 39,
        41, 45, 15, 21, 8,
        18, 2, 61, 56, 14,
    ]

    private static let roundConstants: [UInt64] = [
        0x0000_0000_0000_0001, 0x0000_0000_0000_8082, 0x8000_0000_0000_808A, 0x8000_0000_8000_8000,
        0x0000_0000_0000_808B, 0x0000_0000_8000_0001, 0x8000_0000_8000_8081, 0x8000_0000_0000_8009,
        0x0000_0000_0000_008A, 0x0000_0000_0000_0088, 0x0000_0000_8000_8009, 0x0000_0000_8000_000A,
        0x0000_0000_8000_808B, 0x8000_0000_0000_008B, 0x8000_0000_0000_8089, 0x8000_0000_0000_8003,
        0x8000_0000_0000_8002, 0x8000_0000_0000_0080, 0x0000_0000_0000_800A, 0x8000_0000_8000_000A,
        0x8000_0000_8000_8081, 0x8000_0000_0000_8080, 0x0000_0000_8000_0001, 0x8000_0000_8000_8008,
    ]
}

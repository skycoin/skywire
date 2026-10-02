/// BIP 173 (bech32) and BIP 350 (bech32m) — segwit address encoding.
/// Straight port of the reference implementation (via the Kotlin port).
public enum Bech32 {
    private static let charset = Array("qpzry9x8gf2tvdw0s3jn54khce6mua7l".utf8)
    private static let bech32Const = 1
    private static let bech32mConst = 0x2bc8_30a3

    public enum Spec: Sendable { case bech32, bech32m }

    public struct Segwit: Equatable, Sendable {
        public let hrp: String
        public let version: Int
        public let program: [UInt8]
    }

    private static func polymod(_ values: [Int]) -> Int {
        let gen = [0x3b6a_57b2, 0x2650_8e6d, 0x1ea1_19fa, 0x3d42_33dd, 0x2a14_62b3]
        var chk = 1
        for v in values {
            let top = chk >> 25
            chk = ((chk & 0x1ff_ffff) << 5) ^ v
            for i in 0..<5 where (top >> i) & 1 == 1 {
                chk ^= gen[i]
            }
        }
        return chk
    }

    private static func hrpExpand(_ hrp: [UInt8]) -> [Int] {
        hrp.map { Int($0) >> 5 } + [0] + hrp.map { Int($0) & 31 }
    }

    private static func createChecksum(_ hrp: [UInt8], _ data: [Int], _ spec: Spec) -> [Int] {
        let values = hrpExpand(hrp) + data + [0, 0, 0, 0, 0, 0]
        let mod = polymod(values) ^ (spec == .bech32m ? bech32mConst : bech32Const)
        return (0..<6).map { (mod >> (5 * (5 - $0))) & 31 }
    }

    private static func verifyChecksum(_ hrp: [UInt8], _ data: [Int]) -> Spec? {
        switch polymod(hrpExpand(hrp) + data) {
        case bech32Const: .bech32
        case bech32mConst: .bech32m
        default: nil
        }
    }

    public static func encode(hrp: String, data: [Int], spec: Spec) -> String {
        let h = Array(hrp.utf8)
        let combined = data + createChecksum(h, data, spec)
        return hrp + "1" + String(decoding: combined.map { charset[$0] }, as: UTF8.self)
    }

    /// (hrp, data without the checksum, spec), or nil.
    public static func decode(_ bech: String) -> (hrp: String, data: [Int], spec: Spec)? {
        let scalars = Array(bech.unicodeScalars)
        if scalars.count > 90 { return nil }
        if scalars.contains(where: { $0.value < 33 || $0.value > 126 }) { return nil }
        let lower = bech.lowercased()
        if bech != lower && bech != bech.uppercased() { return nil }
        let chars = Array(lower.utf8)
        guard let pos = chars.lastIndex(of: UInt8(ascii: "1")), pos >= 1, pos + 7 <= chars.count else { return nil }
        let hrp = Array(chars[0..<pos])
        var data = [Int]()
        for c in chars[(pos + 1)...] {
            guard let d = charset.firstIndex(of: c) else { return nil }
            data.append(d)
        }
        guard let spec = verifyChecksum(hrp, data) else { return nil }
        return (String(decoding: hrp, as: UTF8.self), Array(data[0..<(data.count - 6)]), spec)
    }

    public static func convertBits(_ data: [Int], from fromBits: Int, to toBits: Int, pad: Bool) -> [Int]? {
        var acc = 0
        var bits = 0
        var out = [Int]()
        let maxv = (1 << toBits) - 1
        for value in data {
            if value < 0 || value >> fromBits != 0 { return nil }
            acc = (acc << fromBits) | value
            bits += fromBits
            while bits >= toBits {
                bits -= toBits
                out.append((acc >> bits) & maxv)
            }
            // Only the low bits still to be emitted matter; keep acc small.
            acc &= (1 << bits) - 1
        }
        if pad {
            if bits > 0 { out.append((acc << (toBits - bits)) & maxv) }
        } else if bits >= fromBits || ((acc << (toBits - bits)) & maxv) != 0 {
            return nil
        }
        return out
    }

    /// Decode a segwit address for the given hrp; enforces BIP 350's
    /// spec-per-version rule.
    public static func segwitDecode(hrp: String, address: String) -> Segwit? {
        guard let (gotHrp, data, spec) = decode(address), gotHrp == hrp else { return nil }
        if data.isEmpty || data[0] > 16 { return nil }
        guard let program = convertBits(Array(data[1...]), from: 5, to: 8, pad: false) else { return nil }
        if program.count < 2 || program.count > 40 { return nil }
        if data[0] == 0 && program.count != 20 && program.count != 32 { return nil }
        if data[0] == 0 && spec != .bech32 { return nil }
        if data[0] != 0 && spec != .bech32m { return nil }
        return Segwit(hrp: hrp, version: data[0], program: program.map { UInt8($0) })
    }

    public static func segwitEncode(hrp: String, version: Int, program: [UInt8]) -> String {
        let spec: Spec = version == 0 ? .bech32 : .bech32m
        // convertBits(8→5, pad) cannot fail on bytes.
        let data = [version] + convertBits(program.map { Int($0) }, from: 8, to: 5, pad: true)!
        return encode(hrp: hrp, data: data, spec: spec)
    }
}

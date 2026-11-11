/// Recursive-length-prefix encoding — exactly the subset a transaction
/// needs: byte strings, unsigned integers (minimal big-endian, zero = empty),
/// and lists. No decoder: this wallet only ever authors RLP, never parses it.
public enum Rlp {

    public indirect enum Item: Sendable {
        case str([UInt8])
        case lst([Item])

        /// The byte string's bytes (empty for a list).
        public var bytes: [UInt8] {
            if case .str(let b) = self { return b }
            return []
        }
    }

    public static func of(_ bytes: [UInt8]) -> Item { .str(bytes) }

    public static func of(_ value: BigUInt) -> Item { .str(value.bigEndianBytes) }

    public static func encode(_ item: Item) -> [UInt8] {
        switch item {
        case .str(let b):
            if b.count == 1 && b[0] < 0x80 { return b }
            return lengthPrefix(b.count, offset: 0x80) + b
        case .lst(let items):
            let body = items.flatMap { encode($0) }
            return lengthPrefix(body.count, offset: 0xc0) + body
        }
    }

    private static func lengthPrefix(_ length: Int, offset: UInt8) -> [UInt8] {
        if length < 56 { return [offset + UInt8(length)] }
        let lenBytes = BigUInt(UInt64(length)).bigEndianBytes
        return [offset + 55 + UInt8(lenBytes.count)] + lenBytes
    }
}

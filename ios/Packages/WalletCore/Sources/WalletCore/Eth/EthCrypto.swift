/// Ethereum key and address arithmetic: Keccak-256, the BIP 44 Ethereum
/// path, and EIP-55 checksummed addresses. Signing itself stays in
/// Secp256k1 — an Ethereum signature is the same compact recoverable form
/// Skycoin uses, taken over a different hash.
public enum EthCrypto {

    public static func keccak256(_ data: [UInt8]) -> [UInt8] { Keccak256.hash(data) }

    /// m/44'/60'/0'/0 — the node every index-i external address hangs off.
    public static func accountKey(mnemonic: String) throws -> Bip32.ExtKey {
        try Bip32.derive(
            Bip32.master(seed: Bip39.toSeed(mnemonic)),
            path: [44 | Bip32.hardened, 60 | Bip32.hardened, Bip32.hardened, 0]
        )
    }

    public static func key(_ account: Bip32.ExtKey, index: UInt32) throws -> Bip32.ExtKey {
        try Bip32.ckdPriv(account, index: index)
    }

    /// The 20 address bytes behind a public key: Keccak-256 of the raw
    /// 64-byte point (the 0x04 prefix of the uncompressed encoding excluded),
    /// low 20.
    public static func addressBytes(pubCompressed: [UInt8]) throws -> [UInt8] {
        guard let uncompressed = Secp256k1.decompress(pubCompressed) else { throw WalletCoreError("invalid public key") }
        return Array(keccak256(Array(uncompressed[1...]))[12..<32])
    }

    /// EIP-55 mixed-case form — the only form this wallet ever prints.
    public static func checksumAddress(_ bytes: [UInt8]) throws -> String {
        try require(bytes.count == 20, "an address is 20 bytes")
        let hex = Array(bytes.hex.utf8)
        let hash = keccak256(hex)
        var out = Array("0x".utf8)
        for (i, c) in hex.enumerated() {
            // Uppercase where the hash nibble at the same position is ≥ 8.
            let nibble = (hash[i / 2] >> (i % 2 == 0 ? 4 : 0)) & 0xf
            out.append(c >= UInt8(ascii: "a") && c <= UInt8(ascii: "f") && nibble >= 8 ? c - 32 : c)
        }
        return String(decoding: out, as: UTF8.self)
    }

    public static func address(pubCompressed: [UInt8]) throws -> String {
        try checksumAddress(addressBytes(pubCompressed: pubCompressed))
    }

    /// The 20 bytes behind any accepted spelling of an address, or nil.
    /// All-lowercase and all-uppercase hex pass unchecked (they carry no
    /// checksum); mixed case must be the exact EIP-55 form — a mixed-case
    /// address that fails its own checksum is a typo, not a preference.
    public static func parseAddress(_ address: String) -> [UInt8]? {
        guard address.hasPrefix("0x") else { return nil }
        let body = String(address.dropFirst(2))
        guard body.utf8.count == 40, let bytes = [UInt8](hex: body) else { return nil }
        let hasLower = body.utf8.contains { $0 >= UInt8(ascii: "a") && $0 <= UInt8(ascii: "f") }
        let hasUpper = body.utf8.contains { $0 >= UInt8(ascii: "A") && $0 <= UInt8(ascii: "F") }
        if hasLower && hasUpper && (try? checksumAddress(bytes)) != "0x" + body { return nil }
        return bytes
    }

    public static func isValidAddress(_ address: String) -> Bool { parseAddress(address) != nil }
}

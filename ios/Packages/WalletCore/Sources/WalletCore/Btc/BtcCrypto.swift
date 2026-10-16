/// BIP 84 wallet keys: m/84'/0'/0'/change/index, native segwit (bc1q…).
/// The phrase runs through BIP 39 PBKDF2 here — unlike the Skycoin chain,
/// where the phrase bytes are the seed directly.
public enum Bip84 {
    private static let purpose: [UInt32] = [84 | Bip32.hardened, 0 | Bip32.hardened, 0 | Bip32.hardened]

    public static func accountKey(mnemonic: String) throws -> Bip32.ExtKey {
        try Bip32.derive(Bip32.master(seed: Bip39.toSeed(mnemonic)), path: purpose)
    }

    public static func key(_ account: Bip32.ExtKey, change: UInt32, index: UInt32) throws -> Bip32.ExtKey {
        try Bip32.ckdPriv(Bip32.ckdPriv(account, index: change), index: index)
    }

    public static func address(pubKey: [UInt8]) -> String {
        Bech32.segwitEncode(hrp: "bc", version: 0, program: Hashes.hash160(pubKey))
    }
}

/// Destination-address decoding: every mainnet form a send can target.
public enum BtcAddress {

    /// scriptPubKey for the address, or nil when the address is invalid.
    public static func scriptPubKey(_ address: String) -> [UInt8]? {
        let a = address.trimmingCharacters(in: .whitespacesAndNewlines)
        if a.isEmpty { return nil }

        // bech32 / bech32m
        if a.lowercased().hasPrefix("bc1") {
            guard let sw = Bech32.segwitDecode(hrp: "bc", address: a) else { return nil }
            if sw.version == 0 && (sw.program.count == 20 || sw.program.count == 32) {
                return [0x00, UInt8(sw.program.count)] + sw.program
            }
            if (1...16).contains(sw.version) && (2...40).contains(sw.program.count) {
                return [UInt8(0x50 + sw.version), UInt8(sw.program.count)] + sw.program
            }
            return nil
        }

        // base58check
        guard let raw = Base58.decode(a), raw.count == 25 else { return nil }
        let body = Array(raw[0..<21])
        if Array(Hashes.doubleSha256(body)[0..<4]) != Array(raw[21..<25]) { return nil }
        let hash = Array(body[1..<21])
        switch body[0] {
        case 0x00: return [0x76, 0xa9, 0x14] + hash + [0x88, 0xac] // P2PKH
        case 0x05: return [0xa9, 0x14] + hash + [0x87] // P2SH
        default: return nil
        }
    }

    public static func isValid(_ address: String) -> Bool { scriptPubKey(address) != nil }

    /// Standard-output size in weight units (8-byte value + script with its varint).
    public static func outputWeight(scriptPubKey: [UInt8]) -> Int { (8 + 1 + scriptPubKey.count) * 4 }
}

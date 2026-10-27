/// The shape of a reward address the visor takes: a BIP 44 account xpub. The
/// visor decodes it properly; this only catches a pasted single address or a
/// cut-off key before the round trip. The port of Android's core/RewardXpub.kt.
public enum RewardXpub {
    public static func looksValid(_ s: String) -> Bool {
        s.hasPrefix("xpub") && (100...120).contains(s.count)
    }
}

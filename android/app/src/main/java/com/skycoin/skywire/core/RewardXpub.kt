package com.skycoin.skywire.core

/**
 * The shape of a reward address the visor takes: a BIP 44 account xpub. The
 * visor decodes it properly; this only catches a pasted single address or a
 * cut-off key before the round trip.
 */
object RewardXpub {
    fun looksValid(s: String): Boolean = s.startsWith("xpub") && s.length in 100..120
}

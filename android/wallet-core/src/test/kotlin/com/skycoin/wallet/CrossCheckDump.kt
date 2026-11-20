package com.skycoin.wallet

import com.skycoin.wallet.btc.Bip84
import com.skycoin.wallet.btc.BtcAddress
import com.skycoin.wallet.btc.BtcTxn
import com.skycoin.wallet.eth.EthCrypto
import com.skycoin.wallet.eth.EthTxn
import com.skycoin.wallet.skycoin.SkycoinCreate
import com.skycoin.wallet.skycoin.SkycoinCrypto
import org.junit.Assume.assumeTrue
import java.io.File
import java.math.BigInteger
import kotlin.test.Test

/**
 * The Kotlin half of ios/scripts/wallet-crosscheck.sh: for three fixed
 * seeds, the addresses every chain derives and one fully signed transaction
 * per chain (Skycoin, Bitcoin, Ethereum, an ERC-20 transfer), written to the
 * file WALLET_CROSSCHECK_OUT names. The iOS port's CrossCheckDumpTests
 * writes the same lines from the same inputs; the script diffs the two
 * files, which must be identical. Skipped when the variable is unset.
 *
 * The transactions are built from fixed inputs, not a node, so they are
 * the same every run: every signature here is RFC 6979.
 */
class CrossCheckDump {

    @Test
    fun dump() {
        val out = System.getenv("WALLET_CROSSCHECK_OUT")
        assumeTrue(!out.isNullOrEmpty())
        val lines = ArrayList<String>()
        SEEDS.forEachIndexed { i, mnemonic -> dumpSeed(i + 1, mnemonic, lines) }
        File(out!!).writeText(lines.joinToString("\n") + "\n")
    }

    private fun dumpSeed(n: Int, mnemonic: String, lines: MutableList<String>) {
        lines += "seed $n: $mnemonic"
        lines += "seed $n valid: ${Bip39.validate(mnemonic)}"

        // Skycoin: two outputs on addresses 0 and 1, spent to address 2,
        // change back to address 0.
        val keys = SkycoinCrypto.generateKeyPairs(mnemonic.toByteArray(Charsets.UTF_8), 3)
        val sky = keys.map { SkycoinCrypto.addressFromPubKey(it.public) }
        sky.forEachIndexed { i, a -> lines += "SKY address $i: $a" }
        val uxid0 = Hashes.sha256("crosscheck $n ux 0".toByteArray())
        val uxid1 = Hashes.sha256("crosscheck $n ux 1".toByteArray())
        val created = SkycoinCreate.create(
            unspents = listOf(
                SkycoinCreate.UxBalance(uxid0, uxid0.toHex(), 10uL, sky[0], 5_000_000uL, 100uL, 120uL),
                SkycoinCreate.UxBalance(uxid1, uxid1.toHex(), 20uL, sky[1], 2_000_000uL, 40uL, 50uL),
            ),
            to = listOf(SkycoinCreate.Destination(sky[2], 6_000_000uL)),
            changeAddress = sky[0],
            burnFactor = 10u,
        )
        val secretOf = sky.indices.associate { sky[it] to keys[it].secret }
        created.txn.signInputs(
            created.txn.inputs.map { uxid ->
                secretOf.getValue(created.spends.first { it.hash.contentEquals(uxid) }.address)
            },
        )
        lines += "SKY plan: fee ${created.feeHours} hours-out ${created.hoursToDestinations} " +
            "change ${created.changeCoins}/${created.changeHours}"
        lines += "SKY tx: ${created.txn.serializeHex()}"
        lines += "SKY txid: ${created.txn.txidHex()}"

        // Bitcoin: one input on receive 0, one on change 0; paid to receive
        // 2 with change to change 1.
        val account = Bip84.accountKey(mnemonic)
        val receive = (0 until 3).map { Bip84.address(Bip84.key(account, 0, it).pubKey()) }
        val change = (0 until 2).map { Bip84.address(Bip84.key(account, 1, it).pubKey()) }
        receive.forEachIndexed { i, a -> lines += "BTC receive $i: $a" }
        change.forEachIndexed { i, a -> lines += "BTC change $i: $a" }
        val k0 = Bip84.key(account, 0, 0)
        val c0 = Bip84.key(account, 1, 0)
        val btc = BtcTxn(
            inputs = listOf(
                BtcTxn.Input(Hashes.sha256("crosscheck $n btc 0".toByteArray()).toHex(), 1, 150_000uL, Hashes.hash160(k0.pubKey())),
                BtcTxn.Input(Hashes.sha256("crosscheck $n btc 1".toByteArray()).toHex(), 0, 80_000uL, Hashes.hash160(c0.pubKey())),
            ),
            outputs = listOf(
                BtcTxn.Output(200_000uL, BtcAddress.scriptPubKey(receive[2])!!),
                BtcTxn.Output(27_000uL, BtcAddress.scriptPubKey(change[1])!!),
            ),
        )
        btc.sign(listOf(k0.key, c0.key))
        lines += "BTC tx: ${btc.serialize().toHex()}"
        lines += "BTC txid: ${btc.txid()} vsize ${btc.vsize()}"

        // Ethereum: 0.0123 ETH from address 0 to address 1, and 1.234567
        // USDT from address 0 to address 2.
        val ethAccount = EthCrypto.accountKey(mnemonic)
        val eth = (0 until 3).map { EthCrypto.address(EthCrypto.key(ethAccount, it).pubKey()) }
        eth.forEachIndexed { i, a -> lines += "ETH address $i: $a" }
        val sender = EthCrypto.key(ethAccount, 0).key
        val native = EthTxn(
            chainId = BigInteger.ONE,
            nonce = BigInteger.valueOf(n.toLong()),
            maxPriorityFeePerGas = BigInteger.valueOf(1_500_000_000),
            maxFeePerGas = BigInteger.valueOf(30_000_000_000),
            gasLimit = BigInteger.valueOf(21_000),
            to = EthCrypto.parseAddress(eth[1])!!,
            value = BigInteger("12300000000000000"),
            data = ByteArray(0),
        ).signed(sender)
        lines += "ETH tx: ${native.raw.toHex()}"
        lines += "ETH txid: 0x${native.hash.toHex()}"
        val token = EthTxn(
            chainId = BigInteger.ONE,
            nonce = BigInteger.valueOf(n.toLong() + 1),
            maxPriorityFeePerGas = BigInteger.valueOf(1_500_000_000),
            maxFeePerGas = BigInteger.valueOf(30_000_000_000),
            gasLimit = BigInteger.valueOf(65_000),
            to = EthCrypto.parseAddress(USDT)!!,
            value = BigInteger.ZERO,
            data = EthTxn.erc20Transfer(EthCrypto.parseAddress(eth[2])!!, BigInteger.valueOf(1_234_567)),
        ).signed(sender)
        lines += "ERC20 tx: ${token.raw.toHex()}"
        lines += "ERC20 txid: 0x${token.hash.toHex()}"
    }

    private companion object {
        /** The two Trezor 12-word vectors and their 24-word one (entropy 0x80 × 32). */
        val SEEDS = listOf(
            "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
            "legal winner thank year wave sausage worth useful legal winner thank yellow",
            "letter advice cage absurd amount doctor acoustic avoid letter advice cage absurd " +
                "amount doctor acoustic avoid letter advice cage absurd amount doctor acoustic bless",
        )
        const val USDT = "0xdAC17F958D2ee523a2206206994597C13D831ec7"
    }
}

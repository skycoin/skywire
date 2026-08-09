package com.skycoin.wallet

import com.skycoin.wallet.btc.Bip84
import com.skycoin.wallet.btc.BtcAddress
import com.skycoin.wallet.btc.BtcTxn
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNull
import kotlin.test.assertTrue

class Bip39VectorsTest {

    // Trezor reference vectors (also present in the Go repo's bip39 tests).
    @Test
    fun entropyToMnemonicAndSeed() {
        val v1 = Bip39.entropyToMnemonic(ByteArray(16))
        assertEquals(
            "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
            v1,
        )
        assertEquals(
            "c55257c360c07c72029aebc1b53c05ed0362ada38ead3e3e9efa3708e53495531f09a6987599d18264c1e1c92f2cf141630c7a3c4ab7c81b2f001698e7463b04",
            Bip39.toSeed(v1, "TREZOR").toHex(),
        )

        val v2 = Bip39.entropyToMnemonic("7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f7f".hexToBytes())
        assertEquals("legal winner thank year wave sausage worth useful legal winner thank yellow", v2)
        assertEquals(
            "2e8905819b8723fe2c1d161860e5ee1830318dbf49a83bd451cfb8440c28bd6fa457fe1296106559a3c80937a1c1069be3a3a5bd381ee6260e8d9739fce1f607",
            Bip39.toSeed(v2, "TREZOR").toHex(),
        )
    }

    @Test
    fun validation() {
        assertTrue(Bip39.validate("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"))
        // Word swap breaks the checksum.
        assertTrue(!Bip39.validate("about abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon"))
        // Off-list word.
        assertTrue(!Bip39.validate("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon aboot"))
        // Wrong count.
        assertTrue(!Bip39.validate("abandon abandon abandon"))
        // Fresh phrases validate and differ.
        val a = Bip39.newMnemonic()
        val b = Bip39.newMnemonic()
        assertTrue(Bip39.validate(a))
        assertTrue(a != b)
        assertEquals(12, a.split(" ").size)
    }
}

class Bip84FixtureTest {

    @Serializable
    private class Bip84Fixture(
        val mnemonic: String,
        val bip39SeedHex: String,
        val accountPrivHex: String,
        val receivePrivHex0: String,
        val receive: List<String>,
        val change: List<String>,
    )

    @Serializable
    private class Fixtures(val bip84: Bip84Fixture)

    private val fx: Bip84Fixture = Json { ignoreUnknownKeys = true }.decodeFromString(
        Fixtures.serializer(),
        javaClass.getResourceAsStream("/skycoin-fixtures.json")!!.bufferedReader().readText(),
    ).bip84

    @Test
    fun derivationChainMatchesReference() {
        assertEquals(fx.bip39SeedHex, Bip39.toSeed(fx.mnemonic).toHex())
        val account = Bip84.accountKey(fx.mnemonic)
        assertEquals(fx.accountPrivHex, account.key.toHex())
        assertEquals(fx.receivePrivHex0, Bip84.key(account, 0, 0).key.toHex())
        fx.receive.forEachIndexed { i, addr ->
            assertEquals(addr, Bip84.address(Bip84.key(account, 0, i).pubKey()), "receive $i")
        }
        fx.change.forEachIndexed { i, addr ->
            assertEquals(addr, Bip84.address(Bip84.key(account, 1, i).pubKey()), "change $i")
        }
        // The canonical BIP 84 first address, stated in the BIP itself.
        assertEquals("bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu", fx.receive[0])
    }
}

class BtcAddressTest {

    @Test
    fun scriptForms() {
        // Canonical BIP 173 v0 example — hash160 of the generator pubkey.
        assertEquals(
            "0014751e76e8199196d454941c45d1b3a323f1433bd6",
            BtcAddress.scriptPubKey("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t4")!!.toHex(),
        )
        // Case-insensitive bech32.
        assertEquals(
            "0014751e76e8199196d454941c45d1b3a323f1433bd6",
            BtcAddress.scriptPubKey("BC1QW508D6QEJXTDG4Y5R3ZARVARY0C5XW7KV8F3T4")!!.toHex(),
        )
        // Damaged checksum.
        assertNull(BtcAddress.scriptPubKey("bc1qw508d6qejxtdg4y5r3zarvary0c5xw7kv8f3t5"))
        // A Skycoin address is not a Bitcoin address.
        assertNull(BtcAddress.scriptPubKey("QmnwkcchkjgduYeeMqHaXhgEFKKYiFpc4"))
        // Taproot outputs (bech32m) are spendable-to.
        val p2tr = BtcAddress.scriptPubKey(Bech32.segwitEncode("bc", 1, ByteArray(32) { 7 }))
        assertEquals(0x51, p2tr!![0].toInt())
        // v1 with a bech32 (not bech32m) checksum must fail.
        assertNull(BtcAddress.scriptPubKey(Bech32.encode("bc", intArrayOf(1) + Bech32.convertBits(IntArray(32) { 7 }, 8, 5, true)!!, Bech32.Spec.BECH32)))
    }

    @Test
    fun base58Forms() {
        // Round-trip our own construction of a P2PKH address.
        val hash = Hashes.hash160("test".toByteArray())
        val body = byteArrayOf(0x00) + hash
        val addr = Base58.encode(body + Hashes.doubleSha256(body).copyOfRange(0, 4))
        val script = BtcAddress.scriptPubKey(addr)!!
        assertEquals(25, script.size)
        assertEquals(0x76, script[0].toInt())
    }
}

/** The BIP 143 native-P2WPKH example, values verbatim from the BIP. */
class Bip143Test {

    private val unsignedHex =
        "0100000002fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad969f0000000000eeffffffef51e1b804cc89d182d279655c3aa89e815b1b309fe287d9b2b55d57b90ec68a0100000000ffffffff02202cb206000000001976a9148280b37df378db99f66f85c95a783a76ac7a6d5988ac9093510d000000001976a9143bde42dbee7e4dbe6a21b2d50ce2f0167faa815988ac11000000"

    private fun txidDisplay(serializedLe: String): String =
        serializedLe.chunked(2).reversed().joinToString("")

    private fun buildTxn(): BtcTxn {
        val key = "619c335025c7f4012e556c2a58b2506e30b8511b53ade95ea316fd8c3286feb9".hexToBytes()
        val pub = Secp256k1.pubKeyFromSecKey(key)
        return BtcTxn(
            inputs = listOf(
                BtcTxn.Input(
                    txid = txidDisplay("fff7f7881a8099afa6940d42d1e7f6362bec38171ea3edf433541db4e4ad969f"),
                    vout = 0,
                    valueSats = 625000000uL, // 6.25 BTC P2PK input, irrelevant to input 1's sighash
                    pubKeyHash = ByteArray(20),
                    sequence = 0xFFFFFFEEL,
                ),
                BtcTxn.Input(
                    txid = txidDisplay("ef51e1b804cc89d182d279655c3aa89e815b1b309fe287d9b2b55d57b90ec68a"),
                    vout = 1,
                    valueSats = 600000000uL,
                    pubKeyHash = Hashes.hash160(pub),
                    sequence = 0xFFFFFFFFL,
                ),
            ),
            outputs = listOf(
                BtcTxn.Output(
                    112340000uL,
                    "76a9148280b37df378db99f66f85c95a783a76ac7a6d5988ac".hexToBytes(),
                ),
                BtcTxn.Output(
                    223450000uL,
                    "76a9143bde42dbee7e4dbe6a21b2d50ce2f0167faa815988ac".hexToBytes(),
                ),
            ),
            version = 1,
            locktime = 0x11,
        )
    }

    @Test
    fun unsignedSerializationMatches() {
        assertEquals(unsignedHex, buildTxn().serializeStripped().toHex())
    }

    @Test
    fun sighashMatches() {
        assertEquals(
            "c37af31116d1b27caf68aae9e3ac82f1477929014d5b917657d0eb49478cb670",
            buildTxn().sighash(1).toHex(),
        )
    }

    @Test
    fun signatureMatchesByteForByte() {
        val key = "619c335025c7f4012e556c2a58b2506e30b8511b53ade95ea316fd8c3286feb9".hexToBytes()
        val txn = buildTxn()
        val der = Secp256k1.signDer(txn.sighash(1), key) + byteArrayOf(0x01)
        // The BIP's example signature is RFC 6979-deterministic — ours must equal it.
        assertEquals(
            "304402203609e17b84f6a7d30c80bfa610b5b4542f32a8a0d5447a12fb1366d7f01cc44a0220573a954c4518331561406f90300e8f3358f51928d43c212a8caed02de67eebee01",
            der.toHex(),
        )
    }

    @Test
    fun witnessSerializationShape() {
        val key = "619c335025c7f4012e556c2a58b2506e30b8511b53ade95ea316fd8c3286feb9".hexToBytes()
        val txn = buildTxn()
        val pub = Secp256k1.pubKeyFromSecKey(key)
        val der = Secp256k1.signDer(txn.sighash(1), key) + byteArrayOf(0x01)
        txn.inputs[1].witness = listOf(der, pub)
        val full = txn.serialize().toHex()
        assertTrue(full.startsWith("01000000000102"), "segwit marker+flag present")
        assertTrue(full.contains(der.toHex()), "witness signature embedded")
        assertTrue(full.endsWith("11000000"), "locktime last")
        // txid never changes with witness data.
        assertEquals(
            Hashes.doubleSha256(unsignedHex.hexToBytes()).reversedArray().toHex(),
            txn.txid(),
        )
        // vsize: stripped both times the discount says so.
        assertTrue(txn.vsize() < txn.serialize().size)
    }
}

class AmountsTest {
    @Test
    fun parseAndFormat() {
        assertEquals(2500000uL, Amounts.parse("2.5", 6))
        assertEquals(1uL, Amounts.parse("0.000001", 6))
        assertNull(Amounts.parse("0.0000001", 6))
        assertNull(Amounts.parse("1..2", 6))
        assertNull(Amounts.parse("-1", 6))
        assertNull(Amounts.parse("abc", 6))
        assertEquals(2uL, Amounts.parse("0.00000002", 8))
        assertEquals("1,204.500", Amounts.format(1204500000uL, 6, 3))
        assertEquals("0.04821930", Amounts.format(4821930uL, 8, 8))
        assertEquals("12,500", Amounts.format(12500000000uL, 6, 0))
        assertEquals(3, Amounts.decimals("1.204"))
        assertEquals(0, Amounts.decimals("1204"))
    }
}

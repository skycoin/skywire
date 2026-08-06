package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkycoinCrypto
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.util.Base64
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertNotNull
import kotlin.test.assertTrue

/**
 * Golden vectors from the reference implementation's cipher testsuite:
 * seed → keypair chain → addresses, plus stored signatures that must recover
 * to the stored public keys through our port.
 */
class SkycoinGoldenTest {

    @Serializable
    private class GoldenKey(
        val address: String,
        val secret: String,
        val public: String,
        val signatures: List<String> = emptyList(),
    )

    @Serializable
    private class GoldenSeed(val seed: String, val keys: List<GoldenKey>)

    @Serializable
    private class GoldenHashes(val hashes: List<String>)

    private val json = Json { ignoreUnknownKeys = true }

    private fun load(name: String): String =
        javaClass.getResourceAsStream("/$name")!!.bufferedReader().readText()

    private val inputHashes: List<ByteArray> by lazy {
        json.decodeFromString(GoldenHashes.serializer(), load("input-hashes.golden"))
            .hashes.map { it.hexToBytes() }
    }

    private fun checkSeedFile(name: String) {
        val golden = json.decodeFromString(GoldenSeed.serializer(), load(name))
        val seed = Base64.getDecoder().decode(golden.seed)
        val keys = SkycoinCrypto.generateKeyPairs(seed, golden.keys.size)

        golden.keys.forEachIndexed { i, expected ->
            assertEquals(expected.secret, keys[i].secret.toHex(), "secret $i of $name")
            assertEquals(expected.public, keys[i].public.toHex(), "public $i of $name")
            assertEquals(expected.address, SkycoinCrypto.addressFromPubKey(keys[i].public), "address $i of $name")

            expected.signatures.forEachIndexed { j, sigHex ->
                val sig = sigHex.hexToBytes()
                val recovered = Secp256k1.recoverCompact(inputHashes[j], sig)
                assertNotNull(recovered, "recover sig $j of key $i in $name")
                assertEquals(expected.public, recovered.toHex(), "recovered pubkey sig $j key $i in $name")
            }

            // Our own signature over each hash must verify and stay canonical.
            inputHashes.forEach { hash ->
                val sig = Secp256k1.signCompact(hash, keys[i].secret)
                assertTrue(Secp256k1.verifyCompact(hash, sig, keys[i].public), "own sig verifies")
                assertTrue((sig[32].toInt() and 0x80) == 0, "own sig is low-S")
            }
        }
    }

    @Test fun seedFile0() = checkSeedFile("seed-0000.golden")
    @Test fun seedFile1() = checkSeedFile("seed-0001.golden")
    @Test fun seedFile2() = checkSeedFile("seed-0002.golden")

    @Test
    fun addressCodecRejectsDamage() {
        val addr = "QmnwkcchkjgduYeeMqHaXhgEFKKYiFpc4"
        assertTrue(SkycoinCrypto.isValidAddress(addr))
        assertTrue(!SkycoinCrypto.isValidAddress(addr.dropLast(1) + "5"))
        assertTrue(!SkycoinCrypto.isValidAddress(""))
        assertTrue(!SkycoinCrypto.isValidAddress("bc1qcr8te4kr609gcawutmrza0j4xv80jy8z306fyu"))
        assertTrue(!SkycoinCrypto.isValidAddress("0OIl"))
    }
}

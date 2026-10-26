package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFailsWith

/**
 * A fiber send to several destinations. The expected inputs, outputs and
 * inner hashes are what skycoin's transaction.Create returns for the same
 * unspent outputs at the version go.mod pins, with change to the first address.
 */
class MultiDestinationTest {

    private val seed = KeySource("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
    private val first = "2EFSW8YqFDG3x6mwfbDjBk6M9eMc6WUoFHZ"
    private val second = "24qhX1ZC9F8iDaT6R13j4fgJyQvumW2izpm"
    private val third = "oJAsEpQrwQdVs6D12ufJ4T25EcCQYg9XFX"
    private val fourth = "Zr7NGdVCLTPCbCrCBeYQFYHt389XhEUcfk"
    private val spent = "2c4433f46d6078b329217c2ceac3e31afae11533d5f63e49ea70b1458048b2a1"

    private val outputs = """{"head_outputs":[
        {"hash":"0c238c905d91745963347b93e44436a7fa5daefeabd16ea34c04bdc8d5270869","block_seq":5,"address":"$first","coins":"10.000000","hours":100,"calculated_hours":100},
        {"hash":"$spent","block_seq":6,"address":"$second","coins":"100.000000","hours":1000,"calculated_hours":1000}]}"""

    private val core = SkyFiberWalletCore(
        "http://node.test",
        OkHttpClient.Builder().addInterceptor { chain ->
            val body = when (val path = chain.request().url.encodedPath) {
                "/api/v1/health" -> """{"user_verify_transaction":{"burn_factor":10,"max_transaction_size":32768,"max_decimals":3}}"""
                "/api/v1/outputs" -> outputs
                else -> error("unexpected request $path")
            }
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body(body.toResponseBody("application/json".toMediaType())).build()
        }.build(),
    )
    private val book = AddressBook(receive = listOf(first, second), change = emptyList())

    private fun innerHash(plan: TxPlan) = core.signTx(seed, plan).rawHex.substring(10, 74)

    @Test
    fun automaticHoursFollowTheCoins() = runBlocking {
        val plan = core.buildTx(seed, book, third, 20_000_000uL, null, false, more = listOf(Recipient(fourth, 5_000_000uL)))
        assertEquals(25_000_000uL, plan.amount)
        assertEquals(
            listOf(Triple(third, 20_000_000uL, 360uL), Triple(fourth, 5_000_000uL, 90uL)),
            plan.recipients.map { Triple(it.address, it.amount, it.hours) },
        )
        assertEquals(75_000_000uL, plan.changeAmount)
        assertEquals(450uL, plan.hoursChange)
        assertEquals("8cdc4be4cb7e4424c9bb4a500075a005c118775c4d3dc281cad56bed133e4309", innerHash(plan))
    }

    @Test
    fun manualHoursGoToEachDestination() = runBlocking {
        val plan = core.buildTx(
            seed, book, third, 20_000_000uL, null, false,
            hoursTo = 50uL,
            more = listOf(Recipient(fourth, 5_000_000uL, 30uL)),
        )
        assertEquals(listOf(50uL, 30uL), plan.recipients.map { it.hours })
        assertEquals(820uL, plan.hoursChange)
        assertEquals("13c23f5b5bf3144ce2efe6efd6f2cc552c5670201a6a94c8aaba1e5ca6c4f1f9", innerHash(plan))
    }

    @Test
    fun changeGoesToTheAddressAsked() = runBlocking {
        val plan = core.buildTx(seed, book, third, 20_000_000uL, null, false, changeAddress = fourth)
        assertEquals(fourth, plan.changeAddress)
        assertEquals(80_000_000uL, plan.changeAmount)
        assertEquals("79a463487be5b9083d1f0c330deed5160210fb171cb4745b535e989dde72bb4b", innerHash(plan))
    }

    @Test
    fun shareFactorSetsTheHoursSentOnward() = runBlocking {
        val cases = listOf(
            Triple("0.1", listOf(72uL, 18uL, 810uL), "c6d604ec6052bb8dd0358eb0f0be54d40a581a3cbad164a5e8cb0e553f87173f"),
            Triple("0.25", listOf(180uL, 45uL, 675uL), "5928013c3b40aef3dce60d13fa2ce49d4d230bfbc51db92d56efc43b6c4ea101"),
            Triple("0.73", listOf(526uL, 131uL, 243uL), "ef56f22e3a3e42f69bc161b0c2c398c701c4493c05a62bc8b3a703561e93f164"),
            Triple("1", listOf(720uL, 180uL, 0uL), "1c672237d11bb4b948f82249a4f7fee9791266cba872f11a97f29fdcf9c8d0b9"),
        )
        for ((share, hours, inner) in cases) {
            val plan = core.buildTx(
                seed, book, third, 20_000_000uL, null, false,
                more = listOf(Recipient(fourth, 5_000_000uL)),
                shareFactor = java.math.BigDecimal(share),
            )
            assertEquals(hours, plan.recipients.map { it.hours } + plan.hoursChange, "share $share")
            assertEquals(inner, innerHash(plan), "share $share")
        }
    }

    // skycoin's transaction.Create output for the two-destination send above, unsigned.
    private val goUnsigned = "01010000008cdc4be4cb7e4424c9bb4a500075a005c118775c4d3dc281cad56bed133e4309" +
        "01000000" + "00".repeat(65) + "01000000" + spent +
        "0300000000730e4768ecc67163e20a7107c3814a18700ccb90002d31010000000068010000000000000051a19bde64aeff9c" +
        "6515a7886838340716cfd7ba404b4c00000000005a0000000000000000b111ba744917645301e900a180feba35191a0551" +
        "c068780400000000c201000000000000"

    @Test
    fun unsignedMatchesTheReferenceAndSignsLikeASend() = runBlocking {
        val plan = core.buildTx(seed, book, third, 20_000_000uL, null, false, more = listOf(Recipient(fourth, 5_000_000uL)))
        assertEquals(goUnsigned, core.unsignedHex(plan))
        // Signatures are deterministic (RFC 6979), so signing the pasted
        // transaction gives the bytes the send itself would have broadcast.
        assertEquals(core.signTx(seed, plan).rawHex, core.signRaw(seed, book, goUnsigned).rawHex)
        assertEquals(goUnsigned, core.unsignedHex(plan), "signing must not change the unsigned form")
    }

    @Test
    fun rawTransactionsAreChecked() = runBlocking {
        assertFailsWith<WalletException.BadTransaction> { core.signRaw(seed, book, goUnsigned.dropLast(2)) }
        assertFailsWith<WalletException.BadTransaction> { core.signRaw(seed, book, goUnsigned + "00") }
        // A changed output no longer matches the inner hash.
        assertFailsWith<WalletException.BadTransaction> { core.signRaw(seed, book, goUnsigned.replace("68010000", "69010000")) }
        assertFailsWith<WalletException.BadTransaction> { core.broadcastRaw(goUnsigned) }
        val stranger = AddressBook(receive = listOf(first), change = emptyList())
        assertFailsWith<WalletException.NotInWallet> { core.signRaw(seed, stranger, goUnsigned) }
        Unit
    }

    @Test
    fun hoursForSomeDestinationsOnlyAreRefused() {
        assertFailsWith<WalletException.InvalidAmount> {
            runBlocking {
                core.buildTx(seed, book, third, 20_000_000uL, null, false, more = listOf(Recipient(fourth, 5_000_000uL, 30uL)))
            }
        }
    }

    @Test
    fun maxWithSeveralDestinationsIsRefused() {
        assertFailsWith<WalletException.InvalidAmount> {
            runBlocking {
                core.buildTx(seed, book, third, 0uL, null, true, more = listOf(Recipient(fourth, 5_000_000uL)))
            }
        }
    }
}

package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import kotlinx.coroutines.runBlocking
import okhttp3.FormBody
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertTrue

/** Finding used addresses past a wallet's own, as an xpub paid by the reward system needs. */
class AddressScanTest {

    private val keys = KeySource(
        "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about",
        "",
        Derivation.BIP44,
    )
    private val book = SkyFiberWalletCore("http://127.0.0.1:1", OkHttpClient()).deriveAddresses(keys, 120, 30)

    private val queries = mutableListOf<Pair<String, Int>>()

    /** A node where [used] addresses have each been paid once. */
    private fun core(used: Set<String>) = SkyFiberWalletCore(
        "http://node.test",
        OkHttpClient.Builder().addInterceptor { chain ->
            val req = chain.request()
            val form = req.body as? FormBody
            val addrs = (form?.let { f -> (0 until f.size).first { f.name(it) == "addrs" }.let(f::value) }
                ?: req.url.queryParameter("addrs").orEmpty()).split(',')
            queries += req.method to addrs.size
            val body = when (req.url.encodedPath) {
                "/api/v1/csrf" -> """{"csrf_token":"t"}"""
                "/api/v1/transactions" -> addrs.filter { it in used }.joinToString(",", "[", "]") {
                    """{"status":{"confirmed":true,"height":1},"txn":{"txid":"$it",
                    "inputs":[{"uxid":"u","owner":"elsewhere","coins":"1.000000"}],
                    "outputs":[{"dst":"$it","coins":"1.000000"}]}}"""
                }
                "/api/v1/outputs" -> "{}"
                "/api/v1/balance" -> """{"confirmed":{"coins":1,"hours":0},"predicted":{"coins":1,"hours":0}}"""
                else -> error("unexpected ${req.url}")
            }
            Response.Builder().request(req).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body(body.toResponseBody("application/json".toMediaType())).build()
        }.build(),
    )

    @Test
    fun aRestoreFindsUsedAddressesAcrossGapsUnderTwenty() = runBlocking {
        val used = setOf(book.receive[0], book.receive[3], book.receive[22], book.change[5])
        assertEquals(23 to 6, core(used).scanUsed(keys))
    }

    @Test
    fun aGapOfTwentyEndsTheScan() = runBlocking {
        val used = setOf(book.receive[0], book.receive[21])
        assertEquals(1 to 0, core(used).scanUsed(keys))
    }

    @Test
    fun aWalletHoldingOneAddressFindsRewardsPaidPastIt() = runBlocking {
        // The reward system pays each reward to the xpub's next unused address.
        val used = (0..44).map { book.receive[it] }.toSet()
        assertEquals(45 to 0, core(used).scanPast(keys, 1, 0))
    }

    @Test
    fun nothingNewLeavesTheCountsAndCostsOneQueryPerChain() = runBlocking {
        val c = core(setOf(book.receive[0]))
        queries.clear()
        assertEquals(4 to 2, c.scanPast(keys, 4, 2))
        assertEquals(listOf("GET" to 20, "GET" to 20), queries)
    }

    @Test
    fun theClassicChainScansTheSameAddressesItDerives() = runBlocking {
        val classic = KeySource("some classic seed text")
        val addrs = SkyFiberWalletCore("http://127.0.0.1:1", OkHttpClient()).deriveAddresses(classic, 40, 0).receive
        assertEquals(30 to 0, core(setOf(addrs[0], addrs[15], addrs[29])).scanPast(classic, 1, 0))
    }

    @Test
    fun manyAddressesGoAsAPostForm() = runBlocking {
        queries.clear()
        val all = book.receive + book.change
        core(emptySet()).balance(AddressBook(all.take(60), emptyList()))
        core(emptySet()).history(AddressBook(all, emptyList()))
        assertEquals("GET" to 60, queries[0])
        assertTrue(queries.contains("POST" to 150), queries.toString())
    }
}

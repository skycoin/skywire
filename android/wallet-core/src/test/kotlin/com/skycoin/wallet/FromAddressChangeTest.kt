package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import com.skycoin.wallet.skycoin.SkycoinCrypto
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import java.nio.ByteBuffer
import java.nio.ByteOrder
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * A send from one chosen address returns its change to that address. It
 * used to go to the wallet's first address, moving coins the user had not
 * chosen to move.
 */
class FromAddressChangeTest {

    private val seed = "abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"

    private fun fakeNode(outputs: String) = OkHttpClient.Builder().addInterceptor { chain ->
        val body = when (val path = chain.request().url.encodedPath) {
            "/api/v1/health" -> """{"user_verify_transaction":{"burn_factor":10,"max_transaction_size":32768,"max_decimals":3}}"""
            "/api/v1/outputs" -> outputs
            else -> error("unexpected request $path")
        }
        Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
            .body(body.toResponseBody("application/json".toMediaType())).build()
    }.build()

    /** (address key hex, coins) of each output in a signed transaction. */
    private fun outputs(rawHex: String): List<Pair<String, ULong>> {
        val b = ByteBuffer.wrap(rawHex.hexToBytes()).order(ByteOrder.LITTLE_ENDIAN)
        b.position(4 + 1 + 32)
        b.position(b.position() + 4 + 65 * b.getInt(b.position()))
        b.position(b.position() + 4 + 32 * b.getInt(b.position()))
        return List(b.int) {
            b.get()
            val key = ByteArray(20).also { b.get(it) }
            val coins = b.long.toULong()
            b.long
            key.toHex() to coins
        }
    }

    @Test
    fun changeReturnsToTheChosenAddress() = runBlocking {
        val (first, second, to) = SkyFiberWalletCore("http://node.test", OkHttpClient())
            .deriveAddresses(seed, 3, 0).receive
        val held = """{"head_outputs":[
            {"hash":"${"11".repeat(32)}","address":"$first","coins":"10.000000","hours":100,"calculated_hours":100},
            {"hash":"${"22".repeat(32)}","address":"$second","coins":"100.000000","hours":1000,"calculated_hours":1000}]}"""
        val core = SkyFiberWalletCore("http://node.test", fakeNode(held))
        val book = AddressBook(receive = listOf(first, second), change = emptyList())

        val plan = core.buildTx(seed, book, to, 10_000_000uL, null, false, second, null)
        val sent = outputs(core.signTx(seed, plan).rawHex)

        val key = { a: String -> SkycoinCrypto.decodeAddress(a)!!.toHex() }
        assertEquals(listOf(key(to) to 10_000_000uL, key(second) to 90_000_000uL), sent)
    }
}

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

/**
 * Each transaction names the wallet's own addresses it used: where a send's
 * coins left from, and where a receive's coins arrived.
 */
class HistoryOwnAddressTest {

    private val mine1 = "2GgFvqoyk9RjwVzj8tqfcXVXB4orBwoc9qv"
    private val mine2 = "R6aHqKWSQfvpdo2fGSrq4F1RYXkBWR9HHJ"
    private val other = "WADSeEwEQVbtUy8CfcVimyxX1KjTRkvfoK"

    private fun fakeNode(transactions: String) = OkHttpClient.Builder().addInterceptor { chain ->
        val body = when (val path = chain.request().url.encodedPath) {
            "/api/v1/transactions" -> transactions
            else -> error("unexpected request $path")
        }
        Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
            .body(body.toResponseBody("application/json".toMediaType())).build()
    }.build()

    @Test
    fun sendAndReceiveNameTheirOwnAddresses() = runBlocking {
        val txs = """[
            {"status":{"confirmed":true,"height":3},"time":100,"txn":{"txid":"send","timestamp":100,
             "inputs":[{"uxid":"a","owner":"$mine2","coins":"5.000000"},{"uxid":"b","owner":"$mine1","coins":"1.000000"},
                       {"uxid":"c","owner":"$mine2","coins":"1.000000"}],
             "outputs":[{"dst":"$other","coins":"6.000000"},{"dst":"$mine2","coins":"1.000000"}]}},
            {"status":{"confirmed":true,"height":2},"time":90,"txn":{"txid":"recv","timestamp":90,
             "inputs":[{"uxid":"d","owner":"$other","coins":"3.000000"}],
             "outputs":[{"dst":"$mine1","coins":"2.000000"},{"dst":"$other","coins":"1.000000"}]}}]"""
        val core = SkyFiberWalletCore("http://node.test", fakeNode(txs))
        val history = core.history(AddressBook(receive = listOf(mine1, mine2), change = emptyList()))
            .associateBy { it.txid }

        assertEquals(listOf(mine2, mine1), history.getValue("send").own)
        assertEquals(listOf(mine1), history.getValue("recv").own)
    }
}

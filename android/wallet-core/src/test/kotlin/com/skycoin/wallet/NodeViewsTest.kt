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

/** The node pages: the chain, the peers and the unconfirmed pool, read as node.skycoin.com answers them. */
class NodeViewsTest {

    private val mine = "2iNNt6fm9LszSWe51693BeyNUKX34pPaLx8"
    private var deleted: String? = null

    private val core = SkyFiberWalletCore(
        "http://node.test",
        OkHttpClient.Builder().addInterceptor { chain ->
            val url = chain.request().url
            val body = when (url.encodedPath) {
                "/api/v1/blockchain/metadata" ->
                    """{"head":{"seq":195720,"block_hash":"6144bdc1","timestamp":1792385083,"fee":2601377}}"""
                "/api/v1/coinSupply" ->
                    """{"current_supply":"24239050.000000","total_supply":"25000000.000000","max_supply":"100000000.000000"}"""
                "/api/v1/network/connections" ->
                    """{"connections":[{"id":339,"address":"106.43.109.53:44870","last_sent":1792666593,"last_received":1792666590,
                    "outgoing":false,"height":195720,"user_agent":"skycoin:0.27.1","is_trusted_peer":true}]}"""
                "/api/v1/pendingTxs" -> if (chain.request().method == "DELETE") {
                    deleted = url.queryParameter("txid")
                    "{}"
                } else {
                    """[{"transaction":{"txid":"aa","inputs":[{"uxid":"u1","owner":"$mine","coins":"5.000000"}],
                    "outputs":[{"dst":"x","coins":"4.000000"},{"dst":"y","coins":"1.000000"}]},"received":"2026-10-22T10:56:06Z"},
                    {"transaction":{"txid":"bb","inputs":[{"uxid":"u2","owner":"z","coins":"2.000000"}],
                    "outputs":[{"dst":"w","coins":"2.000000"}]},"received":"2026-10-22T11:00:00Z"}]"""
                }
                "/api/v1/csrf" -> """{"csrf_token":"t"}"""
                else -> error("unexpected $url")
            }
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body(body.toResponseBody("application/json".toMediaType())).build()
        }.build(),
    )

    @Test
    fun chain() = runBlocking {
        val c = core.chainInfo()
        assertEquals(195720L, c.height)
        assertEquals("6144bdc1", c.blockHash)
        assertEquals("24239050.000000", c.currentSupply)
        assertEquals("25000000.000000", c.totalSupply)
    }

    @Test
    fun peers() = runBlocking {
        val p = core.peers().single()
        assertEquals("106.43.109.53:44870", p.address)
        assertEquals(true, p.trusted)
        assertEquals(1792666590L, p.lastReceived)
    }

    @Test
    fun pendingMarksTheWalletsOwn() = runBlocking {
        val p = core.pending(AddressBook(listOf(mine), emptyList()))
        assertEquals(listOf("aa" to true, "bb" to false), p.map { it.txid to it.mine })
        assertEquals(5_000_000uL, p[0].coins)
        core.deletePending("aa")
        assertEquals("aa", deleted)
    }
}

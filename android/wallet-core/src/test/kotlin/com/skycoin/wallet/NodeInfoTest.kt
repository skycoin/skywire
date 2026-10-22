package com.skycoin.wallet

import com.skycoin.wallet.eth.EthWalletCore
import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import kotlinx.coroutines.runBlocking
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import kotlin.test.Test
import kotlin.test.assertEquals
import kotlin.test.assertFalse
import kotlin.test.assertNull
import kotlin.test.assertTrue

/** A node checked before it is used: what it reports, and when the wallet refuses it. */
class NodeInfoTest {

    private fun answering(body: String) = OkHttpClient.Builder().addInterceptor { chain ->
        Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
            .body(body.toResponseBody("application/json".toMediaType())).build()
    }.build()

    // The fields node.skycoin.com's /api/v1/health answered with on 2026-10-22.
    private fun health(version: String) = """{"blockchain":{"head":{"seq":195720}},"version":{"version":"$version"},
        "coin":"skycoin","csrf_enabled":false,"blockchain_pubkey":"0328c576d3f420e7682058a981173a4b374c7cc5ff55bf394d3cf57059bbe6456a",
        "user_verify_transaction":{"burn_factor":10,"max_transaction_size":32768,"max_decimals":3},
        "fiber":{"name":"skycoin","display_name":"Skycoin","ticker":"SKY","explorer_url":"https://explorer.skycoin.com"}}"""

    @Test
    fun fiberNodeNamesItsCoin() = runBlocking {
        val info = SkyFiberWalletCore("http://node.test", answering(health("0.27.1"))).nodeInfo()
        assertEquals("Skycoin", info.coinName)
        assertEquals("SKY", info.ticker)
        assertEquals("0.27.1", info.version)
        assertEquals(195720L, info.height)
        assertEquals(10, info.burnFactor)
        assertEquals("https://explorer.skycoin.com", info.explorerUrl)
        assertNull(info.problem)
    }

    @Test
    fun oldNodesAreRefused() = runBlocking {
        assertEquals(NodeProblem.TOO_OLD, SkyFiberWalletCore("http://node.test", answering(health("0.23.9"))).nodeInfo().problem)
    }

    @Test
    fun versionFloor() {
        val floor = listOf(0, 24, 0)
        assertTrue(SkyFiberWalletCore.versionBelow("0.23.1", floor))
        assertFalse(SkyFiberWalletCore.versionBelow("0.24.0", floor))
        assertFalse(SkyFiberWalletCore.versionBelow("v0.28.0-rc1", floor))
        assertFalse(SkyFiberWalletCore.versionBelow("", floor), "an unknown version is not held against a node")
    }

    @Test
    fun ethereumOffMainnetIsRefused() = runBlocking {
        val core = EthWalletCore("http://rpc.test", null, answering("""{"jsonrpc":"2.0","id":1,"result":"0x5"}"""))
        assertEquals(NodeProblem.WRONG_CHAIN, core.nodeInfo().problem)
    }
}

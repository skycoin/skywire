package com.skycoin.wallet

import com.skycoin.wallet.eth.EthWalletCore
import kotlinx.coroutines.runBlocking
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Protocol
import okhttp3.Response
import okhttp3.ResponseBody.Companion.toResponseBody
import okio.Buffer
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * The slow, normal and fast fees are skycoin's eth.FeePresets: a 0.1 gwei
 * tip, the node's suggestion with that floor, and twice the suggestion,
 * each capped at twice the base fee plus the tip.
 */
class EthFeeLevelTest {

    private val seed = KeySource("abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about")
    private val to = "0xfb6916095ca1df60bb79ce92ce3ea74c37c5d359"

    private fun core(suggestedTip: String) = EthWalletCore(
        "http://rpc.test",
        null,
        OkHttpClient.Builder().addInterceptor { chain ->
            val body = Buffer().also { chain.request().body!!.writeTo(it) }.readUtf8()
            val result = when (Json.parseToJsonElement(body).jsonObject["method"]!!.jsonPrimitive.content) {
                "eth_getBalance" -> "\"0xde0b6b3a7640000\"" // 1 ETH
                "eth_chainId" -> "\"0x1\""
                "eth_maxPriorityFeePerGas" -> "\"$suggestedTip\""
                "eth_getBlockByNumber" -> """{"baseFeePerGas":"0x2540be400"}""" // 10 gwei
                "eth_getTransactionCount" -> "\"0x0\""
                "eth_estimateGas" -> "\"0x5208\"" // 21000, padded to 25200
                else -> error("unexpected call $body")
            }
            Response.Builder().request(chain.request()).protocol(Protocol.HTTP_1_1).code(200).message("OK")
                .body("""{"jsonrpc":"2.0","id":1,"result":$result}""".toResponseBody("application/json".toMediaType()))
                .build()
        }.build(),
    )

    private fun feeGwei(core: EthWalletCore, level: FeeLevel?): ULong = runBlocking {
        val book = core.deriveAddresses(seed, 1, 0)
        core.buildTx(seed, book, to, 1_000_000uL, null, false, feeLevel = level).fee
    }

    @Test
    fun levelsPickTheTip() {
        val c = core("0x77359400") // 2 gwei suggested
        assertEquals(25_200uL * 201uL / 10uL, feeGwei(c, FeeLevel.SLOW)) // cap 20.1 gwei
        assertEquals(25_200uL * 22uL, feeGwei(c, FeeLevel.NORMAL))
        assertEquals(25_200uL * 22uL, feeGwei(c, null))
        assertEquals(25_200uL * 24uL, feeGwei(c, FeeLevel.FAST))
    }

    @Test
    fun aZeroSuggestionIsRaisedToTheLeastTip() {
        assertEquals(25_200uL * 201uL / 10uL, feeGwei(core("0x0"), FeeLevel.NORMAL))
    }
}

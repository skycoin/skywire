package com.skycoin.wallet.btc

import com.skycoin.wallet.WalletException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.doubleOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException

/**
 * Esplora-compatible Bitcoin API (mempool.space, blockstream.info, or a
 * self-hosted electrs). Balance, history and broadcast only — keys never
 * appear on this wire.
 */
class BtcEsploraClient(baseUrl: String, private val client: OkHttpClient) {

    private val base: HttpUrl = baseUrl.trim().trimEnd('/').toHttpUrlOrNull()
        ?: throw IllegalArgumentException("invalid esplora URL: $baseUrl")

    @Serializable
    class TxoStats(
        @SerialName("funded_txo_sum") val fundedSum: ULong = 0u,
        @SerialName("spent_txo_sum") val spentSum: ULong = 0u,
        @SerialName("tx_count") val txCount: Long = 0,
    )

    @Serializable
    class AddressInfo(
        @SerialName("chain_stats") val chain: TxoStats = TxoStats(),
        @SerialName("mempool_stats") val mempool: TxoStats = TxoStats(),
    )

    @Serializable
    class UtxoStatus(
        val confirmed: Boolean = false,
        @SerialName("block_height") val blockHeight: Long = 0,
        @SerialName("block_time") val blockTime: Long = 0,
    )

    @Serializable
    class Utxo(
        val txid: String,
        val vout: Int,
        val value: ULong,
        val status: UtxoStatus = UtxoStatus(),
    )

    @Serializable
    class Prevout(
        @SerialName("scriptpubkey_address") val address: String? = null,
        val value: ULong = 0u,
    )

    @Serializable
    class Vin(val prevout: Prevout? = null)

    @Serializable
    class Vout(
        @SerialName("scriptpubkey_address") val address: String? = null,
        val value: ULong = 0u,
    )

    @Serializable
    class Tx(
        val txid: String,
        val fee: ULong = 0u,
        val status: UtxoStatus = UtxoStatus(),
        val vin: List<Vin> = emptyList(),
        val vout: List<Vout> = emptyList(),
    )

    suspend fun addressInfo(address: String): AddressInfo =
        get("api/address/$address") { json.decodeFromString(AddressInfo.serializer(), it) }

    suspend fun utxos(address: String): List<Utxo> =
        get("api/address/$address/utxo") { json.decodeFromString(ListSerializer(Utxo.serializer()), it) }

    suspend fun transactions(address: String): List<Tx> =
        get("api/address/$address/txs") { json.decodeFromString(ListSerializer(Tx.serializer()), it) }

    suspend fun tipHeight(): Long =
        get("api/blocks/tip/height") { it.trim().toLong() }

    /**
     * sat/vB presets. mempool.space serves /api/v1/fees/recommended; plain
     * esplora serves /api/fee-estimates keyed by confirmation target.
     */
    suspend fun feeRates(): Triple<Int, Int, Int> {
        runCatching {
            return get("api/v1/fees/recommended") { body ->
                val o = json.parseToJsonElement(body).jsonObject
                fun f(k: String) = o[k]?.jsonPrimitive?.doubleOrNull?.toInt() ?: 1
                Triple(maxOf(f("economyFee"), 1), maxOf(f("halfHourFee"), 1), maxOf(f("fastestFee"), 1))
            }
        }
        return get("api/fee-estimates") { body ->
            val o = json.parseToJsonElement(body).jsonObject
            fun target(k: String) = o[k]?.jsonPrimitive?.doubleOrNull
            val economy = target("144") ?: target("25") ?: 1.0
            val normal = target("6") ?: target("3") ?: economy
            val priority = target("1") ?: target("2") ?: normal
            Triple(
                maxOf(economy.toInt(), 1),
                maxOf(normal.toInt(), 1),
                maxOf(priority.toInt(), 1),
            )
        }
    }

    /** Broadcast raw hex; the server's rejection text is the error message. */
    suspend fun broadcast(rawHex: String): String = withContext(Dispatchers.IO) {
        val url = base.newBuilder().addPathSegments("api/tx").build()
        val req = Request.Builder()
            .url(url)
            .post(rawHex.toRequestBody("text/plain".toMediaType()))
            .build()
        client.newCall(req).execute().use { resp ->
            val body = resp.body.string()
            if (!resp.isSuccessful) {
                throw WalletException.NodeRejected(body.trim().take(300).ifEmpty { "server answered HTTP ${resp.code}" })
            }
            body.trim()
        }
    }

    private suspend fun <T> get(path: String, parse: (String) -> T): T = withContext(Dispatchers.IO) {
        val url = base.newBuilder().addPathSegments(path).build()
        client.newCall(Request.Builder().url(url).build()).execute().use { resp ->
            val body = resp.body.string()
            if (!resp.isSuccessful) throw IOException(body.trim().take(300).ifEmpty { "server answered HTTP ${resp.code}" })
            parse(body)
        }
    }

    companion object {
        private val json = Json { ignoreUnknownKeys = true; isLenient = true }
    }
}

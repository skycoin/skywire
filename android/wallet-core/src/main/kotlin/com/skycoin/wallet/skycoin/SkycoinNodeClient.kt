package com.skycoin.wallet.skycoin

import com.skycoin.wallet.WalletException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
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
 * REST client for a Skycoin-family node. Every fiber chain runs the same
 * daemon, so one client serves Skycoin and any user-added fiber coin — only
 * the base URL differs.
 */
class SkycoinNodeClient(baseUrl: String, private val client: OkHttpClient) {

    private val base: HttpUrl = baseUrl.trim().trimEnd('/').toHttpUrlOrNull()
        ?: throw IllegalArgumentException("invalid node URL: $baseUrl")

    @Serializable
    class VerifyTxnParams(
        @SerialName("burn_factor") val burnFactor: UInt = 10u,
        @SerialName("max_transaction_size") val maxTransactionSize: UInt = 32768u,
        @SerialName("max_decimals") val maxDecimals: Int = 3,
    )

    @Serializable
    class BlockHeader(val seq: ULong = 0u, val timestamp: ULong = 0u)

    @Serializable
    class BlockchainInfo(val head: BlockHeader = BlockHeader())

    @Serializable
    class Health(
        val blockchain: BlockchainInfo = BlockchainInfo(),
        @SerialName("user_verify_transaction") val userVerifyTxn: VerifyTxnParams = VerifyTxnParams(),
        val coin: String = "",
    )

    @Serializable
    class NodeOutput(
        val hash: String,
        val time: ULong = 0u,
        @SerialName("block_seq") val blockSeq: ULong = 0u,
        @SerialName("src_tx") val srcTx: String = "",
        val address: String,
        val coins: String,
        val hours: ULong = 0u,
        @SerialName("calculated_hours") val calculatedHours: ULong = 0u,
    )

    @Serializable
    class Outputs(
        @SerialName("head_outputs") val headOutputs: List<NodeOutput> = emptyList(),
        @SerialName("outgoing_outputs") val outgoingOutputs: List<NodeOutput> = emptyList(),
        @SerialName("incoming_outputs") val incomingOutputs: List<NodeOutput> = emptyList(),
    )

    @Serializable
    class BalancePair(val coins: ULong = 0u, val hours: ULong = 0u)

    @Serializable
    class Balance(
        val confirmed: BalancePair = BalancePair(),
        val predicted: BalancePair = BalancePair(),
    )

    @Serializable
    class TxnStatus(
        val confirmed: Boolean = false,
        val unconfirmed: Boolean = false,
        /** When confirmed: how many blocks deep (1 = in the head block). */
        val height: ULong = 0u,
        @SerialName("block_seq") val blockSeq: ULong = 0u,
    )

    @Serializable
    class VerboseInput(
        val uxid: String,
        val owner: String,
        val coins: String = "0",
        val hours: ULong = 0u,
        @SerialName("calculated_hours") val calculatedHours: ULong = 0u,
    )

    @Serializable
    class VerboseOutput(
        val uxid: String = "",
        val dst: String,
        val coins: String = "0",
        val hours: ULong = 0u,
    )

    @Serializable
    class VerboseTxn(
        val txid: String,
        val timestamp: ULong = 0u,
        @SerialName("inner_hash") val innerHash: String = "",
        val fee: ULong = 0u,
        val inputs: List<VerboseInput> = emptyList(),
        val outputs: List<VerboseOutput> = emptyList(),
    )

    @Serializable
    class TxnEntry(
        val status: TxnStatus = TxnStatus(),
        val time: ULong = 0u,
        val txn: VerboseTxn,
    )

    suspend fun health(): Health = get("api/v1/health") { body ->
        json.decodeFromString(Health.serializer(), body)
    }

    suspend fun outputs(addresses: List<String>): Outputs =
        get("api/v1/outputs", "addrs" to addresses.joinToString(",")) { body ->
            json.decodeFromString(Outputs.serializer(), body)
        }

    suspend fun balance(addresses: List<String>): Balance =
        get("api/v1/balance", "addrs" to addresses.joinToString(",")) { body ->
            json.decodeFromString(Balance.serializer(), body)
        }

    suspend fun transactions(addresses: List<String>): List<TxnEntry> =
        get("api/v1/transactions", "addrs" to addresses.joinToString(","), "verbose" to "1") { body ->
            json.decodeFromString(ListSerializer(TxnEntry.serializer()), body)
        }

    /** Broadcast; returns the txid the node reports. Node-side rejections carry the node's own words. */
    suspend fun inject(rawTxHex: String): String = withContext(Dispatchers.IO) {
        val csrf = fetchCsrf()
        val url = base.newBuilder().addPathSegments("api/v1/injectTransaction").build()
        val payload = "{\"rawtx\":\"$rawTxHex\"}"
        val req = Request.Builder()
            .url(url)
            .post(payload.toRequestBody("application/json".toMediaType()))
            .apply { if (csrf != null) header("X-CSRF-Token", csrf) }
            .build()
        client.newCall(req).execute().use { resp ->
            val body = resp.body.string()
            if (!resp.isSuccessful) {
                throw WalletException.NodeRejected(errorMessage(body, resp.code))
            }
            json.decodeFromString(String.serializer(), body.trim())
        }
    }

    /** CSRF tokens are optional server-side; absence is not an error. */
    private fun fetchCsrf(): String? = runCatching {
        val url = base.newBuilder().addPathSegments("api/v1/csrf").build()
        client.newCall(Request.Builder().url(url).build()).execute().use { resp ->
            if (!resp.isSuccessful) return@use null
            val obj = json.parseToJsonElement(resp.body.string()).jsonObject
            obj["csrf_token"]?.jsonPrimitive?.content
        }
    }.getOrNull()

    private suspend fun <T> get(
        path: String,
        vararg params: Pair<String, String>,
        parse: (String) -> T,
    ): T = withContext(Dispatchers.IO) {
        val url = base.newBuilder().addPathSegments(path).apply {
            params.forEach { (k, v) -> addQueryParameter(k, v) }
        }.build()
        client.newCall(Request.Builder().url(url).build()).execute().use { resp ->
            val body = resp.body.string()
            if (!resp.isSuccessful) throw IOException(errorMessage(body, resp.code))
            parse(body)
        }
    }

    companion object {
        private val json = Json { ignoreUnknownKeys = true; isLenient = true }

        /** Prefer the node's own message: v2 wraps it in error.message, v1 is plain text. */
        fun errorMessage(body: String, code: Int): String {
            val fromJson = runCatching {
                val el = json.parseToJsonElement(body)
                (el.jsonObject["error"] as? JsonObject)?.get("message")?.jsonPrimitive?.content
            }.getOrNull()
            val text = fromJson ?: body.trim().ifEmpty { null }
            return text?.take(300) ?: "node answered HTTP $code"
        }
    }
}

package com.skycoin.wallet.eth

import com.skycoin.wallet.WalletException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonNull
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.HttpUrl
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.math.BigInteger

/**
 * Ethereum chain access, split the same way Bitcoin's is: a node for state
 * and broadcast (JSON-RPC), an indexer for history — plain JSON-RPC cannot
 * list an address's past transactions. The indexer speaks the
 * etherscan-style `?module=account` API, which Blockscout instances serve
 * without a key. Keys never appear on either wire.
 */
class EthRpcClient(
    rpcUrl: String,
    indexerUrl: String?,
    private val client: OkHttpClient,
) {

    private val rpc: HttpUrl = rpcUrl.trim().trimEnd('/').toHttpUrlOrNull()
        ?: throw IllegalArgumentException("invalid RPC URL: $rpcUrl")
    private val indexer: HttpUrl? = indexerUrl?.trim()?.trimEnd('/')?.toHttpUrlOrNull()

    private var requestId = 0

    // --- JSON-RPC ---

    suspend fun balanceWei(address: String): BigInteger =
        quantity(call("eth_getBalance", JsonPrimitive(address), JsonPrimitive("latest")))

    /** Pending-tag nonce, so back-to-back sends chain instead of colliding. */
    suspend fun nonce(address: String): BigInteger =
        quantity(call("eth_getTransactionCount", JsonPrimitive(address), JsonPrimitive("pending")))

    suspend fun chainId(): BigInteger = quantity(call("eth_chainId"))

    /** The node's tip suggestion, or 1 gwei where the method is not served. */
    suspend fun maxPriorityFeePerGas(): BigInteger =
        runCatching { quantity(call("eth_maxPriorityFeePerGas")) }
            .getOrDefault(BigInteger.valueOf(1_000_000_000))

    suspend fun baseFeePerGas(): BigInteger {
        val block = call("eth_getBlockByNumber", JsonPrimitive("latest"), JsonPrimitive(false))
        val fee = block.jsonObject["baseFeePerGas"] ?: return BigInteger.ZERO
        return quantity(fee)
    }

    /** Node's gas estimate, or null when it refuses (the caller falls back). */
    suspend fun estimateGas(
        from: String,
        to: String,
        valueWei: BigInteger,
        data: ByteArray?,
    ): BigInteger? {
        val tx = buildMap<String, JsonElement> {
            put("from", JsonPrimitive(from))
            put("to", JsonPrimitive(to))
            if (valueWei.signum() > 0) put("value", JsonPrimitive(hex(valueWei)))
            if (data != null && data.isNotEmpty()) put("data", JsonPrimitive(hex(data)))
        }
        return runCatching { quantity(call("eth_estimateGas", JsonObject(tx))) }.getOrNull()
    }

    /** A read-only contract call; returns the raw return bytes. */
    suspend fun ethCall(to: String, data: ByteArray): ByteArray {
        val tx = JsonObject(
            mapOf("to" to JsonPrimitive(to), "data" to JsonPrimitive(hex(data))),
        )
        val result = call("eth_call", tx, JsonPrimitive("latest"))
        return hexBytes(result.jsonPrimitive.content)
    }

    /** Broadcast; returns the tx hash the node reports. */
    suspend fun sendRaw(raw: ByteArray): String {
        val result = call("eth_sendRawTransaction", JsonPrimitive(hex(raw)))
        return result.jsonPrimitive.content
    }

    private suspend fun call(method: String, vararg params: JsonElement): JsonElement =
        withContext(Dispatchers.IO) {
            val body = JsonObject(
                mapOf(
                    "jsonrpc" to JsonPrimitive("2.0"),
                    "id" to JsonPrimitive(++requestId),
                    "method" to JsonPrimitive(method),
                    "params" to JsonArray(params.toList()),
                ),
            ).toString()
            val request = Request.Builder()
                .url(rpc)
                .post(body.toRequestBody("application/json".toMediaType()))
                .build()
            client.newCall(request).execute().use { resp ->
                if (!resp.isSuccessful) throw IOException("node answered HTTP ${resp.code}")
                val envelope = json.parseToJsonElement(resp.body.string()).jsonObject
                envelope["error"]?.let { err ->
                    // The node's own words: an estimateGas revert reason or a
                    // rejected broadcast is a message the user can act on.
                    val message = err.jsonObject["message"]?.jsonPrimitive?.contentOrNull
                        ?: err.toString()
                    throw WalletException.NodeRejected(message)
                }
                envelope["result"]?.takeIf { it !is JsonNull }
                    ?: throw IOException("node answered without a result")
            }
        }

    private fun quantity(element: JsonElement): BigInteger {
        val text = element.jsonPrimitive.content.removePrefix("0x")
        if (text.isEmpty()) return BigInteger.ZERO
        return BigInteger(text, 16)
    }

    private fun hex(value: BigInteger): String = "0x" + value.toString(16)

    private fun hex(bytes: ByteArray): String =
        "0x" + bytes.joinToString("") { "%02x".format(it) }

    private fun hexBytes(text: String): ByteArray {
        val body = text.removePrefix("0x").let { if (it.length % 2 == 1) "0$it" else it }
        return ByteArray(body.length / 2) { i ->
            ((Character.digit(body[i * 2], 16) shl 4) or Character.digit(body[i * 2 + 1], 16)).toByte()
        }
    }

    // --- indexer (history) ---

    /**
     * One row of `action=txlist` / `action=tokentx`. Every number arrives as
     * a decimal string; both actions share the fields this wallet reads.
     */
    @Serializable
    class IndexedTx(
        val hash: String = "",
        val from: String = "",
        val to: String = "",
        val value: String = "0",
        @SerialName("timeStamp") val timestamp: String = "0",
        val confirmations: String = "0",
        @SerialName("isError") val isError: String = "0",
        @SerialName("gasUsed") val gasUsed: String = "0",
        @SerialName("gasPrice") val gasPrice: String = "0",
    )

    @Serializable
    private class IndexerEnvelope(
        val status: String = "0",
        val message: String = "",
        val result: JsonElement = JsonNull,
    )

    suspend fun transactions(address: String): List<IndexedTx> =
        indexed("txlist", address, contract = null)

    suspend fun tokenTransfers(address: String, contract: String): List<IndexedTx> =
        indexed("tokentx", address, contract)

    private suspend fun indexed(
        action: String,
        address: String,
        contract: String?,
    ): List<IndexedTx> = withContext(Dispatchers.IO) {
        val base = indexer ?: return@withContext emptyList()
        val url = base.newBuilder()
            .addPathSegment("api")
            .addQueryParameter("module", "account")
            .addQueryParameter("action", action)
            .addQueryParameter("address", address)
            .addQueryParameter("sort", "desc")
            .addQueryParameter("page", "1")
            .addQueryParameter("offset", HISTORY_PAGE)
            .apply { if (contract != null) addQueryParameter("contractaddress", contract) }
            .build()
        client.newCall(Request.Builder().url(url).build()).execute().use { resp ->
            if (!resp.isSuccessful) throw IOException("indexer answered HTTP ${resp.code}")
            val envelope = json.decodeFromString(IndexerEnvelope.serializer(), resp.body.string())
            // status "0" covers both "No transactions found" (an empty wallet,
            // not an error) and real failures, which come with a non-list result.
            val rows = envelope.result as? JsonArray ?: return@use emptyList()
            rows.map { json.decodeFromJsonElement(IndexedTx.serializer(), it) }
        }
    }

    private companion object {
        val json = Json { ignoreUnknownKeys = true; isLenient = true }

        /** Most recent rows per address — a phone screen, not an archive. */
        const val HISTORY_PAGE = "50"
    }
}

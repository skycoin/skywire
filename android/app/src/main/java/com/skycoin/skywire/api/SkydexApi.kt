package com.skycoin.skywire.api

import android.content.Context
import com.skycoin.skywire.R
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkydexProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.DeserializationStrategy
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.util.concurrent.TimeUnit

/** skydex-client's answer about its one market connection. */
@Serializable
data class MarketStatus(
    @SerialName("connected") val connected: Boolean = false,
    @SerialName("market_pk") val marketPk: String = "",
    /** Operator-set display name; often empty. */
    @SerialName("market_name") val marketName: String = "",
)

/**
 * Client for skydex-client's *own* control API (`127.0.0.1:8051` by default) —
 * separate from [VisorApi], which talks to the visor on `:8000`.
 *
 * Everything the SkyDEX screens do goes through here: the connection itself,
 * and every trading call, which skydex-client passes to the market over dmsg.
 * The engine never dials a market on its own: `--market-pk` only pre-fills a
 * connect form, and something has to POST `/api/connect` for a connection to
 * exist. [status] is the truth the screen mirrors rather than whatever it last
 * did.
 *
 * Every request carries the basic-auth credential of the phone's gate (see
 * [SkydexProfile]) — on this device that surface is not open, including to us.
 */
class SkydexApi private constructor(context: Context) {

    private val app = context.applicationContext

    private val secrets = SecretStore(app)

    // Loopback, and a server that either answers at once or isn't up yet.
    private val client = OkHttpClient.Builder()
        .connectTimeout(2, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .callTimeout(6, TimeUnit.SECONDS)
        .build()

    /**
     * For `/api/connect` alone, which does not return until a dmsg route to
     * the market is built *and* a `get_currencies` handshake has come back.
     * That is the same route setup the SkySOCKS screen measured at ~10 s on a
     * good network and minutes on a bad cell — the client above would abort it
     * long before the visor gave up.
     */
    private val dialClient = client.newBuilder()
        .readTimeout(90, TimeUnit.SECONDS)
        .callTimeout(95, TimeUnit.SECONDS)
        .build()

    /**
     * For the calls skydex-client passes through to the market: each one is a
     * round trip over dmsg, which takes seconds on a relayed route.
     */
    private val marketClient = client.newBuilder()
        .readTimeout(45, TimeUnit.SECONDS)
        .callTimeout(50, TimeUnit.SECONDS)
        .build()

    /** The device-local password the gate is configured with. */
    suspend fun password(): String = secrets.skydexPassword()

    /** `Basic …` header value the gate accepts. */
    suspend fun authorization(): String =
        okhttp3.Credentials.basic(SkydexProfile.USER, password())

    /** True once skydex-client answers on its control port. */
    suspend fun probe(baseUrl: String): Boolean = withContext(Dispatchers.IO) {
        runCatching {
            client.newCall(request(baseUrl)).execute().use { it.isSuccessful }
        }.getOrDefault(false)
    }

    /**
     * The current market connection, or null when the app isn't answering —
     * the ordinary case while it is still coming up, and not worth an error
     * on a poll that runs every couple of seconds.
     */
    suspend fun status(baseUrl: String): MarketStatus? = withContext(Dispatchers.IO) {
        runCatching {
            client.newCall(request(baseUrl + "api/status")).execute().use { resp ->
                if (resp.isSuccessful) decode(resp.body.string()) else null
            }
        }.getOrNull()
    }

    /**
     * Dial [pk] and handshake with it. The failure here is the one the user
     * most needs to read — an unreachable market, a rejected key — so it is
     * raised with the market's own wording rather than swallowed.
     */
    suspend fun connect(baseUrl: String, pk: String): MarketStatus =
        withContext(Dispatchers.IO) {
            val body = json.encodeToString(
                ConnectRequest.serializer(),
                ConnectRequest(pk),
            ).toRequestBody(JSON_MEDIA)
            dialClient.newCall(request(baseUrl + "api/connect", body))
                .execute().use { resp ->
                    val text = resp.body.string()
                    if (!resp.isSuccessful) throw IOException(errorMessage(text, resp.code))
                    decode(text)
                }
        }

    /** Drop the market connection. Best-effort: there is nothing to retry. */
    suspend fun disconnect(baseUrl: String): Unit = withContext(Dispatchers.IO) {
        runCatching {
            val body = EMPTY_BODY.toRequestBody(JSON_MEDIA)
            client.newCall(request(baseUrl + "api/disconnect", body)).execute().close()
        }
    }

    // --- the market, through skydex-client ---

    suspend fun currencies(baseUrl: String): DexCurrencies =
        market(baseUrl + "api/currencies", null, DexCurrencies.serializer())

    suspend fun products(baseUrl: String): List<DexProduct> =
        market(baseUrl + "api/products", null, DexProducts.serializer()).products.orEmpty()

    suspend fun orders(baseUrl: String): List<DexOrder> =
        market(baseUrl + "api/orders", null, DexOrders.serializer()).orders.orEmpty()

    suspend fun listings(baseUrl: String): List<DexListing> =
        market(baseUrl + "api/listings/mine", null, DexListings.serializer()).listings.orEmpty()

    suspend fun orderStatus(baseUrl: String, orderId: String): DexOrderStatus =
        market(baseUrl + "api/order-status", idBody("order_id", orderId), DexOrderStatus.serializer())

    suspend fun register(baseUrl: String, walletSky: String, wallets: Map<String, String>) {
        val body = json.encodeToString(DexRegisterRequest.serializer(), DexRegisterRequest(walletSky, wallets))
        market(baseUrl + "api/register", body, DexMessage.serializer())
    }

    suspend fun createListing(baseUrl: String, sellCoin: String, amount: Double, price: Double, paymentCurrency: String): DexCreatedListing {
        val body = json.encodeToString(DexListingRequest.serializer(), DexListingRequest(sellCoin, amount, price, paymentCurrency))
        return market(baseUrl + "api/listings", body, DexCreatedListing.serializer())
    }

    suspend fun cancelListing(baseUrl: String, listingId: String): DexMessage =
        market(baseUrl + "api/listings/cancel", idBody("listing_id", listingId), DexMessage.serializer())

    suspend fun cancelOrder(baseUrl: String, orderId: String): DexMessage =
        market(baseUrl + "api/orders/cancel", idBody("order_id", orderId), DexMessage.serializer())

    suspend fun buy(baseUrl: String, productId: String): DexPurchase =
        market(baseUrl + "api/buy", idBody("product_id", productId), DexPurchase.serializer())

    /** A GET, or a POST with [body]; a refusal comes back as [SkydexException] in the market's words. */
    private suspend fun <T> market(url: String, body: String?, deserializer: DeserializationStrategy<T>): T =
        withContext(Dispatchers.IO) {
            marketClient.newCall(request(url, body?.toRequestBody(JSON_MEDIA))).execute().use { resp ->
                val text = resp.body.string()
                if (!resp.isSuccessful) {
                    val refusal = runCatching { json.decodeFromString(DexRefusal.serializer(), text) }.getOrNull()
                    throw SkydexException(
                        refusal?.error?.takeIf { it.isNotEmpty() } ?: app.getString(R.string.dex_error_connect, resp.code),
                        refusal?.code.orEmpty(),
                        resp.code,
                    )
                }
                json.decodeFromString(deserializer, text.ifBlank { EMPTY_BODY })
            }
        }

    private fun idBody(key: String, id: String): String =
        json.encodeToString(JsonObject.serializer(), JsonObject(mapOf(key to JsonPrimitive(id))))

    @Serializable
    private data class DexRefusal(val error: String = "", val code: String = "")

    // --- plumbing ---

    /** A GET, or a POST when [body] is given, carrying the gate credential. */
    private suspend fun request(url: String, body: RequestBody? = null): Request =
        Request.Builder()
            .url(url)
            .header("Authorization", authorization())
            .apply { if (body != null) post(body) }
            .build()

    private fun decode(body: String): MarketStatus =
        json.decodeFromString(MarketStatus.serializer(), body)

    /** The engine answers `{"error": "…"}`; fall back to the bare status. */
    private fun errorMessage(body: String, code: Int): String =
        runCatching { json.decodeFromString(ApiError.serializer(), body).error }
            .getOrNull()
            ?.takeIf { it.isNotEmpty() }
            ?: app.getString(R.string.dex_error_connect, code)

    @Serializable
    private data class ConnectRequest(@SerialName("market_pk") val marketPk: String)

    companion object {
        private val json = Json { ignoreUnknownKeys = true; isLenient = true }
        private val JSON_MEDIA = "application/json".toMediaType()
        private const val EMPTY_BODY = "{}"

        @Volatile private var instance: SkydexApi? = null

        fun get(context: Context): SkydexApi =
            instance ?: synchronized(this) {
                instance ?: SkydexApi(context.applicationContext).also { instance = it }
            }
    }
}

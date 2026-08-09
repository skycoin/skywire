package com.skycoin.skywire.api

import android.content.Context
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkydexProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
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
 * This is the seam that lets the market public key be entered natively while
 * the trading UI stays the page the desktop already serves. The engine never
 * dials a market on its own: `--market-pk` only pre-fills the page's connect
 * form, and something has to POST `/api/connect` for a connection to exist.
 * Doing that here is what makes "type a key, get the market" one step on the
 * phone instead of two — and the page, which polls `/api/status` on load, comes
 * up already on the market rather than on a second connect form.
 *
 * The reverse direction matters just as much: [status] is the truth the screen
 * mirrors rather than whatever the native header last did.
 *
 * Every request carries the basic-auth credential of the phone's gate (see
 * [SkydexProfile]) — on this device that surface is not open, including to us.
 */
class SkydexApi private constructor(context: Context) {

    private val secrets = SecretStore(context.applicationContext)

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

    /** The device-local password the gate is configured with. */
    suspend fun password(): String = secrets.skydexPassword()

    /** `Basic …` header value the gate accepts. */
    suspend fun authorization(): String =
        okhttp3.Credentials.basic(SkydexProfile.USER, password())

    /** True once the trading UI answers — the gate for showing the WebView. */
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
            ?: "market connect failed ($code)"

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

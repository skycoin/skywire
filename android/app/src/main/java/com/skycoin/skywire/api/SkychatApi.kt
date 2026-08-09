package com.skycoin.skywire.api

import android.content.Context
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkychatProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.serialization.builtins.MapSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

/**
 * Client for skychat's *own* HTTP surface (`127.0.0.1:8001` by default) —
 * separate from [VisorApi], which talks to the visor's API on `:8000`.
 *
 * The app needs three things from it: the basic-auth credential the WebView
 * answers the password gate with, a readiness probe so the screen can wait for
 * the listener instead of showing the WebView's error page, and the address
 * book — the one piece of the embedded UI's state that native screens need
 * too, because a call screen showing 66 hex characters for someone the user
 * has named is not a call screen. Everything else is the UI's business.
 */
class SkychatApi private constructor(context: Context) {

    private val secrets = SecretStore(context.applicationContext)

    // Loopback and a server that either answers at once or isn't up yet —
    // short timeouts keep the readiness poll on its own schedule.
    private val client = OkHttpClient.Builder()
        .connectTimeout(2, TimeUnit.SECONDS)
        .readTimeout(5, TimeUnit.SECONDS)
        .callTimeout(6, TimeUnit.SECONDS)
        .build()

    /** The device-local password the gate is configured with. */
    suspend fun password(): String = secrets.skychatPassword()

    /** `Basic …` header value for the WebView and for downloads. */
    suspend fun authorization(): String =
        okhttp3.Credentials.basic(SkychatProfile.USER, password())

    /**
     * Status of a `GET` on [baseUrl], or null when nothing answered — the
     * ordinary case while the app is still coming up. 401 is reported rather
     * than swallowed: it means the running skychat loaded an older password
     * file, which the caller fixes by restarting the app.
     */
    suspend fun probe(baseUrl: String): Int? = withContext(Dispatchers.IO) {
        runCatching {
            client.newCall(
                Request.Builder()
                    .url(baseUrl)
                    .header("Authorization", authorization())
                    .build(),
            ).execute().use { it.code }
        }.getOrNull()
    }

    /**
     * The operator's names for public keys, from skychat's address book.
     *
     * Empty on any failure rather than throwing: a missing name costs a
     * shortened key on screen, which is exactly what the caller falls back to
     * anyway, and a call must never fail because a nickname could not be read.
     */
    suspend fun contacts(baseUrl: String): Map<String, String> = withContext(Dispatchers.IO) {
        runCatching {
            client.newCall(
                Request.Builder()
                    .url(baseUrl.trimEnd('/') + "/contacts")
                    .header("Authorization", authorization())
                    .build(),
            ).execute().use { resp ->
                if (!resp.isSuccessful) return@use emptyMap()
                json.decodeFromString(
                    MapSerializer(String.serializer(), String.serializer()),
                    resp.body.string(),
                )
            }
        }.getOrDefault(emptyMap())
    }

    /**
     * The unread estimate skychat keeps for surfaces that are not the page —
     * the hub card's badge. Null on any failure: no badge beats a wrong one.
     */
    suspend fun unread(baseUrl: String): Int? = withContext(Dispatchers.IO) {
        runCatching {
            client.newCall(
                Request.Builder()
                    .url(baseUrl.trimEnd('/') + "/unread")
                    .header("Authorization", authorization())
                    .build(),
            ).execute().use { resp ->
                if (!resp.isSuccessful) return@use null
                json.decodeFromString(UnreadCount.serializer(), resp.body.string()).unread
            }
        }.getOrNull()
    }

    @kotlinx.serialization.Serializable
    private data class UnreadCount(val unread: Int = 0)

    companion object {
        private val json = Json { ignoreUnknownKeys = true; isLenient = true }

        @Volatile private var instance: SkychatApi? = null

        fun get(context: Context): SkychatApi =
            instance ?: synchronized(this) {
                instance ?: SkychatApi(context.applicationContext).also { instance = it }
            }
    }
}

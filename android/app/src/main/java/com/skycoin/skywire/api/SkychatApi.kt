package com.skycoin.skywire.api

import android.content.Context
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkychatProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.OkHttpClient
import okhttp3.Request
import java.util.concurrent.TimeUnit

/**
 * Client for skychat's *own* HTTP surface (`127.0.0.1:8001` by default) —
 * separate from [VisorApi], which talks to the visor's API on `:8000`.
 *
 * The app only needs two things from it: the basic-auth credential the
 * WebView answers the password gate with, and a readiness probe so the
 * screen can wait for the listener instead of showing the WebView's error
 * page. Everything else is the embedded UI's business.
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

    companion object {
        @Volatile private var instance: SkychatApi? = null

        fun get(context: Context): SkychatApi =
            instance ?: synchronized(this) {
                instance ?: SkychatApi(context.applicationContext).also { instance = it }
            }
    }
}

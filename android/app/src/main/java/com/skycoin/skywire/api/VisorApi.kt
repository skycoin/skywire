package com.skycoin.skywire.api

import android.content.Context
import com.skycoin.skywire.core.SecretStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import okhttp3.Cookie
import okhttp3.CookieJar
import okhttp3.HttpUrl
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import java.io.IOException
import java.net.URLEncoder
import java.util.concurrent.TimeUnit

/** Login/create-account cannot succeed with the stored password. */
class AuthFailedException(message: String) : IOException(message)

/**
 * Client for the visor's local API on 127.0.0.1:8000.
 *
 * Auth model (all verified against the server):
 *  - single account, username fixed to "admin"; password is the app's
 *    device-local random secret ([SecretStore]);
 *  - `swm-session` cookie, in-memory server side — every visor restart
 *    invalidates it, so any 401 triggers one transparent re-login;
 *  - CSRF (`X-CSRF-Token`, 30 s TTL) is required only for mutating
 *    `/api/visors/{pk}/…` calls — fetch a fresh token per mutation.
 */
class VisorApi(context: Context) {

    private val secrets = SecretStore(context.applicationContext)
    private val json = Json {
        ignoreUnknownKeys = true
        isLenient = true
        coerceInputValues = true
    }

    private val cookies = object : CookieJar {
        private val store = mutableMapOf<String, List<Cookie>>()

        @Synchronized
        override fun saveFromResponse(url: HttpUrl, cookies: List<Cookie>) {
            store[url.host] = cookies
        }

        @Synchronized
        override fun loadForRequest(url: HttpUrl): List<Cookie> =
            store[url.host].orEmpty().filter { it.expiresAt > System.currentTimeMillis() }
    }

    private val client = OkHttpClient.Builder()
        .cookieJar(cookies)
        .connectTimeout(2, TimeUnit.SECONDS)
        .readTimeout(10, TimeUnit.SECONDS)
        .callTimeout(15, TimeUnit.SECONDS)
        .build()

    /**
     * For the routes the visor answers by going out over the network
     * itself. `/api/svc-fetch` dials a deployment service over DMSG with
     * its own 15-second budget per hop, so the loopback client has to
     * outlast it — the default above would abort the call at the very
     * moment the visor is still waiting on the first hop.
     */
    private val relayClient = client.newBuilder()
        .readTimeout(45, TimeUnit.SECONDS)
        .callTimeout(50, TimeUnit.SECONDS)
        .build()

    /**
     * For the two voice-audio streams, which last as long as the call does.
     * Every timeout is off: a call is minutes, and a quiet moment is not a
     * dead connection. Liveness is the server's business — each stream
     * re-arms its own deadline per frame — and on this side the loops stop
     * when the call does.
     */
    private val streamClient = client.newBuilder()
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .writeTimeout(0, TimeUnit.MILLISECONDS)
        .callTimeout(0, TimeUnit.MILLISECONDS)
        .build()

    private val sessionMutex = Mutex()

    @Volatile private var cachedPk: String? = null

    // --- liveness ---

    /** True once the local API answers at all (no session needed). */
    suspend fun ping(): Boolean = withContext(Dispatchers.IO) {
        runCatching {
            client.newCall(Request.Builder().url("$BASE/api/ping").build())
                .execute().use { it.isSuccessful }
        }.getOrDefault(false)
    }

    // --- session bootstrap ---

    /**
     * Make sure a valid session exists: first run creates the single
     * `admin` account with the device-local password, later runs just log
     * in. Throws [AuthFailedException] when the stored password is rejected
     * (keystore rotated under an existing users.db) — callers recover by
     * resetting the account DB with the visor stopped.
     */
    suspend fun ensureSession(): Unit = withContext(Dispatchers.IO) {
        sessionMutex.withLock {
            if (get("/api/user").use { it.isSuccessful }) return@withLock
            val password = secrets.apiPassword()
            val exists = get("/api/user-exists").use { resp ->
                resp.isSuccessful && decode<UserExists>(resp).exists
            }
            if (!exists) {
                postJson("/api/create-account", Credentials("admin", password)).use { resp ->
                    // 500 "user exists" just means we raced/lost state — the
                    // login below is the real gate.
                    if (!resp.isSuccessful && resp.code != 500) {
                        throw AuthFailedException("create-account failed: ${errorBody(resp)}")
                    }
                }
            }
            postJson("/api/login", Credentials("admin", password)).use { resp ->
                when {
                    resp.isSuccessful -> Unit
                    // "not logged out" — a live session cookie already exists.
                    resp.code == 403 -> Unit
                    resp.code == 401 ->
                        throw AuthFailedException("stored password rejected: ${errorBody(resp)}")
                    else -> throw IOException("login failed (${resp.code}): ${errorBody(resp)}")
                }
            }
        }
    }

    // --- data endpoints (session-authed GETs; one re-login retry on 401) ---

    suspend fun about(): About = authedGet("/api/about")

    /** The visor's own public key, fetched once per process. */
    suspend fun localPk(): String =
        cachedPk ?: about().publicKey.also { cachedPk = it }

    suspend fun summary(): VisorSummary = authedGet("/api/visors/${localPk()}/summary")

    suspend fun serviceHealth(): List<ServiceHealthEntry> = authedGet("/api/service-health")

    suspend fun runtimeLogs(since: Long): RuntimeLogsDelta =
        authedGet("/api/visors/${localPk()}/runtime-logs?since=$since")

    /**
     * Per-app log page. The server answers HTTP 500 `"no new available
     * logs"` when nothing is new and 500 `"proc … is not found"` when the
     * app isn't running — both are empty pages here, not errors.
     */
    suspend fun appLogs(app: String, since: String?): AppLogs = withContext(Dispatchers.IO) {
        val query = since?.takeIf { it.isNotEmpty() }
            ?.let { "?since=" + URLEncoder.encode(it, "UTF-8") } ?: ""
        val resp = getWithRelogin("/api/visors/${localPk()}/apps/$app/logs$query")
        resp.use {
            if (it.isSuccessful) return@withContext decode<AppLogs>(it)
            val error = errorBody(it)
            if (it.code == 500 &&
                (error.contains("no new available logs") || error.contains("is not found"))
            ) {
                AppLogs(lastLogTimestamp = since ?: "")
            } else {
                throw IOException("app logs failed (${it.code}): $error")
            }
        }
    }

    // --- service discovery ---

    /**
     * Public servers of one service type, straight from service discovery.
     *
     * There is no dedicated REST route for this: `/api/svc-fetch` is the
     * visor's generic deployment-service proxy (`service` picks the
     * configured endpoint, `path` is passed through verbatim over
     * DMSG-HTTP), and SD's own `/api/services` takes the `type` filter —
     * `proxy` is the skysocks family, `vpn` is SkyVPN's.
     */
    suspend fun services(type: String): List<ServiceEntry> = withContext(Dispatchers.IO) {
        val path = URLEncoder.encode("/api/services?type=$type", "UTF-8")
        getWithRelogin("/api/svc-fetch?service=sd&path=$path", relayClient).use { resp ->
            if (!resp.isSuccessful) {
                throw IOException("service discovery failed (${resp.code}): ${errorBody(resp)}")
            }
            // The body is the upstream's payload verbatim, so it can be a
            // bare `null` for an empty result set — not a JSON array.
            val body = resp.body.string().trim()
            if (body.isEmpty() || body == "null") {
                emptyList()
            } else {
                json.decodeFromString(ListSerializer(ServiceEntry.serializer()), body)
            }
        }
    }

    // --- apps ---

    suspend fun app(name: String): AppState = authedGet("/api/visors/${localPk()}/apps/$name")

    /**
     * Live connections of an app. The server answers 500 when the app has
     * no running proc, and JSON `null` when it runs but holds no
     * connection yet — both are "nothing to show", not errors.
     */
    suspend fun appConnections(name: String): List<AppConnection> = withContext(Dispatchers.IO) {
        getWithRelogin("/api/visors/${localPk()}/apps/$name/connections").use { resp ->
            if (resp.code == 500) return@withContext emptyList()
            if (!resp.isSuccessful) {
                throw IOException("app connections failed (${resp.code}): ${errorBody(resp)}")
            }
            val body = resp.body.string().trim()
            if (body.isEmpty() || body == "null") {
                emptyList()
            } else {
                json.decodeFromString(ListSerializer(AppConnection.serializer()), body)
            }
        }
    }

    /**
     * Runtime stats of an app. Same "500 means no running proc" contract as
     * [appConnections]; an empty [AppStats] stands for it.
     */
    suspend fun appStats(name: String): AppStats = withContext(Dispatchers.IO) {
        getWithRelogin("/api/visors/${localPk()}/apps/$name/stats").use { resp ->
            if (resp.code == 500) return@withContext AppStats()
            if (!resp.isSuccessful) {
                throw IOException("app stats failed (${resp.code}): ${errorBody(resp)}")
            }
            decode<AppStats>(resp)
        }
    }

    /**
     * One mutating PUT on an app, carrying only the fields given:
     *  - [pk] sets the remote server (`--srv`),
     *  - [args] replaces the whole argv (shell-quoted string, parsed
     *    server-side),
     *  - [killswitch] adds or removes vpn-client's `--killswitch` flag (the
     *    visor rejects it for any other app),
     *  - [status] 1 starts, 0 stops.
     *
     * [pk], [args] and [killswitch] all restart a *running* app server-side;
     * on a stopped one they only rewrite the config, so configure-then-start
     * is a safe order in a single call.
     */
    suspend fun updateApp(
        name: String,
        pk: String? = null,
        args: String? = null,
        killswitch: Boolean? = null,
        status: Int? = null,
    ): AppState = withContext(Dispatchers.IO) {
        val body = buildJsonObject {
            pk?.let { put("pk", JsonPrimitive(it)) }
            args?.let { put("args", JsonPrimitive(it)) }
            killswitch?.let { put("killswitch", JsonPrimitive(it)) }
            status?.let { put("status", JsonPrimitive(it)) }
        }
        putWithRelogin("/api/visors/${localPk()}/apps/$name", body.toString()).use { resp ->
            if (!resp.isSuccessful) {
                throw IOException("app update failed (${resp.code}): ${errorBody(resp)}")
            }
            decode<AppState>(resp)
        }
    }

    // --- router settings ---

    suspend fun routerSettings(): RouterSettings =
        authedGet("/api/visors/${localPk()}/router-settings")

    /**
     * Install [order] as the transport-type priority order, live — the
     * visor applies it without a restart and persists it to its config.
     *
     * Read-modify-write, and it has to be: the PUT applies every field of
     * the settings struct, so sending the preference alone would also send
     * `min_hops: 0` — which the router reads as *routing disabled* — along
     * with a zeroed mux_routes.
     */
    suspend fun setTransportPreference(order: List<String>): RouterSettings =
        withContext(Dispatchers.IO) {
            val body = json.encodeToString(
                RouterSettings.serializer(),
                routerSettings().copy(transportPreference = order),
            )
            putWithRelogin("/api/visors/${localPk()}/router-settings", body).use { resp ->
                if (!resp.isSuccessful) {
                    throw IOException(
                        "router settings update failed (${resp.code}): ${errorBody(resp)}",
                    )
                }
                decode<RouterSettings>(resp)
            }
        }

    // --- voice calls ---

    /** Calls ringing right now, awaiting an answer. */
    suspend fun voiceIncoming(): List<VoiceInvite> =
        voiceList("incoming").mapNotNull(VoiceInvite::parse)

    /** Ids of calls that are connected. */
    suspend fun voiceActive(): List<String> = voiceList("active")

    /**
     * Calls this phone is PLACING and that have not been answered yet — the
     * caller's half of the picture. A call being dialed is in neither the
     * ringing list (that is the callee's) nor the active list (that starts at
     * "answered"), so without this there is nothing to show for the whole ring.
     */
    suspend fun voiceDialing(): List<VoiceInvite> = withContext(Dispatchers.IO) {
        getWithRelogin("/api/visors/${localPk()}/skychat/voice/dialing").use { resp ->
            if (resp.code == HTTP_UNAVAILABLE || !resp.isSuccessful) {
                return@withContext emptyList()
            }
            val body = resp.body.string().trim()
            if (body.isEmpty() || body == "null") {
                emptyList()
            } else {
                json.decodeFromString(ListSerializer(VoiceDialing.serializer()), body)
                    .map { VoiceInvite(it.callId, it.peer) }
            }
        }
    }

    suspend fun voiceAnswer(callId: String) = voiceAction("answer", callId)

    suspend fun voiceDecline(callId: String) = voiceAction("decline", callId)

    suspend fun voiceHangup(callId: String) = voiceAction("hangup", callId)

    /** [mic] silences what the peer hears from us; [speaker] what we hear. */
    suspend fun voiceMute(callId: String, mic: Boolean, speaker: Boolean): Unit =
        withContext(Dispatchers.IO) {
            val body = buildJsonObject {
                put("call_id", JsonPrimitive(callId))
                put("mic", JsonPrimitive(mic))
                put("speaker", JsonPrimitive(speaker))
            }.toString()
            postWithRelogin("/api/visors/${localPk()}/skychat/voice/mute", body).use { resp ->
                if (!resp.isSuccessful) {
                    throw IOException("voice mute failed (${resp.code}): ${errorBody(resp)}")
                }
            }
        }

    /**
     * Opens the microphone stream: the visor reads captured PCM from the
     * request body until [body] stops producing.
     *
     * [body] is a factory, not a body: a request body can only be written
     * once, so the re-login retry needs a fresh one. The caller owns closing
     * the returned response.
     */
    suspend fun voiceMicStream(body: () -> RequestBody): okhttp3.Response =
        withContext(Dispatchers.IO) {
            val path = "/api/voice-audio/${localPk()}/mic"
            val first = post(path, body(), csrfToken(), streamClient)
            if (first.code != 401) return@withContext first
            first.close()
            ensureSession()
            post(path, body(), csrfToken(), streamClient)
        }

    /**
     * Opens the playback stream: the response body is PCM to play, for as long
     * as it is read. The caller owns closing the returned response.
     */
    suspend fun voiceSpeakerStream(): okhttp3.Response = withContext(Dispatchers.IO) {
        getWithRelogin("/api/voice-audio/${localPk()}/speaker", streamClient)
    }

    /**
     * Opens the visor's notification stream (SSE): everything its apps
     * publish, for as long as the response is read. The caller owns closing it.
     */
    suspend fun notificationStream(): okhttp3.Response = withContext(Dispatchers.IO) {
        getWithRelogin("/api/notifications/stream", streamClient)
    }

    /**
     * 503 means the visor has voice off (or opens its own audio device) — a
     * state the poller lives with, not an error to raise every two seconds.
     */
    private suspend fun voiceList(name: String): List<String> = withContext(Dispatchers.IO) {
        getWithRelogin("/api/visors/${localPk()}/skychat/voice/$name").use { resp ->
            if (resp.code == HTTP_UNAVAILABLE) return@withContext emptyList()
            if (!resp.isSuccessful) {
                throw IOException("voice $name failed (${resp.code}): ${errorBody(resp)}")
            }
            val body = resp.body.string().trim()
            if (body.isEmpty() || body == "null") {
                emptyList()
            } else {
                json.decodeFromString(ListSerializer(String.serializer()), body)
            }
        }
    }

    private suspend fun voiceAction(name: String, callId: String): Unit =
        withContext(Dispatchers.IO) {
            val body = buildJsonObject { put("call_id", JsonPrimitive(callId)) }.toString()
            postWithRelogin("/api/visors/${localPk()}/skychat/voice/$name", body).use { resp ->
                if (!resp.isSuccessful) {
                    throw IOException("voice $name failed (${resp.code}): ${errorBody(resp)}")
                }
            }
        }

    /** Fresh 30-second CSRF token for a mutating `/api/visors/{pk}/…` call. */
    suspend fun csrfToken(): String = withContext(Dispatchers.IO) {
        get("/api/csrf").use { decode<CsrfToken>(it).token }
    }

    // --- plumbing ---

    private suspend inline fun <reified T> authedGet(path: String): T =
        withContext(Dispatchers.IO) {
            getWithRelogin(path).use { resp ->
                if (!resp.isSuccessful) {
                    throw IOException("GET $path failed (${resp.code}): ${errorBody(resp)}")
                }
                decode<T>(resp)
            }
        }

    private suspend fun getWithRelogin(
        path: String,
        client: OkHttpClient = this.client,
    ): okhttp3.Response {
        val first = get(path, client)
        if (first.code != 401) return first
        first.close()
        ensureSession()
        return get(path, client)
    }

    /**
     * A mutation needs a fresh CSRF token per attempt — the token lives 30
     * seconds and the retry happens after a full re-login round trip.
     */
    private suspend fun putWithRelogin(path: String, body: String): okhttp3.Response {
        val first = put(path, body, csrfToken())
        if (first.code != 401) return first
        first.close()
        ensureSession()
        return put(path, body, csrfToken())
    }

    private suspend fun postWithRelogin(path: String, body: String): okhttp3.Response {
        val payload = { body.toRequestBody("application/json".toMediaType()) }
        val first = post(path, payload(), csrfToken())
        if (first.code != 401) return first
        first.close()
        ensureSession()
        return post(path, payload(), csrfToken())
    }

    private fun get(path: String, client: OkHttpClient = this.client): okhttp3.Response =
        client.newCall(Request.Builder().url("$BASE$path").build()).execute()

    private fun post(
        path: String,
        body: RequestBody,
        csrf: String,
        client: OkHttpClient = this.client,
    ): okhttp3.Response =
        client.newCall(
            Request.Builder()
                .url("$BASE$path")
                .header(CSRF_HEADER, csrf)
                .post(body)
                .build(),
        ).execute()

    private fun put(path: String, body: String, csrf: String): okhttp3.Response =
        client.newCall(
            Request.Builder()
                .url("$BASE$path")
                .header(CSRF_HEADER, csrf)
                .put(body.toRequestBody("application/json".toMediaType()))
                .build(),
        ).execute()

    private inline fun <reified T> postJson(path: String, body: T): okhttp3.Response {
        val payload = json.encodeToString(
            kotlinx.serialization.serializer<T>(),
            body,
        ).toRequestBody("application/json".toMediaType())
        return client.newCall(Request.Builder().url("$BASE$path").post(payload).build()).execute()
    }

    private inline fun <reified T> decode(resp: okhttp3.Response): T =
        json.decodeFromString(kotlinx.serialization.serializer<T>(), resp.body.string())

    private fun errorBody(resp: okhttp3.Response): String = runCatching {
        val text = resp.body.string()
        runCatching {
            json.decodeFromString(ApiError.serializer(), text).error
        }.getOrDefault(text)
    }.getOrDefault("(no body)").take(500)

    companion object {
        /** `status` values [updateApp] takes — start and stop an app. */
        const val APP_STOP = 0
        const val APP_START = 1

        private const val BASE = "http://127.0.0.1:8000"
        private const val CSRF_HEADER = "X-CSRF-Token"

        /** The visor's "that feature is off here" answer. */
        private const val HTTP_UNAVAILABLE = 503

        @Volatile private var instance: VisorApi? = null

        fun get(context: Context): VisorApi =
            instance ?: synchronized(this) {
                instance ?: VisorApi(context.applicationContext).also { instance = it }
            }
    }
}

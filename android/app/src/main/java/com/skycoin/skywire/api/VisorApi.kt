package com.skycoin.skywire.api

import android.content.Context
import com.skycoin.skywire.core.SecretStore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
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
     * re-arms its own deadline per frame.
     *
     * Which makes ending a stream entirely this side's job, and the only
     * thing that ends one is cancelling its [okhttp3.Call] — see the `onCall`
     * hook on the two stream openers below. With no timeout to fall back on,
     * a stream nobody cancels runs until the process dies.
     */
    private val streamClient = client.newBuilder()
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .writeTimeout(0, TimeUnit.MILLISECONDS)
        .callTimeout(0, TimeUnit.MILLISECONDS)
        .build()

    /**
     * For sending mail, which the visor delivers before it answers: a dmsg
     * dial and an SMTP exchange per recipient, and a skynet route setup for a
     * .skynet address. The budget has to outlast the slowest relay worth
     * waiting for — the visor finishes a delivery this side gave up on, so
     * an early timeout reads a sent mail as failed.
     */
    private val mailClient = client.newBuilder()
        .readTimeout(5, TimeUnit.MINUTES)
        .callTimeout(5, TimeUnit.MINUTES)
        // A send must never be replayed: OkHttp's silent retry on a stale
        // pooled connection would deliver the same mail twice.
        .retryOnConnectionFailure(false)
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

    /**
     * Forget the cached key. This client is a process-lifetime singleton and
     * every `/api/visors/{pk}/…` route is built from [localPk], so after an
     * identity change the cache would address the visor by a key it no longer
     * has — every call 404s until the app is killed. Settings calls this the
     * moment it replaces the identity.
     */
    fun forgetIdentity() {
        cachedPk = null
    }

    suspend fun summary(): VisorSummary = authedGet("/api/visors/${localPk()}/summary")

    suspend fun serviceHealth(): List<ServiceHealthEntry> = authedGet("/api/service-health")

    /**
     * Runtime-log page of [pk], defaulting to this phone's own visor. Fleet
     * passes a remote key: the same route resolves it through the hypervisor
     * mux and answers from that visor's ring buffer.
     */
    suspend fun runtimeLogs(since: Long, pk: String? = null): RuntimeLogsDelta =
        authedGet("/api/visors/${pk ?: localPk()}/runtime-logs?since=$since")

    // --- fleet (remote visors that dialed in over dmsg) ---

    /**
     * Every visor this one knows about — the local visor first, then each
     * remote that connected in over dmsg. Offline remotes stay in the list,
     * served from the last snapshot with `online: false`.
     *
     * The list is only ever longer than one when the Fleet opt-in is on: with
     * the ingest off there is no listener for a remote visor to reach.
     */
    suspend fun visorsSummary(): List<VisorSummary> = authedGet("/api/visors-summary")

    /**
     * Restart [pk] — the visor closes its module stack, re-reads its config and
     * runs again, in the same process.
     *
     * The server answers 202 without waiting, and it has to: the restart tears
     * down the RPC connection carrying the request, so there is no success to
     * report. What actually happened shows up in the next [visorsSummary] —
     * the visor drops to offline and comes back.
     */
    suspend fun restartVisor(pk: String): Unit = withContext(Dispatchers.IO) {
        repeat(RESTART_ATTEMPTS) { attempt ->
            postWithRelogin("/api/visors/$pk/restart", "{}").use { resp ->
                if (resp.isSuccessful) return@withContext
                // 503 means "known but not connected right now, retrying" —
                // and that is a window the phone hits routinely, not an error:
                // a managed visor's RPC connection idle-closes after about two
                // minutes and redials within seconds, while the summary this
                // screen was drawn from still reports it online. The server
                // says "retrying" and expects the caller to; failing the tap
                // for a visor that is fine would be the wrong answer.
                if (resp.code != HTTP_UNAVAILABLE || attempt == RESTART_ATTEMPTS - 1) {
                    throw IOException("visor restart failed (${resp.code}): ${errorBody(resp)}")
                }
            }
            delay(RESTART_RETRY_MS)
        }
    }

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
     *  - [autostart] whether the visor starts the app with itself,
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
        autostart: Boolean? = null,
        status: Int? = null,
    ): AppState = withContext(Dispatchers.IO) {
        val body = buildJsonObject {
            pk?.let { put("pk", JsonPrimitive(it)) }
            args?.let { put("args", JsonPrimitive(it)) }
            killswitch?.let { put("killswitch", JsonPrimitive(it)) }
            autostart?.let { put("autostart", JsonPrimitive(it)) }
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
     */
    suspend fun setTransportPreference(order: List<String>): RouterSettings =
        updateRouterSettings { it.copy(transportPreference = order) }

    /**
     * Set the minimum number of route hops the visor dials through. 1 allows
     * a direct route; 2 or more forces the traffic through intermediaries,
     * which is what buys sender privacy at the cost of latency.
     */
    suspend fun setMinHops(hops: Int): RouterSettings =
        updateRouterSettings { it.copy(minHops = hops) }

    /**
     * Read-modify-write of the router knobs, and it has to be: the PUT
     * applies every field of the settings struct, so sending one field alone
     * would also send `min_hops: 0` — which the router reads as *routing
     * disabled* — along with a zeroed mux_routes. Every caller goes through
     * here so no future setter can rediscover that the hard way.
     */
    private suspend fun updateRouterSettings(
        edit: (RouterSettings) -> RouterSettings,
    ): RouterSettings = withContext(Dispatchers.IO) {
        val body = json.encodeToString(RouterSettings.serializer(), edit(routerSettings()))
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
    suspend fun voiceDialing(): List<OutgoingCall> = withContext(Dispatchers.IO) {
        getWithRelogin("/api/visors/${localPk()}/skychat/voice/dialing").use { resp ->
            if (resp.code == HTTP_UNAVAILABLE || !resp.isSuccessful) {
                return@withContext emptyList()
            }
            val body = resp.body.string().trim()
            if (body.isEmpty() || body == "null") {
                emptyList()
            } else {
                json.decodeFromString(ListSerializer(VoiceDialing.serializer()), body)
                    .map { OutgoingCall(it.callId, it.peer, DialState.parse(it.state), it.ringback) }
            }
        }
    }

    /**
     * The other side's ringback tone for an outbound call, or null while it
     * has not arrived — and for a peer that plays none. Up to a megabyte.
     */
    suspend fun voiceDialRingback(callId: String): ByteArray? = withContext(Dispatchers.IO) {
        val id = URLEncoder.encode(callId, "UTF-8")
        getWithRelogin("/api/visors/${localPk()}/skychat/voice/ringback?call=$id").use { resp ->
            if (!resp.isSuccessful) null else resp.body.bytes().takeIf { it.isNotEmpty() }
        }
    }

    /** Place a call to [peer]; returns its id at once, before it is answered. */
    suspend fun voiceCall(peer: String): String = withContext(Dispatchers.IO) {
        val body = buildJsonObject { put("peer", JsonPrimitive(peer)) }.toString()
        postWithRelogin("/api/visors/${localPk()}/skychat/voice/call", body).use { resp ->
            if (!resp.isSuccessful) {
                throw IOException("voice call failed (${resp.code}): ${errorBody(resp)}")
            }
            (json.parseToJsonElement(resp.body.string()) as? JsonObject)
                ?.get("call_id")?.jsonPrimitive?.contentOrNull.orEmpty()
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
     *
     * [onCall] receives each attempt's call — including the retry's, so the
     * caller always holds the current one. It is the only way to end this
     * stream: the upload is written from inside `execute()`, so this function
     * does not return until the body stops producing, and cancelling the
     * coroutine cannot reach a thread parked in there. The caller must keep
     * the call and cancel it to stop early.
     */
    suspend fun voiceMicStream(
        body: () -> RequestBody,
        onCall: (okhttp3.Call) -> Unit = {},
    ): okhttp3.Response =
        withContext(Dispatchers.IO) {
            val path = "/api/voice-audio/${localPk()}/mic"
            val first = post(path, body(), csrfToken(), streamClient, onCall)
            if (first.code != 401) return@withContext first
            first.close()
            ensureSession()
            post(path, body(), csrfToken(), streamClient, onCall)
        }

    /**
     * Opens the playback stream: the response body is PCM to play, for as long
     * as it is read. The caller owns closing the returned response.
     *
     * [onCall] as above — cancelling the call is what unblocks a reader parked
     * on the body.
     */
    suspend fun voiceSpeakerStream(
        onCall: (okhttp3.Call) -> Unit = {},
    ): okhttp3.Response = withContext(Dispatchers.IO) {
        getWithRelogin("/api/voice-audio/${localPk()}/speaker", streamClient, onCall)
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

    /**
     * Tell the core what the phone knows about the network.
     *
     * "moved" when the default network changed underneath it: the old sockets
     * are dead but nothing on either end knows it yet. The core re-dials dmsg,
     * looks its public IP and NAT up again and registers its new addresses;
     * the hypervisor RPC conn rides one of the dmsg sessions, so it comes back
     * with them. The metered flag lets the core stop carrying other visors'
     * traffic on mobile data. Returns the core's mode afterwards.
     */
    internal suspend fun networkEvent(ev: NetworkEvent): NetworkReply = withContext(Dispatchers.IO) {
        val body = json.encodeToString(NetworkEvent.serializer(), ev)
        postWithRelogin("/api/network", body).use { resp ->
            if (!resp.isSuccessful) {
                throw IOException("network event failed (${resp.code}): ${errorBody(resp)}")
            }
            decode<NetworkReply>(resp)
        }
    }

    // --- mail: the visor's Skymail mailbox ---

    suspend fun mailStatus(): MailStatus = authedGet("/api/visors/${localPk()}/mail")

    suspend fun mailList(folder: String): List<MailSummary> = authedGet(mailPath(folder))

    /** The message reduced to text, and marked read. */
    suspend fun mailRead(folder: String, id: String): MailMessage = authedGet(mailPath(folder, id))

    suspend fun mailAttachment(folder: String, id: String, n: Int): ByteArray = withContext(Dispatchers.IO) {
        getWithRelogin(mailPath(folder, id) + "/attachments/$n").use { resp ->
            if (!resp.isSuccessful) throw IOException("attachment $n failed (${resp.code}): ${errorBody(resp)}")
            resp.body.bytes()
        }
    }

    suspend fun mailDelete(folder: String, id: String): Unit = withContext(Dispatchers.IO) {
        deleteWithRelogin(mailPath(folder, id)).use { resp ->
            if (!resp.isSuccessful) throw IOException("delete failed (${resp.code}): ${errorBody(resp)}")
        }
    }

    /**
     * Delivers now and answers per recipient. A send that reached nobody comes
     * back as a result with [MailSendResult.error] set, not as an exception, so
     * the screen can say which address failed and why.
     */
    suspend fun mailSend(msg: MailOutgoing): MailSendResult = withContext(Dispatchers.IO) {
        val body = json.encodeToString(MailOutgoing.serializer(), msg)
        postWithRelogin("/api/visors/${localPk()}/mail/send", body, mailClient).use { resp ->
            val text = resp.body.string()
            val result = runCatching { json.decodeFromString(MailSendResult.serializer(), text) }.getOrNull()
            when {
                result != null && (resp.isSuccessful || result.error.isNotEmpty()) -> result
                else -> throw IOException("send failed (${resp.code}): ${text.take(500)}")
            }
        }
    }

    /** Who may deliver; empty accepts everyone. */
    suspend fun mailSetWhitelist(pks: List<String>): Unit = withContext(Dispatchers.IO) {
        val body = buildJsonObject {
            put("pks", kotlinx.serialization.json.JsonArray(pks.map(::JsonPrimitive)))
        }.toString()
        putWithRelogin("/api/visors/${localPk()}/mail/whitelist", body).use { resp ->
            if (!resp.isSuccessful) throw IOException("whitelist failed (${resp.code}): ${errorBody(resp)}")
        }
    }

    /** Turns the mailbox on or off at once; the visor keeps the choice with the mail. */
    suspend fun mailSetEnabled(enabled: Boolean): Unit = withContext(Dispatchers.IO) {
        val body = buildJsonObject { put("enable", JsonPrimitive(enabled)) }.toString()
        putWithRelogin("/api/visors/${localPk()}/mail/settings", body).use { resp ->
            if (!resp.isSuccessful) throw IOException("settings failed (${resp.code}): ${errorBody(resp)}")
        }
    }

    private suspend fun mailPath(folder: String, id: String? = null): String {
        val base = "/api/visors/${localPk()}/mail/${URLEncoder.encode(folder, "UTF-8")}"
        return if (id == null) base else "$base/${URLEncoder.encode(id, "UTF-8")}"
    }

    // --- contacts: the visor's address book, shared with skychat and the dashboard ---

    /** Every contact, sorted by name. */
    suspend fun contacts(): List<BookContact> = authedGet("/api/visors/${localPk()}/contacts")

    /** Adds [contact], or replaces the one with its uid, and answers it as stored. */
    suspend fun contactPut(contact: BookContact): BookContact = withContext(Dispatchers.IO) {
        val body = json.encodeToString(BookContact.serializer(), contact)
        postWithRelogin("/api/visors/${localPk()}/contacts", body).use { resp ->
            if (resp.code == HTTP_BAD_REQUEST) throw ContactRejectedException(errorBody(resp))
            if (!resp.isSuccessful) throw IOException(errorBody(resp))
            decode<BookContact>(resp)
        }
    }

    /** Removes the contact with [uid]; one already gone is no error. */
    suspend fun contactDelete(uid: String): Unit = withContext(Dispatchers.IO) {
        deleteWithRelogin("/api/visors/${localPk()}/contacts/${URLEncoder.encode(uid, "UTF-8")}").use { resp ->
            if (!resp.isSuccessful && resp.code != HTTP_NOT_FOUND) throw IOException(errorBody(resp))
        }
    }

    /** The reward address: an account xpub, a single address set before xpubs were required, or "" for none. */
    suspend fun rewardAddress(): String = authedGet("/api/visors/${localPk()}/reward")

    /** Sets the reward address. The visor takes only an account xpub, and says why it refused anything else. */
    suspend fun setRewardAddress(xpub: String): Unit = withContext(Dispatchers.IO) {
        val body = buildJsonObject { put("reward_address", JsonPrimitive(xpub)) }.toString()
        putWithRelogin("/api/visors/${localPk()}/reward", body).use { resp ->
            if (!resp.isSuccessful) throw IOException(errorBody(resp))
        }
    }

    /** Clears the reward address, which opts this visor out of rewards. */
    suspend fun deleteRewardAddress(): Unit = withContext(Dispatchers.IO) {
        deleteWithRelogin("/api/visors/${localPk()}/reward").use { resp ->
            if (!resp.isSuccessful) throw IOException(errorBody(resp))
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
        onCall: (okhttp3.Call) -> Unit = {},
    ): okhttp3.Response {
        val first = get(path, client, onCall)
        if (first.code != 401) return first
        first.close()
        ensureSession()
        return get(path, client, onCall)
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

    private suspend fun postWithRelogin(
        path: String,
        body: String,
        client: OkHttpClient = this.client,
    ): okhttp3.Response {
        val payload = { body.toRequestBody("application/json".toMediaType()) }
        val first = post(path, payload(), csrfToken(), client)
        if (first.code != 401) return first
        first.close()
        ensureSession()
        return post(path, payload(), csrfToken(), client)
    }

    private suspend fun deleteWithRelogin(path: String): okhttp3.Response {
        val first = delete(path, csrfToken())
        if (first.code != 401) return first
        first.close()
        ensureSession()
        return delete(path, csrfToken())
    }

    // onCall hands the call out BEFORE it is executed, which is the only
    // moment that works for a long-lived stream: execute() blocks until the
    // exchange is over, so a caller that waited for the return value would be
    // holding a call it no longer needs to cancel. Default no-op — every
    // request that completes on its own ignores it.
    private fun get(
        path: String,
        client: OkHttpClient = this.client,
        onCall: (okhttp3.Call) -> Unit = {},
    ): okhttp3.Response =
        client.newCall(Request.Builder().url("$BASE$path").build())
            .also(onCall)
            .execute()

    private fun post(
        path: String,
        body: RequestBody,
        csrf: String,
        client: OkHttpClient = this.client,
        onCall: (okhttp3.Call) -> Unit = {},
    ): okhttp3.Response =
        client.newCall(
            Request.Builder()
                .url("$BASE$path")
                .header(CSRF_HEADER, csrf)
                .post(body)
                .build(),
        ).also(onCall).execute()

    private fun delete(path: String, csrf: String): okhttp3.Response =
        client.newCall(
            Request.Builder()
                .url("$BASE$path")
                .header(CSRF_HEADER, csrf)
                .delete()
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

        /**
         * The visor's "that feature is off here" answer — and, on the
         * `/visors/{pk}/…` routes, "that visor is reconnecting".
         */
        private const val HTTP_UNAVAILABLE = 503
        private const val HTTP_NOT_FOUND = 404
        private const val HTTP_BAD_REQUEST = 400

        /** Enough to outlast a hypervisor-client redial (measured ~4 s). */
        private const val RESTART_ATTEMPTS = 4
        private const val RESTART_RETRY_MS = 3_000L

        @Volatile private var instance: VisorApi? = null

        fun get(context: Context): VisorApi =
            instance ?: synchronized(this) {
                instance ?: VisorApi(context.applicationContext).also { instance = it }
            }
    }
}

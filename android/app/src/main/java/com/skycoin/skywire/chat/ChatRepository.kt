package com.skycoin.skywire.chat

import android.content.Context
import android.util.Log
import androidx.annotation.StringRes
import com.skycoin.skywire.R
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.SkychatApi
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppVisibility
import com.skycoin.skywire.core.ChatPageLog
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SkychatProfile
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import kotlin.coroutines.coroutineContext

/** A message for the snackbar: a string resource and its arguments. */
data class ChatNote(@StringRes val res: Int, val args: List<Any> = emptyList(), val error: Boolean = false)

/** How far the chat surface is from usable. */
data class ChatConn(
    val core: CoreState = CoreState.Stopped,
    val apiUp: Boolean = false,
    val starting: Boolean = false,
    val ready: Boolean = false,
    val error: String? = null,
)

/** CXO pairing as the list and the DM header need it. */
data class PairUi(
    val available: Boolean = false,
    val paired: Set<String> = emptySet(),
    val records: Map<String, PairRecord> = emptyMap(),
    val invites: List<String> = emptyList(),
)

/** One list row's live facts. */
data class ThreadSummary(val last: ChatMessage? = null, val unread: Int = 0)

/** Group backlog paging. */
data class HistPage(val cursor: String? = null, val done: Boolean = false, val loading: Boolean = false, val loaded: Boolean = false)

/**
 * Process-wide SkyChat state for the native screens: it brings skychat up,
 * holds every conversation, follows `/sse`, and does what the web page did
 * in the browser — local threads, unread counts, receipts, the focus lease.
 */
class ChatRepository private constructor(internal val app: Context) {

    internal val http = SkychatHttp(app)
    val api = SkychatClient(http)
    val store = ChatLocalStore(app)
    val media = ChatMediaCache(app, http)
    private val visor = VisorApi.get(app)
    internal val scope = CoroutineScope(SupervisorJob() + Dispatchers.Main.immediate)

    internal val connMutable = MutableStateFlow(ChatConn())
    val conn: StateFlow<ChatConn> = connMutable.asStateFlow()

    val sseUp = MutableStateFlow(false)
    val myPk = MutableStateFlow("")
    val names = MutableStateFlow<Map<String, String>>(emptyMap())
    val profile = MutableStateFlow<Profile?>(null)
    val profileAvailable = MutableStateFlow<Boolean?>(null)
    val groups = MutableStateFlow<List<GroupInfo>>(emptyList())
    val groupsAvailable = MutableStateFlow<Boolean?>(null)
    val pair = MutableStateFlow(PairUi())
    val privacy = MutableStateFlow<PrivacyState?>(null)
    val presence = MutableStateFlow<Map<String, PresencePeer>>(emptyMap())
    val presenceAvailable = MutableStateFlow<Boolean?>(null)
    val voiceAvailable = MutableStateFlow(false)
    val links = MutableStateFlow<Map<String, LinkState>>(emptyMap())
    val uploads = MutableStateFlow<Map<String, Pair<Long, Long>>>(emptyMap())
    val groupUnread = MutableStateFlow<Map<String, Int>>(emptyMap())
    val hist = MutableStateFlow<Map<String, HistPage>>(emptyMap())
    val summaries = MutableStateFlow<Map<String, ThreadSummary>>(emptyMap())

    /** Route for DM sends: `skynet`, `dmsg` or `cxo`. */
    val network = MutableStateFlow("skynet")

    /** The conversation on screen, and whether its newest message is in view. */
    val openKey = MutableStateFlow<String?>(null)

    @Volatile
    var atBottom = true

    private val notesFlow = MutableSharedFlow<ChatNote>(extraBufferCapacity = 16)
    val notes: SharedFlow<ChatNote> = notesFlow.asSharedFlow()

    /** Events the open admin panel reacts to. */
    val groupChanged = MutableSharedFlow<String>(extraBufferCapacity = 8)

    internal val threads = HashMap<String, MutableStateFlow<List<ChatMessage>>>()
    internal val orphanStatus = LinkedHashMap<String, String>()
    internal val outbox = HashMap<String, MutableList<Outgoing>>()
    internal var linkOff = false
    internal val groupMetaAskedAt = HashMap<String, Long>()

    internal data class Outgoing(val msgKey: String, val wire: String, val network: String)

    private var started = false
    private var session: Job? = null
    private var postedUnread = -1

    fun note(@StringRes res: Int, vararg args: Any, error: Boolean = false) {
        notesFlow.tryEmit(ChatNote(res, args.toList(), error))
    }

    /** A server error body as a note: "<prefix>: <body>". */
    fun fail(@StringRes res: Int, e: Throwable) {
        note(res, errorText(e), error = true)
    }

    fun errorText(e: Throwable): String = when (e) {
        is SkychatHttpException -> e.message ?: "HTTP ${e.code}"
        // A dropped connection or a timeout, not an answer: say so plainly.
        is IOException -> app.getString(R.string.chat_error_no_reply)
        else -> e.message ?: e.javaClass.simpleName
    }

    // --- lifecycle ---

    /**
     * Follow the core: whenever it is running, bring skychat up (starting
     * it only when [startApp]) and run the session. Safe to call repeatedly.
     */
    fun start(startApp: Boolean) {
        if (startApp) wantApp = true
        if (started) {
            if (startApp && !connMutable.value.ready) retry()
            return
        }
        started = true
        scope.launch {
            CoreServiceState.state.collectLatest { core ->
                session?.cancel()
                connMutable.update { ChatConn(core = core) }
                sseUp.value = false
                http.baseUrl = null
                if (core !is CoreState.Running) return@collectLatest
                while (!visor.ping()) delay(PING_MS)
                connMutable.update { it.copy(apiUp = true) }
                session = scope.launch { runSession() }
            }
        }
    }

    @Volatile
    private var wantApp = false

    fun retry() {
        val c = connMutable.value
        if (c.core !is CoreState.Running || !c.apiUp) return
        session?.cancel()
        session = scope.launch { runSession() }
    }

    /** One live session: every loop is its child, so cancelling the session stops them all. */
    private suspend fun runSession() {
        val url = bringUp() ?: return
        http.baseUrl = url
        val me = runCatching { api.status() }.getOrNull()
        if (me == null || me.visorPk.isEmpty()) {
            connMutable.update { it.copy(starting = false, error = app.getString(R.string.chat_error_no_answer, url)) }
            return
        }
        adopt(me.visorPk.lowercase())
        connMutable.update { it.copy(ready = true, starting = false, error = null) }
        coroutineScope {
            launch { syncNames() }
            launch { syncPairs() }
            launch { syncPrivacy() }
            launch { syncGroups() }
            launch { syncProfile() }
            launch { sseLoop() }
            launch { presenceLoop() }
            launch { pairLoop() }
            launch { voiceLoop() }
            launch { focusLoop() }
            launch { unreadLoop() }
        }
    }

    /** skychat's own listener, started if it is not running and [wantApp]; null when it never answered. */
    private suspend fun bringUp(): String? {
        connMutable.update { it.copy(starting = true, error = null) }
        var startError: String? = null
        val state = try {
            val current = visor.app(SkychatProfile.APP)
            val running = current.status == AppState.STATUS_RUNNING || current.status == AppState.STATUS_STARTING
            if (!running && !wantApp) {
                connMutable.update { it.copy(starting = false) }
                return null
            }
            if (!running) {
                runCatching { visor.updateApp(SkychatProfile.APP, status = VisorApi.APP_START) }
                    .onFailure { startError = it.message }
            }
            current
        } catch (e: Exception) {
            connMutable.update { it.copy(starting = false, error = e.message) }
            return null
        }
        val url = SkychatProfile.baseUrl(SkychatProfile.listenPort(state.args))
        val legacy = SkychatApi.get(app)
        var reloadedGate = false
        repeat(READY_ATTEMPTS) {
            when (legacy.probe(url + "status")) {
                HTTP_OK -> return url
                HTTP_UNAUTHORIZED -> if (!reloadedGate) {
                    // The running app holds an older password file; a restart reloads it.
                    reloadedGate = true
                    runCatching {
                        visor.updateApp(SkychatProfile.APP, status = VisorApi.APP_STOP)
                        visor.updateApp(SkychatProfile.APP, status = VisorApi.APP_START)
                    }.onFailure { startError = it.message }
                }
            }
            delay(READY_MS)
        }
        connMutable.update {
            it.copy(starting = false, error = startError ?: app.getString(R.string.chat_error_no_answer, url))
        }
        return null
    }

    /** Open this identity's local store and load its threads. */
    private suspend fun adopt(pk: String) {
        myPk.value = pk
        val fresh = withContext(Dispatchers.IO) { store.open(pk) }
        if (!fresh && threads.isNotEmpty()) return
        val loaded = withContext(Dispatchers.IO) {
            store.threadKeys().associateWith { key ->
                // A message held for a link when the app died will never go by itself.
                store.loadThread(key).map { m -> if (m.status == "queued") m.copy(status = "failed") else m }
            }
        }
        threads.clear()
        for ((key, list) in loaded) {
            if (key == pk) continue
            threads[key] = MutableStateFlow(list)
        }
        threads.getOrPut(SAVED_KEY) { MutableStateFlow(emptyList()) }
        // The page's old bug: our own mirror filed as a conversation with ourselves.
        store.update { s -> s.copy(recipients = s.recipients.filter { it != pk }) }
        recomputeAll()
        LegacyImport.maybeRun(this)
    }

    // --- threads ---

    fun thread(key: String): StateFlow<List<ChatMessage>> = threadFlow(key)

    internal fun threadFlow(key: String): MutableStateFlow<List<ChatMessage>> =
        threads.getOrPut(key) { MutableStateFlow(emptyList()) }

    internal fun isGroupKey(key: String): Boolean = key != SAVED_KEY && !isPk(key)

    /** Change one thread; DM and Saved threads are written to disk. */
    internal fun mutate(key: String, change: (List<ChatMessage>) -> List<ChatMessage>) {
        val flow = threadFlow(key)
        val next = change(flow.value)
        if (next === flow.value) return
        flow.value = next
        if (!isGroupKey(key)) store.saveThread(key, next)
        recompute(key)
    }

    internal fun updateMessage(key: String, msgKey: String, change: (ChatMessage) -> ChatMessage) {
        mutate(key) { list ->
            val i = list.indexOfFirst { it.key == msgKey }
            if (i < 0) list else list.toMutableList().also { it[i] = change(it[i]) }
        }
    }

    internal fun findMessage(key: String, msgKey: String): ChatMessage? = threads[key]?.value?.firstOrNull { it.key == msgKey }

    internal fun appendMessage(key: String, m: ChatMessage) {
        mutate(key) { list -> insertSorted(list, m) }
    }

    private fun insertSorted(list: List<ChatMessage>, m: ChatMessage): List<ChatMessage> {
        if (list.isEmpty() || list.last().ts <= m.ts) return list + m
        val i = list.indexOfFirst { it.ts > m.ts }
        return list.toMutableList().also { it.add(i, m) }
    }

    internal fun recompute(key: String) {
        val list = threads[key]?.value.orEmpty()
        val last = list.lastOrNull()
        val unread = if (isGroupKey(key)) {
            groupUnread.value[key] ?: 0
        } else {
            val seen = store.state.value.readUpTo[key] ?: 0L
            list.count { !it.mine && it.ts > seen }
        }
        summaries.update { it + (key to ThreadSummary(last, unread)) }
    }

    internal fun recomputeAll() {
        threads.keys.toList().forEach { recompute(it) }
    }

    /** The thread was seen up to its newest message. */
    fun markRead(key: String) {
        if (isGroupKey(key)) {
            if ((groupUnread.value[key] ?: 0) != 0) groupUnread.update { it - key }
            recompute(key)
            return
        }
        val newest = threads[key]?.value?.lastOrNull()?.ts ?: return
        if ((store.state.value.readUpTo[key] ?: 0L) >= newest) return
        store.update { s -> s.copy(readUpTo = s.readUpTo + (key to newest)) }
        recompute(key)
    }

    /** Add a DM row for [pk] if it has none. */
    fun ensureRecipient(pk: String) {
        val p = pk.lowercase()
        if (!isPk(p) || p == myPk.value) return
        if (p !in store.state.value.recipients) store.update { s -> s.copy(recipients = s.recipients + p) }
        if (threads[p] == null) {
            threads[p] = MutableStateFlow(emptyList())
            recompute(p)
        }
    }

    fun totalUnread(): Int = summaries.value.filterKeys { isPk(it) }.values.sumOf { it.unread }

    // --- the open conversation ---

    /** The screen opened [key] (or closed every thread with null). */
    fun setOpen(key: String?) {
        openKey.value = key
        atBottom = true
        LinkWatch.watch(this, key?.takeIf { isPk(it) })
        if (key == null) return
        markRead(key)
        when {
            key == SAVED_KEY -> Unit
            isPk(key) -> scope.launch { loadDmHistory(key); sendReadReceipts(key) }
            else -> scope.launch { openGroup(key) }
        }
    }

    fun setAtBottom(key: String, bottom: Boolean) {
        if (openKey.value != key) return
        val was = atBottom
        atBottom = bottom
        if (bottom && !was) {
            markRead(key)
            if (isPk(key)) scope.launch { sendReadReceipts(key) }
        }
    }

    internal suspend fun sendReadReceipts(pk: String) {
        val pending = threads[pk]?.value.orEmpty().filter { !it.mine && !it.viaPair && it.wireId.isNotEmpty() && !it.readSent }
        if (pending.isEmpty()) return
        val ids = pending.map { it.wireId }.toSet()
        mutate(pk) { list -> list.map { if (it.wireId in ids && !it.mine) it.copy(readSent = true) else it } }
        runCatching { api.readReceipt(pk, ids.toList()) }
    }

    // --- DM history ---

    /** Newest stored DMs for one peer, merged into the local thread. */
    internal suspend fun loadDmHistory(pk: String) {
        val records = runCatching { api.history(pk, 100) }.getOrNull() ?: return
        mergeHistory(pk, records)
    }

    /** After every (re)connect: the newest stored DMs of every peer, so nothing missed while away stays missing. */
    internal suspend fun catchUp() {
        runCatching { api.historyPeers() }.getOrNull()?.forEach { ensureRecipient(it) }
        val records = runCatching { api.recentHistory(CATCH_UP) }.getOrNull() ?: return
        records.groupBy { it.peer.lowercase() }.forEach { (pk, list) ->
            if (isPk(pk)) {
                ensureRecipient(pk)
                mergeHistory(pk, list)
            }
        }
        if (!store.state.value.initialized) {
            // First run: what is already stored is history, not news.
            val now = threads.mapNotNull { (k, f) -> f.value.lastOrNull()?.let { k to it.ts } }.toMap()
            store.update { s -> s.copy(initialized = true, readUpTo = now + s.readUpTo) }
            recomputeAll()
        }
    }

    internal fun mergeHistory(pk: String, records: List<HistoryRecord>) {
        if (records.isEmpty()) return
        mutate(pk) { current ->
            val out = current.toMutableList()
            var changed = false
            for (r in records) {
                val m = fromHistory(pk, r) ?: continue
                val i = out.indexOfFirst { same(it, m) }
                if (i >= 0) {
                    val e = out[i]
                    val f = e.file
                    val nf = m.file
                    if (f != null && nf != null && (f.url.isEmpty() && nf.url.isNotEmpty() || f.id.isEmpty())) {
                        out[i] = e.copy(file = f.copy(url = f.url.ifEmpty { nf.url }, id = f.id.ifEmpty { nf.id }))
                        changed = true
                    }
                    if (e.wireId.isEmpty() && m.wireId.isNotEmpty()) {
                        out[i] = out[i].copy(wireId = m.wireId, status = if (e.mine && statusRank(e.status) < statusRank("sent")) "sent" else e.status)
                        changed = true
                    }
                    continue
                }
                out += m
                changed = true
            }
            if (!changed) current else out.sortedBy { it.ts }
        }
    }

    private fun same(a: ChatMessage, b: ChatMessage): Boolean {
        if (a.dmWireId.isNotEmpty() && b.dmWireId.isNotEmpty()) return a.dmWireId == b.dmWireId
        return a.mine == b.mine && a.text == b.text && kotlin.math.abs(a.ts - b.ts) < SAME_WINDOW_MS
    }

    private fun fromHistory(pk: String, r: HistoryRecord): ChatMessage? {
        val ts = parseTime(r.timestamp) ?: return null
        val callOut = r.outgoing && r.text.startsWith("📞")
        val from = if (r.outgoing || callOut) ChatMessage.ME else (r.from.ifEmpty { r.peer }).lowercase()
        val file = if (r.fileName.isNotEmpty()) {
            ChatFile(
                id = r.fileId, name = r.fileName, size = r.fileSize, status = r.fileStatus, url = r.fileUrl,
                from = if (r.outgoing) "" else from,
            )
        } else {
            null
        }
        return ChatMessage(
            key = "h:" + (r.id.ifEmpty { r.fileId }.ifEmpty { "$ts:${r.text.hashCode()}" }),
            ts = ts,
            from = from,
            text = r.text,
            file = file,
            reply = replyOf(r.replyToSender, r.replyToTs, r.replyToPreview),
            forwarded = r.forwarded,
            forwardFrom = r.forwardFrom,
            wireId = r.id.ifEmpty { r.fileId },
            // A stored own message reached the wire; the server keeps no receipts.
            status = if (r.outgoing) "sent" else "",
            // Already read on the device that stored it — unless it is newer than what we saw.
            readSent = false,
        )
    }

    internal fun replyOf(sender: String, ts: String, preview: String): ReplyRef? =
        if (sender.isEmpty() && ts.isEmpty() && preview.isEmpty()) null else ReplyRef(sender.lowercase(), ts, preview)

    // --- SSE ---

    private suspend fun sseLoop() {
        while (coroutineContext.isActive) {
            try {
                withContext(Dispatchers.IO) {
                    http.open("/sse").use { resp ->
                        sseUp.value = true
                        ChatPageLog.add("chat: event stream open")
                        // The replay ring cannot be trusted to hold everything; REST is.
                        scope.launch { resync() }
                        val reader = SseReader(resp.body.charStream().buffered())
                        while (isActive) {
                            val ev = reader.next() ?: break
                            val msg = runCatching { SkychatClient.json.decodeFromString(SseMessage.serializer(), ev.data) }
                                .getOrNull() ?: continue
                            withContext(Dispatchers.Main) { SseDispatch.handle(this@ChatRepository, msg) }
                        }
                    }
                }
            } catch (e: IOException) {
                Log.d(TAG, "sse ended: ${e.message}")
            }
            sseUp.value = false
            ChatPageLog.add("chat: event stream closed")
            delay(SSE_RETRY_MS)
        }
    }

    private suspend fun resync() {
        catchUp()
        syncGroups()
        syncPairs()
        openKey.value?.let { key ->
            if (isGroupKey(key)) refreshGroupHead(key)
        }
    }

    // --- periodic work ---

    private suspend fun presenceLoop() {
        while (coroutineContext.isActive) {
            val s = store.state.value
            val pks = (s.recipients + s.contacts).distinct().filter { isPk(it) }.take(64)
            if (pks.isNotEmpty() && presenceAvailable.value != false) {
                try {
                    presence.value = api.presence(pks).peers.mapKeys { it.key.lowercase() }
                    presenceAvailable.value = true
                } catch (e: SkychatHttpException) {
                    if (e.code == 404 || e.code == 503) {
                        presenceAvailable.value = false
                        presence.value = emptyMap()
                    }
                } catch (_: IOException) {
                }
            }
            delay(PRESENCE_MS)
        }
    }

    private suspend fun pairLoop() {
        while (coroutineContext.isActive) {
            delay(PAIR_MS)
            if (pair.value.available) pollPairEpochs()
        }
    }

    private suspend fun voiceLoop() {
        while (coroutineContext.isActive) {
            voiceAvailable.value = api.voiceAvailable()
            delay(VOICE_MS)
        }
    }

    /** The lease that keeps skychat from notifying about the thread on screen (osnotify.go). */
    private suspend fun focusLoop() {
        var sent: String? = null
        var sentAt = 0L
        while (coroutineContext.isActive) {
            val key = if (AppVisibility.isForeground.value) openKey.value.orEmpty() else ""
            val now = System.currentTimeMillis()
            if (key != sent || (key.isNotEmpty() && now - sentAt >= FOCUS_REFRESH_MS)) {
                if (runCatching { api.notifyFocus(key) }.isSuccess) {
                    sent = key
                    sentAt = now
                }
            }
            delay(FOCUS_MS)
        }
    }

    /** The hub card's badge reads this total back from skychat. */
    private suspend fun unreadLoop() {
        summaries.collectLatest {
            val total = totalUnread()
            if (total != postedUnread && runCatching { api.reportUnread(total) }.isSuccess) postedUnread = total
        }
    }

    // --- syncs ---

    internal suspend fun syncNames() {
        runCatching { api.contacts() }.onSuccess { map -> names.value = map.mapKeys { it.key.lowercase() } }
    }

    internal suspend fun syncPrivacy() {
        runCatching { api.privacy() }.onSuccess { applyPrivacy(it) }.onFailure { privacy.value = null }
    }

    internal fun applyPrivacy(p: PrivacyState) {
        privacy.value = p.copy(blocked = p.blocked.map { it.lowercase() }, requests = p.requests.map { it.lowercase() })
        p.requests.forEach { ensureRecipient(it) }
    }

    internal suspend fun syncGroups() {
        var attempt = 0
        while (coroutineContext.isActive) {
            try {
                val list = api.groups()
                groups.value = list
                groupsAvailable.value = true
                list.forEach { g -> if (threads[g.id] == null) threads[g.id] = MutableStateFlow(emptyList()) }
                return
            } catch (e: SkychatHttpException) {
                if (e.code == 404) {
                    groupsAvailable.value = false
                    return
                }
            } catch (_: IOException) {
            }
            if (attempt >= RETRIES) return
            delay(minOf(5000L, 1000L * (attempt + 1)))
            attempt++
        }
    }

    internal suspend fun syncProfile() {
        var attempt = 0
        while (coroutineContext.isActive) {
            try {
                profile.value = api.profile()
                profileAvailable.value = true
                return
            } catch (e: SkychatHttpException) {
                if (e.code == 404) {
                    profileAvailable.value = false
                    return
                }
            } catch (_: IOException) {
            }
            if (attempt >= RETRIES) return
            delay(minOf(5000L, 1000L * (attempt + 1)))
            attempt++
        }
    }

    internal suspend fun syncPairs() {
        val records = try {
            api.pairs()
        } catch (e: SkychatHttpException) {
            pair.value = PairUi()
            return
        } catch (_: IOException) {
            return
        }
        val live = records.filter { it.peerPk.isNotEmpty() && it.status != "revoked" }
        live.forEach { r ->
            ensureRecipient(r.peerPk)
            if (r.status == "active") contactFromPair(r.peerPk.lowercase(), r.establishedAt)
        }
        pair.update {
            it.copy(
                available = true,
                paired = live.map { r -> r.peerPk.lowercase() }.toSet(),
                records = records.associateBy { r -> r.peerPk.lowercase() },
            )
        }
        syncInvites()
    }

    internal suspend fun syncInvites() {
        val invites = runCatching { api.pairInvites() }.getOrDefault(emptyList())
        pair.update { it.copy(invites = invites.map { i -> i.peerPk.lowercase() }.distinct()) }
    }

    private suspend fun pollPairEpochs() {
        val records = runCatching { api.pairs() }.getOrNull() ?: return
        records.filter { it.status == "active" }.forEach { contactFromPair(it.peerPk.lowercase(), it.establishedAt) }
        pair.update { it.copy(records = records.associateBy { r -> r.peerPk.lowercase() }) }
    }

    /** An accepted pairing adds its peer to the address book once per pairing. */
    internal fun contactFromPair(pk: String, since: String) {
        val mark = "$pk $since"
        if (mark in store.state.value.pairContacts) return
        store.update { s -> s.copy(pairContacts = s.pairContacts + mark) }
        addContact(pk)
    }

    // --- names ---

    /** Nickname, else the short key — the label every list and header shows. */
    fun displayName(pk: String): String {
        val p = pk.lowercase()
        if (p == myPk.value && p.isNotEmpty()) return profile.value?.name?.takeIf { it.isNotEmpty() } ?: abbrevPk(p)
        return names.value[p]?.takeIf { it.isNotEmpty() } ?: abbrevPk(p)
    }

    fun groupInfo(gid: String): GroupInfo? = groups.value.firstOrNull { it.id == gid }

    fun isContact(pk: String): Boolean = pk.lowercase() in store.state.value.contacts

    fun addContact(pk: String, name: String? = null, avatar: ByteArray? = null): Boolean {
        val p = pk.lowercase()
        if (!isPk(p)) return false
        if (!name.isNullOrBlank() && names.value[p].isNullOrEmpty()) setName(p, name)
        if (avatar != null && !store.hasAvatar(p)) store.setAvatar(p, avatar)
        if (p !in store.state.value.contacts) store.update { s -> s.copy(contacts = s.contacts + p) }
        return true
    }

    fun setName(pk: String, name: String) {
        val p = pk.lowercase()
        val clean = name.trim().replace(Regex("\\s+"), " ").take(NAME_MAX)
        names.update { if (clean.isEmpty()) it - p else it + (p to clean) }
        scope.launch { runCatching { api.setContactName(p, clean) } }
    }

    companion object {
        private const val TAG = "ChatRepository"
        private const val PING_MS = 700L
        private const val READY_MS = 500L
        private const val READY_ATTEMPTS = 30
        private const val HTTP_OK = 200
        private const val HTTP_UNAUTHORIZED = 401
        private const val SSE_RETRY_MS = 2_000L
        private const val PRESENCE_MS = 60_000L
        private const val PAIR_MS = 15_000L
        private const val VOICE_MS = 15_000L
        private const val FOCUS_MS = 2_000L
        private const val FOCUS_REFRESH_MS = 5_000L
        private const val RETRIES = 15
        private const val CATCH_UP = 300
        private const val SAME_WINDOW_MS = 120_000L
        const val NAME_MAX = 40

        @Volatile
        private var instance: ChatRepository? = null

        fun get(context: Context): ChatRepository =
            instance ?: synchronized(this) {
                instance ?: ChatRepository(context.applicationContext).also { instance = it }
            }
    }
}

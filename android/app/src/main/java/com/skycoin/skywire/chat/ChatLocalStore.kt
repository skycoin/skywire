package com.skycoin.skywire.chat

import android.content.Context
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.File
import java.util.concurrent.Executors

/** Notification preferences; mutes are thread keys (peer pk, group id). */
@Serializable
data class NotifPrefs(
    val enabled: Boolean = true,
    val preview: Boolean = true,
    val muted: Set<String> = emptySet(),
)

/**
 * The chat state that lives on this phone only — what the web page kept in
 * localStorage. Owned by one visor key: a different identity starts empty.
 */
@Serializable
data class LocalChatState(
    val owner: String = "",
    /** DM peers shown in the list even without stored history. */
    val recipients: List<String> = emptyList(),
    /** Saved contacts (lower-case keys); names live on the server. */
    val contacts: List<String> = emptyList(),
    /** Thread key to the newest message time (ms) the user has seen. */
    val readUpTo: Map<String, Long> = emptyMap(),
    val tab: String = "all",
    /** `dm:<pk>` or `grp:<gid>`. */
    val pinned: String? = null,
    /** Group messages deleted for me, as `<gid>:<ts_nano>`. */
    val hiddenGroup: Set<String> = emptySet(),
    val mediaMode: String = "mic",
    val videoFacingFront: Boolean = true,
    val videoSquare: Boolean = false,
    val speed: Float = 1f,
    /** `<pk> <established_at>` pairings already turned into a contact. */
    val pairContacts: Set<String> = emptySet(),
    /** Set once the first catch-up ran, so a fresh install does not show the whole past as unread. */
    val initialized: Boolean = false,
    val legacyImported: Boolean = false,
)

/**
 * File-backed store under the app's files dir, written off the main thread.
 * DM threads and the Saved Messages notes are one JSON file each; contact
 * pictures are separate files so the state document stays small.
 */
class ChatLocalStore(context: Context) {

    private val root = File(context.filesDir, "skychat-native")
    private val json = Json { ignoreUnknownKeys = true; encodeDefaults = true }
    private val writer = Executors.newSingleThreadExecutor { Thread(it, "chat-store").apply { isDaemon = true } }

    private val mutable = MutableStateFlow(LocalChatState())
    val state: StateFlow<LocalChatState> = mutable.asStateFlow()

    private val notifFlow = MutableStateFlow(loadNotif())
    val notif: StateFlow<NotifPrefs> = notifFlow.asStateFlow()

    private val avatarVersion = MutableStateFlow(0)
    val avatarsChanged: StateFlow<Int> = avatarVersion.asStateFlow()

    @Volatile
    private var dir: File? = null

    val ready: Boolean get() = dir != null

    /** Load the store for [owner], the visor's key. Returns false when it was already open for it. */
    @Synchronized
    fun open(owner: String): Boolean {
        val pk = owner.lowercase()
        if (mutable.value.owner == pk && dir != null) return false
        val d = File(root, pk).apply { mkdirs() }
        File(d, THREADS).mkdirs()
        dir = d
        val loaded = runCatching {
            json.decodeFromString(LocalChatState.serializer(), File(d, STATE).readText())
        }.getOrNull() ?: LocalChatState()
        mutable.value = loaded.copy(owner = pk)
        return true
    }

    fun update(change: (LocalChatState) -> LocalChatState) {
        mutable.update(change)
        val d = dir ?: return
        val snapshot = mutable.value
        writer.execute { atomicWrite(File(d, STATE), json.encodeToString(LocalChatState.serializer(), snapshot)) }
    }

    fun updateNotif(change: (NotifPrefs) -> NotifPrefs) {
        notifFlow.update(change)
        val snapshot = notifFlow.value
        writer.execute {
            root.mkdirs()
            atomicWrite(File(root, NOTIF), json.encodeToString(NotifPrefs.serializer(), snapshot))
        }
    }

    private fun loadNotif(): NotifPrefs = runCatching {
        json.decodeFromString(NotifPrefs.serializer(), File(root, NOTIF).readText())
    }.getOrDefault(NotifPrefs())

    // --- threads ---

    private fun threadFile(key: String): File? =
        dir?.let { File(File(it, THREADS), key.lowercase().filter { c -> c.isLetterOrDigit() || c == '-' } + ".json") }

    fun threadKeys(): List<String> =
        dir?.let { File(it, THREADS).list()?.filter { n -> n.endsWith(".json") }?.map { n -> n.removeSuffix(".json") } }
            ?: emptyList()

    fun loadThread(key: String): List<ChatMessage> = runCatching {
        json.decodeFromString(ListSerializer(ChatMessage.serializer()), threadFile(key)!!.readText())
    }.getOrDefault(emptyList())

    fun saveThread(key: String, messages: List<ChatMessage>) {
        val f = threadFile(key) ?: return
        val snapshot = messages.takeLast(MAX_THREAD)
        writer.execute { atomicWrite(f, json.encodeToString(ListSerializer(ChatMessage.serializer()), snapshot)) }
    }

    fun deleteThread(key: String) {
        val f = threadFile(key) ?: return
        writer.execute { f.delete() }
    }

    // --- pictures ---

    private fun avatarFile(key: String): File? =
        dir?.let { File(File(it, "avatars").apply { mkdirs() }, key.lowercase().filter { c -> c.isLetterOrDigit() }) }

    /** A locally chosen picture for a peer, as image bytes. */
    fun avatar(key: String): ByteArray? = avatarFile(key)?.takeIf { it.isFile }?.readBytes()

    fun hasAvatar(key: String): Boolean = avatarFile(key)?.isFile == true

    fun setAvatar(key: String, bytes: ByteArray?) {
        val f = avatarFile(key) ?: return
        if (bytes == null) f.delete() else f.writeBytes(bytes)
        avatarVersion.update { it + 1 }
    }

    private fun atomicWrite(target: File, text: String) {
        runCatching {
            val tmp = File(target.parentFile, target.name + ".tmp")
            tmp.writeText(text)
            tmp.renameTo(target)
        }
    }

    private companion object {
        const val STATE = "state.json"
        const val NOTIF = "notifications.json"
        const val THREADS = "threads"
        const val MAX_THREAD = 1000
    }
}

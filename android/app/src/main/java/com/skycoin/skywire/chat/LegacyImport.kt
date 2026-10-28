package com.skycoin.skywire.chat

import android.annotation.SuppressLint
import android.webkit.HttpAuthHandler
import android.webkit.WebView
import android.webkit.WebViewClient
import com.skycoin.skywire.core.ChatPageLog
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkychatProfile
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.io.File
import java.util.UUID

/**
 * One-time move of what the old embedded page kept in the WebView's
 * localStorage: Saved Messages, DM caches with their ticks, contact
 * pictures, the address book list, pin, mutes and the small preferences.
 * The page's storage is only readable from its own origin, so a hidden
 * WebView loads a page there and hands the storage back as JSON.
 */
internal object LegacyImport {

    private const val TIMEOUT_MS = 20_000L

    fun maybeRun(repo: ChatRepository) {
        if (repo.store.state.value.legacyImported) return
        val webData = File(repo.app.applicationInfo.dataDir, "app_webview")
        if (!webData.isDirectory) {
            repo.store.update { it.copy(legacyImported = true) }
            return
        }
        repo.scope.launch(Dispatchers.Main) {
            val dump = readStorage(repo) ?: return@launch
            runCatching { apply(repo, dump) }.onFailure { ChatPageLog.add("chat: legacy import failed: ${it.message}") }
            repo.store.update { it.copy(legacyImported = true) }
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    private suspend fun readStorage(repo: ChatRepository): Map<String, String>? {
        val base = repo.http.baseUrl ?: return null
        val password = SecretStore(repo.app).skychatPassword()
        val done = CompletableDeferred<String?>()
        val view = WebView(repo.app)
        view.settings.javaScriptEnabled = true
        view.settings.domStorageEnabled = true
        view.webViewClient = object : WebViewClient() {
            override fun onReceivedHttpAuthRequest(v: WebView, handler: HttpAuthHandler, host: String, realm: String) {
                handler.proceed(SkychatProfile.USER, password)
            }

            override fun onPageFinished(v: WebView, url: String) {
                v.evaluateJavascript(DUMP) { done.complete(it) }
            }
        }
        view.loadUrl(base.trimEnd('/') + "/status")
        val raw = withTimeoutOrNull(TIMEOUT_MS) { done.await() }
        view.stopLoading()
        view.destroy()
        val text = raw?.takeIf { it != "null" } ?: return null
        return runCatching {
            val inner = (SkychatClient.json.parseToJsonElement(text) as JsonPrimitive).content
            val obj = SkychatClient.json.parseToJsonElement(inner) as JsonObject
            obj.mapValues { (it.value as JsonPrimitive).content }
        }.getOrNull()
    }

    private suspend fun apply(repo: ChatRepository, ls: Map<String, String>) {
        val me = repo.myPk.value
        val owner = ls.entries.firstOrNull { it.key.startsWith("skychat_owner:") }?.value?.lowercase()
        if (owner != null && owner != me) {
            ChatPageLog.add("chat: legacy storage belongs to another identity, not imported")
            return
        }
        val json = SkychatClient.json
        fun parse(key: String): JsonElement? = ls[key]?.let { runCatching { json.parseToJsonElement(it) }.getOrNull() }
        fun strings(key: String): List<String> = (parse(key) as? JsonArray)?.mapNotNull { (it as? JsonPrimitive)?.content } ?: emptyList()

        val recipients = strings("r").map { it.lowercase() }.filter { isPk(it) && it != me }
        val contacts = strings("skychat_contacts").map { it.lowercase() }.filter { isPk(it) }
        val seen = (parse("s") as? JsonObject)?.mapValues { it.value.jsonPrimitive.longOrNull ?: 0L } ?: emptyMap()
        val notif = parse("skychat_notif") as? JsonObject
        val speed = ls["skychat_speed"]?.toFloatOrNull()

        val legacyDir = File(repo.app.filesDir, "skychat-native/$me/legacy").apply { mkdirs() }
        val readUpTo = HashMap<String, Long>()
        for ((key, value) in ls) {
            if (!key.startsWith("c_")) continue
            val thread = key.removePrefix("c_").lowercase()
            if (thread != SAVED_KEY && (!isPk(thread) || thread == me)) continue
            val items = (runCatching { json.parseToJsonElement(value) }.getOrNull() as? JsonArray) ?: continue
            val msgs = items.mapNotNull { (it as? JsonObject)?.let { o -> message(o, legacyDir) } }
            if (msgs.isEmpty()) continue
            withContext(Dispatchers.Main) {
                repo.mutate(thread) { current ->
                    val merged = current.toMutableList()
                    for (m in msgs) {
                        val dup = merged.any { e ->
                            (m.dmWireId.isNotEmpty() && e.dmWireId == m.dmWireId) ||
                                (e.mine == m.mine && e.text == m.text && kotlin.math.abs(e.ts - m.ts) < 120_000)
                        }
                        if (!dup) merged += m
                    }
                    merged.sortedBy { it.ts }
                }
            }
            if (thread != SAVED_KEY) {
                val count = seen[thread]?.toInt() ?: msgs.size
                msgs.getOrNull(count - 1)?.let { readUpTo[thread] = it.ts }
            }
        }
        for ((key, value) in ls) {
            if (!key.startsWith("i_")) continue
            val pk = key.removePrefix("i_").lowercase()
            if (!value.startsWith("data:")) continue
            decodeDataUrl(value)?.let { if (!repo.store.hasAvatar(pk)) repo.store.setAvatar(pk, it) }
        }
        repo.store.update { s ->
            s.copy(
                recipients = (s.recipients + recipients).distinct(),
                contacts = (s.contacts + contacts).distinct(),
                readUpTo = readUpTo + s.readUpTo,
                tab = ls["skychat_tab"]?.takeIf { it in setOf("all", "dm", "groups", "channels", "calls") } ?: s.tab,
                pinned = ls["skychat_pinned"] ?: s.pinned,
                hiddenGroup = s.hiddenGroup + strings("skychat_hidden_group"),
                mediaMode = ls["skychat_media_mode"]?.takeIf { it == "video" || it == "mic" } ?: s.mediaMode,
                videoFacingFront = ls["skychat_video_facing"]?.let { it != "environment" } ?: s.videoFacingFront,
                videoSquare = ls["skychat_video_shape"]?.let { it == "square" } ?: s.videoSquare,
                speed = speed ?: s.speed,
                pairContacts = s.pairContacts + strings("skychat_pair_contacts"),
            )
        }
        recipients.forEach { repo.ensureRecipient(it) }
        if (notif != null) {
            repo.store.updateNotif { n ->
                n.copy(
                    enabled = notif["enabled"]?.jsonPrimitive?.booleanOrNull ?: n.enabled,
                    preview = notif["preview"]?.jsonPrimitive?.booleanOrNull ?: n.preview,
                    muted = n.muted + ((notif["muted"] as? JsonObject)?.keys ?: emptySet()),
                )
            }
        }
        (parse("skychat_names") as? JsonObject)?.let { names ->
            val map = names.mapNotNull { (k, v) -> (v as? JsonPrimitive)?.content?.trim()?.takeIf { it.isNotEmpty() }?.let { k.lowercase() to it.take(40) } }.toMap()
            if (map.isNotEmpty()) runCatching { repo.api.importContacts(map) }
            repo.syncNames()
        }
        repo.recomputeAll()
        ChatPageLog.add("chat: imported the old page's storage (${recipients.size} chats, ${contacts.size} contacts)")
    }

    private fun message(o: JsonObject, dir: File): ChatMessage? {
        if (o.containsKey("date") && !o.containsKey("from")) return null
        fun s(k: String, src: JsonObject = o) = (src[k] as? JsonPrimitive)?.takeIf { it.isString }?.content.orEmpty()
        val ts = parseTime(s("ts")) ?: return null
        val from = s("from").lowercase().ifEmpty { ChatMessage.ME }
        val f = o["file"] as? JsonObject
        val file = f?.let {
            var url = s("url", it)
            var local = ""
            if (url.startsWith("data:")) {
                decodeDataUrl(url)?.let { bytes ->
                    val out = File(dir, UUID.randomUUID().toString() + ".jpg")
                    out.writeBytes(bytes)
                    local = out.absolutePath
                }
                url = ""
            } else if (!url.startsWith("/files/")) {
                url = ""
            }
            ChatFile(
                id = s("id", it), name = s("name", it).ifEmpty { "file" },
                size = (it["size"] as? JsonPrimitive)?.longOrNull ?: 0,
                status = s("status", it), url = url, from = s("from", it).lowercase(), localPath = local,
            )
        }
        val r = o["reply"] as? JsonObject
        val reply = r?.let { ReplyRef(s("sender", it).lowercase(), s("ts", it), s("preview", it)) }
        val status = s("status").let { if (it == "queued" || it == "pending") "failed" else it }
        return ChatMessage(
            ts = ts,
            from = if (from == "me") ChatMessage.ME else from,
            text = s("text"),
            viaPair = (o["viaPair"] as? JsonPrimitive)?.booleanOrNull == true,
            file = file,
            reply = reply,
            forwarded = (o["forwarded"] as? JsonPrimitive)?.booleanOrNull == true,
            forwardFrom = s("forwardFrom"),
            wireId = s("wireId"),
            status = if (from == "me") status else "",
            readSent = (o["_readSent"] as? JsonPrimitive)?.booleanOrNull == true,
        )
    }

    private const val DUMP =
        "(function(){var o={};for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);o[k]=localStorage.getItem(k);}return JSON.stringify(o);})()"
}

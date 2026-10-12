package com.skycoin.skywire.ui.mail

import android.app.Application
import android.content.ContentValues
import android.net.Uri
import android.os.Build
import android.os.Environment
import android.provider.MediaStore
import android.provider.OpenableColumns
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.api.MailFolder
import com.skycoin.skywire.api.MailMessage
import com.skycoin.skywire.api.MailOutgoing
import com.skycoin.skywire.api.MailOutgoingAttachment
import com.skycoin.skywire.api.MailSendResult
import com.skycoin.skywire.api.MailStatus
import com.skycoin.skywire.api.MailSummary
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.core.SkymailAddress
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.File
import java.util.Base64

/** A file picked for the draft, read in full: a Skymail message is 1 MiB by default. */
class DraftAttachment(val name: String, val mime: String, val bytes: ByteArray)

data class MailDraft(
    val to: String = "",
    val cc: String = "",
    val subject: String = "",
    val body: String = "",
    val inReplyTo: String = "",
    val attachments: List<DraftAttachment> = emptyList(),
)

data class OpenMail(
    val folder: String,
    val summary: MailSummary,
    val message: MailMessage? = null,
    val error: String? = null,
)

/** Everything the Skymail screen renders. */
data class MailUiState(
    val coreState: CoreState = CoreState.Stopped,
    val apiUp: Boolean = false,
    val status: MailStatus? = null,
    val folder: String = MailFolder.INBOX,
    val inbox: List<MailSummary>? = null,
    val sent: List<MailSummary>? = null,
    val error: String? = null,
    val open: OpenMail? = null,
    val draft: MailDraft? = null,
    val sending: Boolean = false,
    /** The last send: which recipients got it, and why the others did not. */
    val lastSend: MailSendResult? = null,
    val settingsOpen: Boolean = false,
    /** A settings change or a delete is in flight. */
    val busy: Boolean = false,
    /** A one-off line for the snackbar, then cleared. */
    val notice: String? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp
    val messages: List<MailSummary>? get() = if (folder == MailFolder.SENT) sent else inbox

    /** The address to hand out: dmsg needs no route, which a phone rarely has. */
    val myAddress: String? get() = status?.addressDmsg?.takeIf { it.isNotEmpty() } ?: status?.address
}

/**
 * Drives the Skymail screen over the visor's mail routes. The mailbox lives in
 * the core, so everything here waits for it, and the lists are polled while the
 * screen is open, as the desktop dashboard's mail tab does.
 */
class MailViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val mutable = MutableStateFlow(MailUiState())
    val uiState: StateFlow<MailUiState> = mutable.asStateFlow()

    private var pollJob: Job? = null

    init {
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                mutable.update { it.copy(coreState = core, apiUp = false) }
                if (core is CoreState.Running) {
                    while (!api.ping()) delay(PING_INTERVAL_MS)
                    mutable.update { it.copy(apiUp = true) }
                    while (isActive) {
                        refreshNow()
                        openPendingLink()
                        delay(POLL_INTERVAL_MS)
                    }
                }
            }
        }
    }

    fun selectFolder(folder: String) {
        mutable.update { it.copy(folder = folder) }
        refresh()
    }

    fun refresh() {
        pollJob?.cancel()
        pollJob = viewModelScope.launch { refreshNow() }
    }

    private suspend fun refreshNow() {
        if (!mutable.value.coreReady) return
        runCatching { api.mailStatus() }
            .onSuccess { st -> mutable.update { it.copy(status = st, error = null) } }
            .onFailure { e -> mutable.update { it.copy(error = e.message) } }
        if (mutable.value.status?.running != true) return
        val folder = mutable.value.folder
        runCatching { api.mailList(folder) }
            .onSuccess { list ->
                mutable.update { if (folder == MailFolder.SENT) it.copy(sent = list) else it.copy(inbox = list) }
            }
            .onFailure { e -> mutable.update { it.copy(error = e.message) } }
    }

    // --- reading ---

    fun open(folder: String, summary: MailSummary) {
        mutable.update { it.copy(open = OpenMail(folder, summary)) }
        viewModelScope.launch {
            runCatching { api.mailRead(folder, summary.id) }
                .onSuccess { m ->
                    mutable.update { s ->
                        s.copy(
                            open = s.open?.takeIf { it.summary.id == summary.id }?.copy(message = m),
                            inbox = s.inbox?.map { if (it.id == summary.id) it.copy(seen = true) else it },
                        )
                    }
                    refreshNow()
                }
                .onFailure { e ->
                    mutable.update { s -> s.copy(open = s.open?.copy(error = e.message)) }
                }
        }
    }

    fun closeMessage() {
        mutable.update { it.copy(open = null) }
    }

    fun delete() {
        val open = mutable.value.open ?: return
        mutable.update { it.copy(busy = true) }
        viewModelScope.launch {
            runCatching { api.mailDelete(open.folder, open.summary.id) }
                .onSuccess {
                    mutable.update { s ->
                        s.copy(
                            busy = false,
                            open = null,
                            inbox = s.inbox?.filterNot { it.id == open.summary.id },
                            sent = s.sent?.filterNot { it.id == open.summary.id },
                            notice = string(R.string.mail_deleted),
                        )
                    }
                    refreshNow()
                }
                .onFailure { e -> mutable.update { it.copy(busy = false, notice = e.message) } }
        }
    }

    /** Saves attachment [n] of the open message to Downloads. */
    fun saveAttachment(n: Int) {
        val open = mutable.value.open ?: return
        val info = open.message?.attachments?.getOrNull(n) ?: return
        viewModelScope.launch {
            runCatching {
                val bytes = api.mailAttachment(open.folder, open.summary.id, n)
                saveToDownloads(info.name.ifEmpty { "attachment" }, info.contentType, bytes)
            }.onSuccess { name ->
                mutable.update { it.copy(notice = string(R.string.mail_attachment_saved, name)) }
            }.onFailure { e ->
                mutable.update { it.copy(notice = string(R.string.mail_attachment_failed, e.message.orEmpty())) }
            }
        }
    }

    // --- writing ---

    fun compose(to: String = "") {
        mutable.update { it.copy(draft = MailDraft(to = to), lastSend = null) }
    }

    /** A reply to the open message: to its sender, quoting it. */
    fun reply() {
        val open = mutable.value.open ?: return
        val m = open.message ?: return
        val to = if (open.folder == MailFolder.SENT) m.to else m.from
        val subject = if (m.subject.startsWith("Re:", ignoreCase = true)) m.subject else "Re: ${m.subject}"
        val quoted = m.text.trimEnd().lines().joinToString("\n") { "> $it" }
        mutable.update {
            it.copy(
                open = null,
                lastSend = null,
                draft = MailDraft(
                    to = to,
                    subject = subject,
                    body = "\n\n" + string(R.string.mail_reply_header, m.date, m.from) + "\n" + quoted,
                    inReplyTo = m.messageId,
                ),
            )
        }
    }

    fun updateDraft(change: (MailDraft) -> MailDraft) {
        mutable.update { s -> s.copy(draft = s.draft?.let(change)) }
    }

    fun discardDraft() {
        mutable.update { it.copy(draft = null, lastSend = null) }
    }

    fun addAttachments(uris: List<Uri>) {
        if (uris.isEmpty()) return
        viewModelScope.launch {
            val picked = withContext(Dispatchers.IO) { uris.mapNotNull(::readAttachment) }
            val limit = mutable.value.status?.limits?.maxMessageSize?.takeIf { it > 0 } ?: DEFAULT_MAX_MESSAGE
            mutable.update { s ->
                val draft = s.draft ?: return@update s
                val all = draft.attachments + picked
                // Base64 grows a file by a third, and the text and headers ride along.
                if (all.sumOf { it.bytes.size.toLong() } * 4 / 3 + ENVELOPE_BYTES > limit) {
                    s.copy(notice = string(R.string.mail_attachments_too_big, formatBytes(limit)))
                } else {
                    s.copy(draft = draft.copy(attachments = all))
                }
            }
        }
    }

    fun removeAttachment(index: Int) {
        updateDraft { d -> d.copy(attachments = d.attachments.filterIndexed { i, _ -> i != index }) }
    }

    fun send() {
        val draft = mutable.value.draft ?: return
        val to = parseRecipients(draft.to)
        val cc = parseRecipients(draft.cc)
        val bad = (to + cc).filter { it.second == null }.map { it.first }
        when {
            bad.isNotEmpty() -> {
                mutable.update { it.copy(notice = string(R.string.mail_bad_address, bad.joinToString(", "))) }
                return
            }
            to.isEmpty() -> {
                mutable.update { it.copy(notice = string(R.string.mail_no_recipient)) }
                return
            }
        }
        val msg = MailOutgoing(
            to = to.mapNotNull { it.second },
            cc = cc.mapNotNull { it.second },
            subject = draft.subject.trim(),
            body = draft.body,
            inReplyTo = draft.inReplyTo,
            attachments = draft.attachments.map {
                MailOutgoingAttachment(it.name, it.mime, Base64.getEncoder().encodeToString(it.bytes))
            },
        )
        mutable.update { it.copy(sending = true, lastSend = null) }
        viewModelScope.launch {
            runCatching { api.mailSend(msg) }
                .onSuccess { result ->
                    mutable.update { s ->
                        if (result.delivered > 0 && result.recipients.all { it.error.isEmpty() }) {
                            s.copy(sending = false, draft = null, lastSend = null, notice = string(R.string.mail_sent))
                        } else {
                            s.copy(sending = false, lastSend = result)
                        }
                    }
                    refreshNow()
                }
                .onFailure { e ->
                    mutable.update { it.copy(sending = false, notice = string(R.string.mail_send_failed, e.message.orEmpty())) }
                }
        }
    }

    // --- settings ---

    fun openSettings() = mutable.update { it.copy(settingsOpen = true) }

    fun closeSettings() = mutable.update { it.copy(settingsOpen = false) }

    fun setEnabled(enabled: Boolean) = settingsCall { api.mailSetEnabled(enabled) }

    /** Lets [input] (a key or an address) deliver; false when it names no key. */
    fun allowSender(input: String): Boolean {
        val pk = input.trim().lowercase().let { text ->
            SkymailAddress.pkOfAddress(text) ?: text.removePrefix("skychat://").trimEnd('/')
                .takeIf { SkymailAddress.label(it) != null }
        } ?: return false
        val list = mutable.value.status?.whitelist.orEmpty()
        if (pk !in list) settingsCall { api.mailSetWhitelist(list + pk) }
        return true
    }

    fun removeSender(pk: String) {
        val list = mutable.value.status?.whitelist.orEmpty()
        settingsCall { api.mailSetWhitelist(list - pk) }
    }

    private fun settingsCall(call: suspend () -> Unit) {
        mutable.update { it.copy(busy = true) }
        viewModelScope.launch {
            runCatching { call() }
                .onFailure { e -> mutable.update { it.copy(notice = e.message) } }
            mutable.update { it.copy(busy = false) }
            refreshNow()
        }
    }

    fun noticeShown() = mutable.update { it.copy(notice = null) }

    // --- a tapped notification ---

    /** Opens the message a notification named, once the inbox has it. */
    private fun openPendingLink() {
        val link = DeepLinks.pendingMail.value ?: return
        val summary = mutable.value.inbox?.firstOrNull { it.id == link.id }
        if (summary != null) {
            mutable.update { it.copy(folder = MailFolder.INBOX, draft = null, settingsOpen = false) }
            open(MailFolder.INBOX, summary)
        }
        if (summary != null || mutable.value.inbox != null) DeepLinks.mailLinkHandled(link)
    }

    fun onPendingLink() {
        viewModelScope.launch {
            mutable.update { it.copy(folder = MailFolder.INBOX) }
            refreshNow()
            openPendingLink()
        }
    }

    // --- helpers ---

    private fun parseRecipients(text: String): List<Pair<String, String?>> =
        text.split(',', ';', ' ', '\n').map { it.trim() }.filter { it.isNotEmpty() }
            .map { it to SkymailAddress.recipient(it) }

    private fun readAttachment(uri: Uri): DraftAttachment? = runCatching {
        val resolver = getApplication<Application>().contentResolver
        val name = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)?.use { c ->
            if (c.moveToFirst()) c.getString(0) else null
        } ?: uri.lastPathSegment ?: "attachment"
        val bytes = resolver.openInputStream(uri)?.use { it.readBytes() } ?: return null
        DraftAttachment(name, resolver.getType(uri) ?: "application/octet-stream", bytes)
    }.getOrNull()

    /** Public Downloads from Android 10 with no permission; the app's own Downloads before it. */
    private suspend fun saveToDownloads(name: String, mime: String, bytes: ByteArray): String =
        withContext(Dispatchers.IO) {
            val app = getApplication<Application>()
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
                val values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, name)
                    put(MediaStore.Downloads.MIME_TYPE, mime.ifEmpty { "application/octet-stream" })
                }
                val uri = app.contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                    ?: error("no Downloads collection")
                app.contentResolver.openOutputStream(uri)?.use { it.write(bytes) } ?: error("cannot write $name")
            } else {
                val dir = app.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS) ?: error("no storage")
                File(dir, name).writeBytes(bytes)
            }
            name
        }

    private fun string(id: Int, vararg args: Any): String = getApplication<Application>().getString(id, *args)

    private companion object {
        const val PING_INTERVAL_MS = 1_000L
        const val POLL_INTERVAL_MS = 15_000L
        const val DEFAULT_MAX_MESSAGE = 1L shl 20
        const val ENVELOPE_BYTES = 16 * 1024L
    }
}

/** A byte count as a person reads it. */
fun formatBytes(n: Long): String = when {
    n >= 1L shl 20 -> "%.1f MB".format(n / (1024.0 * 1024.0))
    n >= 1L shl 10 -> "%.0f KB".format(n / 1024.0)
    else -> "$n B"
}

package com.skycoin.skywire.core

import android.content.Intent
import android.net.Uri
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import java.util.concurrent.atomic.AtomicLong

/**
 * Links other apps open Skywire for.
 *
 * Process-scoped rather than held by a screen, because the two ends never
 * line up: the link arrives at the Activity, and the screen that acts on it
 * may not exist yet (a cold start), may be mid-way through bringing the core
 * up, or may be showing a page that has not finished loading. Parking the
 * link here lets each side move at its own pace — it is offered once and
 * stays pending until something has actually acted on it.
 *
 * Only `skychat:` is claimed (see the manifest). `skycoin:` in particular is
 * NOT ours to take: the Skycoin wallet app already answers it on the same
 * device, and registering a competing filter would put a disambiguation
 * chooser in front of every one of its links.
 */
object DeepLinks {

    /** `skychat://<pk>`, `skychat://<pk>/<group-id>`, and `skychat:invite:<…>`. */
    const val SKYCHAT_SCHEME = "skychat"

    /**
     * A skychat address waiting to be shown, with the id that makes two
     * identical links two events. Without it, opening the same link twice
     * would set a StateFlow to a value it already holds, which emits nothing
     * and leaves the second tap doing nothing at all.
     */
    data class ChatLink(val address: String, val id: Long, val thread: Boolean = false)

    /** What a SkyChat notification opens the app with; [EXTRA_THREAD] is its conversation. */
    const val ACTION_OPEN_THREAD = "com.skycoin.skywire.OPEN_CHAT_THREAD"
    const val EXTRA_THREAD = "thread"

    /** What a Skymail notification opens the app with; [EXTRA_MAIL_ID] is the message. */
    const val ACTION_OPEN_MAIL = "com.skycoin.skywire.OPEN_MAIL"
    const val EXTRA_MAIL_ID = "mail_id"

    /** An inbox message a notification asked to show. */
    data class MailLink(val id: String, val seq: Long)

    private val mail = MutableStateFlow<MailLink?>(null)

    val pendingMail: StateFlow<MailLink?> = mail.asStateFlow()

    fun mailLinkHandled(link: MailLink) {
        mail.compareAndSet(link, null)
    }

    private val seq = AtomicLong()

    private val chat = MutableStateFlow<ChatLink?>(null)

    /** The skychat link nothing has acted on yet, if any. */
    val pendingChatLink: StateFlow<ChatLink?> = chat.asStateFlow()

    /** True when [intent] carried a link this app claims. */
    fun offer(intent: Intent?): Boolean {
        if (intent?.action == ACTION_OPEN_MAIL) {
            val id = intent.getStringExtra(EXTRA_MAIL_ID)?.trim().orEmpty()
            if (id.isEmpty()) return false
            mail.value = MailLink(id, seq.incrementAndGet())
            return true
        }
        if (intent?.action == ACTION_OPEN_THREAD) {
            val thread = intent.getStringExtra(EXTRA_THREAD)?.trim().orEmpty()
            if (thread.isEmpty()) return false
            chat.value = ChatLink(thread, seq.incrementAndGet(), thread = true)
            return true
        }
        if (intent?.action != Intent.ACTION_VIEW) return false
        return offer(intent.data)
    }

    fun offer(uri: Uri?): Boolean {
        if (uri == null) return false
        if (!uri.scheme.equals(SKYCHAT_SCHEME, ignoreCase = true)) return false
        // The original text, not a rebuilt URI: an invite is the opaque form
        // `skychat:invite:<base64url>`, which does not survive a round trip
        // through the authority/path accessors, and the chat page's resolver
        // takes every form it can be handed as written.
        val address = uri.toString().trim()
        if (address.isEmpty()) return false
        chat.value = ChatLink(address, seq.incrementAndGet())
        return true
    }

    /** Drop [link] once it has been shown; a newer one arriving meanwhile stays. */
    fun chatLinkHandled(link: ChatLink) {
        chat.compareAndSet(link, null)
    }
}

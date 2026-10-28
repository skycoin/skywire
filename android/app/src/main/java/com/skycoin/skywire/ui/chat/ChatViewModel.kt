package com.skycoin.skywire.ui.chat

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import com.skycoin.skywire.chat.ChatRepository
import com.skycoin.skywire.chat.ReplyRef
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update

/** The screens inside the Chat tab; back pops one. */
sealed interface ChatPage {
    data object Home : ChatPage
    data class Thread(val key: String) : ChatPage
    data class Contacts(val pick: Boolean) : ChatPage
    data class ContactSettings(val pk: String) : ChatPage
    data class NewContact(val prefill: String) : ChatPage
    data object MyProfile : ChatPage
    data object Settings : ChatPage
    data class GroupSettings(val gid: String) : ChatPage
    data class AddAddress(val prefill: String, val fromLink: Boolean) : ChatPage
    data class NewGroup(val channel: Boolean) : ChatPage
    data object Search : ChatPage
    data class Forward(val fromKey: String, val msgKey: String) : ChatPage
    data class Image(val source: String, val name: String) : ChatPage
    data class Video(val source: String, val name: String, val startMs: Long) : ChatPage
}

/**
 * The Chat tab's own navigation and per-thread drafts. Everything about the
 * conversations themselves lives in [ChatRepository], which outlives this.
 */
class ChatViewModel(app: Application) : AndroidViewModel(app) {

    val repo = ChatRepository.get(app)

    private val stack = MutableStateFlow<List<ChatPage>>(listOf(ChatPage.Home))
    val pages: StateFlow<List<ChatPage>> = stack.asStateFlow()

    /** Unsent text per thread, so leaving a chat does not lose it. */
    val drafts = HashMap<String, String>()

    private val replyMap = MutableStateFlow<Map<String, ReplyRef>>(emptyMap())
    val replies: StateFlow<Map<String, ReplyRef>> = replyMap.asStateFlow()

    /** A message to scroll to once its thread shows it: thread key to message time. */
    val scrollTarget = MutableStateFlow<Pair<String, Long>?>(null)

    init {
        repo.start(startApp = true)
    }

    val top: ChatPage get() = stack.value.last()

    fun push(page: ChatPage) {
        stack.update { it + page }
        syncOpen()
    }

    /** Back: false when there was nothing left to pop. */
    fun pop(): Boolean {
        if (stack.value.size <= 1) return false
        stack.update { it.dropLast(1) }
        syncOpen()
        return true
    }

    /** Open a conversation from anywhere: it sits directly over the list. */
    fun openThread(key: String) {
        stack.value = listOf(ChatPage.Home, ChatPage.Thread(key))
        syncOpen()
    }

    fun home() {
        stack.value = listOf(ChatPage.Home)
        syncOpen()
    }

    /** Leave the current page and show [page] in its place. */
    fun replace(page: ChatPage) {
        stack.update { it.dropLast(1) + page }
        syncOpen()
    }

    /** Tell the repository which thread is actually on screen (focus lease, unread, link). */
    fun syncOpen(visible: Boolean = true) {
        val t = (stack.value.last() as? ChatPage.Thread)?.key
        repo.setOpen(if (visible) t else null)
    }

    fun setReply(key: String, reply: ReplyRef?) {
        replyMap.update { if (reply == null) it - key else it + (key to reply) }
    }

    override fun onCleared() {
        repo.setOpen(null)
    }
}

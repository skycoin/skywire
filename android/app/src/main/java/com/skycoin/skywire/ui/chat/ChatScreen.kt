package com.skycoin.skywire.ui.chat

import android.view.WindowManager
import androidx.activity.compose.BackHandler
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.runtime.staticCompositionLocalOf
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.ChatConn
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.navigation.HoldTabSwipe
import com.skycoin.skywire.ui.navigation.TabSubPage

/** Snackbars any chat page can raise. */
val LocalChatSnackbar = staticCompositionLocalOf<SnackbarHostState> { error("no chat snackbar") }

/**
 * Chat tab: SkyChat drawn natively over skychat's HTTP API. The list is the
 * screen until a conversation opens over it; every other page (contacts,
 * settings, a group's admin panel…) stacks on top and back pops it.
 */
@Composable
fun ChatScreen(onBack: () -> Unit, viewModel: ChatViewModel = viewModel()) {
    val repo = viewModel.repo
    val conn by repo.conn.collectAsState()
    val pages by viewModel.pages.collectAsState()
    val page = pages.last()
    val snackbar = remember { SnackbarHostState() }
    val context = LocalContext.current

    LaunchedEffect(Unit) {
        repo.notes.collect { n ->
            val args = n.args.map { a -> if (a is Int) a.toString() else a }.toTypedArray()
            snackbar.showSnackbar(context.getString(n.res, *args))
        }
    }

    // Only a thread actually in front of the user holds the focus lease.
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_RESUME -> viewModel.syncOpen(true)
                Lifecycle.Event.ON_PAUSE -> viewModel.syncOpen(false)
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        viewModel.syncOpen(true)
        onDispose {
            lifecycle.removeObserver(observer)
            viewModel.syncOpen(false)
        }
    }

    val chatLink by DeepLinks.pendingChatLink.collectAsState()
    LaunchedEffect(chatLink, conn.ready) {
        val link = chatLink ?: return@LaunchedEffect
        if (!conn.ready) return@LaunchedEffect
        if (link.thread) {
            val tag = link.address.removePrefix("join:").lowercase()
            val key = repo.groups.value.firstOrNull { it.id.equals(link.address.removePrefix("join:"), true) }?.id ?: tag
            viewModel.openThread(key)
        } else {
            viewModel.home()
            viewModel.push(ChatPage.AddAddress(link.address, fromLink = true))
        }
        DeepLinks.chatLinkHandled(link)
    }

    // The composer rides the keyboard through imePadding; a panned window
    // would move it twice and push the header off screen.
    DisposableEffect(context) {
        val window = context.activity()?.window
        val previous = window?.attributes?.softInputMode
        window?.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE)
        onDispose { previous?.let { window.setSoftInputMode(it) } }
    }

    BackHandler(enabled = pages.size > 1) { viewModel.pop() }
    // Inside a conversation, horizontal drags belong to the chat (slide to cancel, seek bars).
    HoldTabSwipe(pages.size > 1)
    TabSubPage(pages.size > 1)

    CompositionLocalProvider(LocalChatSnackbar provides snackbar) {
        if (!conn.ready) {
            Scaffold(
                topBar = {
                    SkyTopBar(
                        title = stringResource(R.string.app_skychat),
                        onBack = onBack,
                        help = HelpTopic(R.string.help_chat_title, R.string.help_chat_body),
                    )
                },
                snackbarHost = { SnackbarHost(snackbar) },
            ) { padding ->
                Box(Modifier.fillMaxSize().padding(padding)) {
                    ChatStatus(conn, onRetry = repo::retry)
                }
            }
            return@CompositionLocalProvider
        }
        val back: () -> Unit = { if (!viewModel.pop()) onBack() }
        Box(Modifier.fillMaxSize()) {
            when (page) {
                ChatPage.Home -> ChatHome(viewModel, onBack = onBack)
                is ChatPage.Thread -> ThreadScreen(viewModel, page.key, onBack = back)
                is ChatPage.Contacts -> ContactsScreen(viewModel, page.pick, onBack = back)
                is ChatPage.ContactSettings -> ContactSettingsScreen(viewModel, page.pk, onBack = back)
                is ChatPage.NewContact -> NewContactScreen(viewModel, page.prefill, onBack = back)
                ChatPage.MyProfile -> MyProfileScreen(viewModel, onBack = back)
                ChatPage.Settings -> ChatSettingsScreen(viewModel, onBack = back)
                is ChatPage.GroupSettings -> GroupSettingsScreen(viewModel, page.gid, onBack = back)
                is ChatPage.AddAddress -> AddAddressScreen(viewModel, page.prefill, page.fromLink, onBack = back)
                is ChatPage.NewGroup -> NewGroupScreen(viewModel, page.channel, onBack = back)
                ChatPage.Search -> SearchScreen(viewModel, onBack = back)
                is ChatPage.Forward -> ForwardScreen(viewModel, page.fromKey, page.msgKey, onBack = back)
                is ChatPage.Image -> ImageViewer(viewModel, page.source, page.name, onBack = back)
                is ChatPage.Video -> VideoViewer(viewModel, page.source, page.name, page.startMs, onBack = back)
            }
            SnackbarHost(snackbar, Modifier.align(Alignment.BottomCenter).padding(bottom = 80.dp))
        }
    }
}

/** What the tab shows until skychat answers. */
@Composable
private fun ChatStatus(conn: ChatConn, onRetry: () -> Unit) {
    val core = conn.core
    val error = conn.error
    val message = when {
        error != null -> error
        core is CoreState.Failed -> core.message
        core is CoreState.Running -> if (conn.apiUp) stringResource(R.string.chat_starting) else stringResource(R.string.chat_core_starting)
        core is CoreState.Starting || core is CoreState.Restarting -> stringResource(R.string.chat_core_starting)
        else -> stringResource(R.string.chat_core_offline)
    }
    val connecting = error == null && (conn.starting || core is CoreState.Starting || core is CoreState.Restarting || core is CoreState.Running)
    Column(
        modifier = Modifier.fillMaxSize().padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        if (connecting) {
            CircularProgressIndicator()
            Spacer(Modifier.height(20.dp))
        }
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        if (error != null && core is CoreState.Running) {
            Spacer(Modifier.height(8.dp))
            FilledTonalButton(onClick = onRetry) { Text(stringResource(R.string.socks_retry)) }
        }
    }
}

private tailrec fun android.content.Context.activity(): android.app.Activity? = when (this) {
    is android.app.Activity -> this
    is android.content.ContextWrapper -> baseContext.activity()
    else -> null
}

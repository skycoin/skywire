package com.skycoin.skywire.ui.chat

import android.content.ActivityNotFoundException
import android.net.Uri
import android.webkit.PermissionRequest
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebView
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.MoreVert
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.core.SkychatProfile
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.logs.LogSources

/**
 * Chat tab — skychat's own web UI, embedded.
 *
 * Deliberately the *same* UI the desktop serves rather than a native rewrite:
 * one chat codebase, and its phone layout is a breakpoint inside it (the
 * conversation list is the screen until a chat is opened, which slides in
 * over it). Everything the page cannot do for itself — the password gate,
 * the microphone, the file picker, saving an attachment, and the phone's own
 * back gesture — is what [ChatWebView] adds around it.
 */
@Composable
fun ChatScreen(onOpenLogs: (String) -> Unit, viewModel: ChatViewModel = viewModel()) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current

    var webView by remember { mutableStateOf<WebView?>(null) }
    var loadedUrl by remember { mutableStateOf<String?>(null) }
    var canGoBack by remember { mutableStateOf(false) }
    var menuOpen by remember { mutableStateOf(false) }
    // The page is loaded and can be driven. Distinct from "the WebView
    // exists": a link handed to a page that is still loading would be
    // evaluated against the document it is replacing.
    var pageReady by remember { mutableStateOf(false) }
    // The page's own error, kept apart from the view model's: one is "the
    // surface never came up", the other "it came up and then broke".
    var pageError by remember { mutableStateOf<String?>(null) }

    // Read by the WebView clients, which are built once — these keep them
    // looking at the current values instead of the ones at creation time.
    val url = rememberUpdatedState(state.url)
    val password = rememberUpdatedState(state.password)

    // --- uploads: the page's <input type="file"> ---
    var pendingFiles by remember { mutableStateOf<ValueCallback<Array<Uri>>?>(null) }
    val filePicker = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { result ->
        // Always answer the callback, cancel included: an unanswered file
        // chooser leaves the page's input permanently unusable.
        pendingFiles?.onReceiveValue(
            WebChromeClient.FileChooserParams.parseResult(result.resultCode, result.data),
        )
        pendingFiles = null
    }

    // --- getUserMedia: voice messages now, video messages next ---
    var pendingMedia by remember { mutableStateOf<PermissionRequest?>(null) }
    val mediaPermissions = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestMultiplePermissions(),
    ) { grants ->
        val request = pendingMedia
        pendingMedia = null
        request?.grantOrDeny { resource ->
            ChatWebView.androidPermission(resource)?.let { grants[it] == true } ?: false
        }
    }

    BackHandler(enabled = canGoBack) { webView?.goBack() }

    // A skychat:// link another app opened us for. It waits here rather than
    // at the Activity for as long as it has to: the core may still be
    // connecting, and skychat only answers once it is. Cleared once the page
    // has it — including when the page declines it, since retrying a link the
    // page has already refused would only loop.
    val chatLink by DeepLinks.pendingChatLink.collectAsState()
    LaunchedEffect(chatLink, pageReady) {
        val link = chatLink ?: return@LaunchedEffect
        val view = webView
        if (!pageReady || view == null) return@LaunchedEffect
        ChatWebView.openAddress(view, link.address)
        DeepLinks.chatLinkHandled(link)
    }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_skychat),
                actions = {
                    IconButton(onClick = { menuOpen = true }) {
                        Icon(
                            Icons.Default.MoreVert,
                            contentDescription = stringResource(R.string.chat_menu),
                        )
                    }
                    DropdownMenu(expanded = menuOpen, onDismissRequest = { menuOpen = false }) {
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.chat_reload)) },
                            onClick = {
                                menuOpen = false
                                pageError = null
                                pageReady = false
                                webView?.reload()
                            },
                        )
                        DropdownMenuItem(
                            text = { Text(stringResource(R.string.logs_title)) },
                            onClick = {
                                menuOpen = false
                                onOpenLogs(LogSources.app(SkychatProfile.APP))
                            },
                        )
                    }
                },
            )
        },
        // The system bars belong to the app scaffold this tab sits in, and
        // are consumed there — claiming them again would offset the page and
        // make the keyboard inset below double-count the navigation bar.
        contentWindowInsets = WindowInsets(0),
    ) { padding ->
        Box(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                // The composer sits at the bottom of the page, and the page
                // is a fixed-height document — without this the keyboard
                // covers the field it belongs to.
                .imePadding(),
        ) {
            val ready = state.url != null && state.password != null && pageError == null
            if (ready) {
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { ctx ->
                        ChatWebView.create(ctx).also { view ->
                            view.webViewClient = ChatWebView.client(
                                baseUrl = { url.value },
                                password = { password.value },
                                onHistoryChanged = { canGoBack = it },
                                onPageReady = { pageReady = true },
                                onError = { pageError = it },
                            )
                            view.webChromeClient = ChatWebView.chromeClient(
                                onPermissionRequest = { request ->
                                    val needed = request.resources
                                        .mapNotNull(ChatWebView::androidPermission)
                                        .filterNot { ChatWebView.hasPermission(ctx, it) }
                                    if (needed.isEmpty()) {
                                        request.grantOrDeny { resource ->
                                            ChatWebView.androidPermission(resource) != null
                                        }
                                    } else {
                                        pendingMedia = request
                                        mediaPermissions.launch(needed.toTypedArray())
                                    }
                                },
                                onFileChooser = { callback, params ->
                                    pendingFiles?.onReceiveValue(null)
                                    pendingFiles = callback
                                    try {
                                        filePicker.launch(params.createIntent())
                                        true
                                    } catch (e: ActivityNotFoundException) {
                                        pendingFiles = null
                                        false
                                    }
                                },
                            )
                            view.setDownloadListener { link, _, disposition, mime, _ ->
                                ChatWebView.download(ctx, link, disposition, mime, password.value)
                            }
                            webView = view
                        }
                    },
                    update = { view ->
                        val target = state.url
                        if (target != null && target != loadedUrl) {
                            loadedUrl = target
                            pageReady = false
                            view.loadUrl(target)
                        }
                    },
                    onRelease = { view ->
                        // A live SSE stream survives the composable otherwise.
                        ChatWebView.release(view)
                        webView = null
                        loadedUrl = null
                        canGoBack = false
                        pageReady = false
                    },
                )
            } else {
                ChatStatus(
                    state = state,
                    pageError = pageError,
                    onRetry = {
                        pageError = null
                        loadedUrl = null
                        viewModel.retry()
                    },
                )
            }
        }
    }
}

/** Grant exactly the resources [allowed] accepts; deny the request outright otherwise. */
private inline fun PermissionRequest.grantOrDeny(allowed: (String) -> Boolean) {
    val granted = resources.filter(allowed).toTypedArray()
    if (granted.isEmpty()) deny() else grant(granted)
}

/**
 * What the tab shows while there is no chat surface to show: the core has to
 * be running before skychat can be, and skychat has to answer before the
 * WebView is worth creating.
 */
@Composable
private fun ChatStatus(
    state: ChatUiState,
    pageError: String?,
    onRetry: () -> Unit,
) {
    val error = pageError ?: state.error
    val core = state.coreState
    val message = when {
        error != null -> error
        core is CoreState.Failed -> core.message
        core is CoreState.Running ->
            if (state.apiUp) stringResource(R.string.chat_starting)
            else stringResource(R.string.chat_core_starting)
        core is CoreState.Starting || core is CoreState.Restarting ->
            stringResource(R.string.chat_core_starting)
        else -> stringResource(R.string.chat_core_offline)
    }
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        if (error == null && state.connecting) {
            CircularProgressIndicator()
            Spacer(Modifier.height(20.dp))
        }
        Text(
            text = message,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        if (error != null && state.coreReady) {
            Spacer(Modifier.height(8.dp))
            TextButton(onClick = onRetry) { Text(stringResource(R.string.socks_retry)) }
        }
    }
}

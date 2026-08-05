package com.skycoin.skywire.ui.dex

import android.webkit.WebView
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.imePadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ArrowDropDown
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.compose.ui.viewinterop.AndroidView
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SkydexProfile
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.logs.LogSources

/**
 * SkyDEX: pick a market, dial it over Skywire, trade in the page the desktop
 * already serves.
 *
 * The split is deliberate. The market public key is entered *natively* — a
 * 66-character key wants the phone's own field, its paste behaviour and a list
 * of the markets this phone has used before — and everything past the
 * handshake is the embedded trading UI, unchanged. Its phone layout is a
 * separate piece of work; until then it is shown zoomed to fit and left
 * zoomable rather than clipped.
 */
@Composable
fun DexScreen(
    onBack: () -> Unit,
    onOpenLogs: (String) -> Unit,
    viewModel: DexViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()

    // The page's own failure, kept apart from the view model's: one is "the
    // market never answered", the other "it did and then the page broke".
    var pageError by remember { mutableStateOf<String?>(null) }
    var loadedUrl by remember { mutableStateOf<String?>(null) }
    // Read by the WebView client, which is built once — these keep it looking
    // at the current values instead of the ones at creation time.
    val url = rememberUpdatedState(state.uiUrl)
    val password = rememberUpdatedState(state.password)

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_skydex),
                onBack = onBack,
                actions = {
                    TextButton(onClick = { onOpenLogs(LogSources.app(SkydexProfile.APP)) }) {
                        Text(stringResource(R.string.logs_title))
                    }
                },
            )
        },
        // The system bars belong to the app scaffold this route sits in and
        // are consumed there — claiming them again would offset the page.
        contentWindowInsets = WindowInsets(0),
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                // The page's order forms sit low on the screen; without this
                // the keyboard covers the field it belongs to.
                .imePadding(),
        ) {
            if (state.connected) {
                ConnectedHeader(state, onDisconnect = viewModel::disconnect)
            } else {
                ConnectPanel(state, viewModel)
            }

            if (state.connected && pageError == null) {
                AndroidView(
                    modifier = Modifier.fillMaxSize(),
                    factory = { ctx ->
                        DexWebView.create(ctx).also { view ->
                            view.webViewClient = DexWebView.client(
                                baseUrl = { url.value },
                                password = { password.value },
                                onError = { pageError = it },
                            )
                            view.webChromeClient = DexWebView.chromeClient()
                        }
                    },
                    update = { view ->
                        val target = state.uiUrl
                        if (target != null && target != loadedUrl) {
                            loadedUrl = target
                            view.loadUrl(target)
                        }
                    },
                    onRelease = { view: WebView ->
                        // The page polls its market on a timer; it would keep
                        // going after the composable is gone.
                        DexWebView.release(view)
                        loadedUrl = null
                    },
                )
            } else if (pageError != null) {
                PageError(pageError) {
                    pageError = null
                    loadedUrl = null
                }
            }
        }
    }
}

// --- the native half: pick a market and dial it ---

@Composable
private fun ConnectPanel(state: DexUiState, viewModel: DexViewModel) {
    var recentsOpen by remember { mutableStateOf(false) }
    val entry = state.entry.trim()
    val malformed = entry.isNotEmpty() && !state.entryValid

    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 20.dp, vertical = 16.dp),
    ) {
        Column(Modifier.padding(20.dp)) {
            Text(
                stringResource(R.string.dex_market_title),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(10.dp))

            Box {
                OutlinedTextField(
                    value = state.entry,
                    onValueChange = viewModel::setEntry,
                    singleLine = true,
                    isError = malformed,
                    enabled = !state.busy,
                    label = { Text(stringResource(R.string.dex_market_label)) },
                    textStyle = MaterialTheme.typography.bodyMedium
                        .copy(fontFamily = FontFamily.Monospace),
                    trailingIcon = {
                        if (state.recents.isNotEmpty()) {
                            IconButton(onClick = { recentsOpen = true }, enabled = !state.busy) {
                                Icon(
                                    Icons.Default.ArrowDropDown,
                                    contentDescription = stringResource(R.string.dex_recents),
                                )
                            }
                        }
                    },
                    modifier = Modifier.fillMaxWidth(),
                )
                DropdownMenu(expanded = recentsOpen, onDismissRequest = { recentsOpen = false }) {
                    state.recents.forEach { market ->
                        DropdownMenuItem(
                            text = {
                                Text(
                                    market.label,
                                    style = MaterialTheme.typography.bodyMedium,
                                )
                            },
                            onClick = {
                                recentsOpen = false
                                viewModel.pickRecent(market)
                            },
                        )
                    }
                }
            }

            if (malformed) {
                Spacer(Modifier.height(6.dp))
                Text(
                    stringResource(R.string.dex_market_invalid),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            state.error?.let { error ->
                Spacer(Modifier.height(8.dp))
                Text(
                    error,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }

            Spacer(Modifier.height(16.dp))
            ConnectControl(state, viewModel)
        }
    }
}

@Composable
private fun ConnectControl(state: DexUiState, viewModel: DexViewModel) {
    if (!state.coreReady) {
        Text(
            stringResource(
                if (state.coreState is CoreState.Stopped) {
                    R.string.dex_core_offline
                } else {
                    R.string.dex_core_starting
                },
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        return
    }

    Button(
        onClick = viewModel::connect,
        enabled = !state.busy && state.entryValid,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(stringResource(R.string.connect))
    }
    Spacer(Modifier.height(8.dp))
    if (state.busy) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Spacer(Modifier.width(10.dp))
            Text(
                stringResource(R.string.dex_connecting),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    } else {
        Text(
            stringResource(R.string.dex_market_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/**
 * Once there is a market, the native half shrinks to one line: the page below
 * is the screen. It keeps Disconnect because that is the action the phone
 * owns — the page's own Disconnect only drops the session and leaves the app
 * running, which the poll then reflects here.
 */
@Composable
private fun ConnectedHeader(state: DexUiState, onDisconnect: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copied = stringResource(R.string.copied_to_clipboard)
    val pk = state.market?.marketPk.orEmpty()
    val name = state.market?.marketName.orEmpty()

    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .padding(start = 20.dp, end = 8.dp, top = 4.dp, bottom = 4.dp),
    ) {
        Box(
            Modifier
                .size(10.dp)
                .clip(CircleShape)
                .background(CONNECTED_GREEN),
        )
        Spacer(Modifier.width(10.dp))
        Column(
            Modifier
                .weight(1f)
                .clickable {
                    clipboard.setText(AnnotatedString(pk))
                    Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                },
        ) {
            Text(
                name.ifEmpty { stringResource(R.string.dex_market_title) },
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
            )
            Text(
                shortMarketPk(pk),
                style = MaterialTheme.typography.bodySmall
                    .copy(fontFamily = FontFamily.Monospace),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        TextButton(onClick = onDisconnect, enabled = !state.busy) {
            Text(stringResource(R.string.disconnect))
        }
    }
}

@Composable
private fun PageError(message: String?, onRetry: () -> Unit) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(32.dp),
        verticalArrangement = Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            message.orEmpty(),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
        Spacer(Modifier.height(8.dp))
        TextButton(onClick = onRetry) { Text(stringResource(R.string.socks_retry)) }
    }
}

private val CONNECTED_GREEN = Color(0xFF16A34A)

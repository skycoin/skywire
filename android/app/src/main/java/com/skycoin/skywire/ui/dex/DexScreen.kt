package com.skycoin.skywire.ui.dex

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.PrimaryScrollableTabRow
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Tab
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleEventObserver
import androidx.lifecycle.compose.LocalLifecycleOwner
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.dex.DexLocal
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.theme.SkyAccents

/**
 * SkyDEX: pick a market, dial it over Skywire, and trade on native screens
 * over skydex-client's control API — the same calls the web trading page
 * made, laid out for a phone.
 */
@Composable
fun DexScreen(
    onBack: () -> Unit,
    viewModel: DexViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val local by viewModel.local.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    LaunchedEffect(Unit) { viewModel.notices.collect { snackbar.showSnackbar(it) } }

    // The market is polled only while this screen is in front, as the page's timers were.
    val lifecycle = LocalLifecycleOwner.current.lifecycle
    DisposableEffect(lifecycle) {
        val observer = LifecycleEventObserver { _, event ->
            when (event) {
                Lifecycle.Event.ON_START -> viewModel.setVisible(true)
                Lifecycle.Event.ON_STOP -> viewModel.setVisible(false)
                else -> Unit
            }
        }
        lifecycle.addObserver(observer)
        if (lifecycle.currentState.isAtLeast(Lifecycle.State.STARTED)) viewModel.setVisible(true)
        onDispose {
            lifecycle.removeObserver(observer)
            viewModel.setVisible(false)
        }
    }

    // Back walks SkyDEX backwards: a tab back to Market, then the market
    // itself (leaving it is the disconnect), and only then the hub.
    val goBack: () -> Unit = {
        when {
            state.connected && state.tab != DexTab.MARKET -> viewModel.selectTab(DexTab.MARKET)
            state.connected -> viewModel.disconnect()
            else -> onBack()
        }
    }
    BackHandler(enabled = state.connected) { goBack() }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_skydex),
                onBack = goBack,
                help = HelpTopic(R.string.help_dex_title, R.string.help_dex_body),
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
        // The system bars belong to the app scaffold this route sits in and
        // are consumed there — claiming them again would offset the screen.
        contentWindowInsets = WindowInsets(0),
    ) { padding ->
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(padding)
                // The order form sits low on the screen; without this the
                // keyboard covers the field it belongs to.
                .imePadding(),
        ) {
            if (state.connected) {
                ConnectedHeader(state, onDisconnect = viewModel::disconnect)
                TabStrip(state.tab, viewModel::selectTab)
                Banners(state, local, viewModel)
                Box(Modifier.weight(1f)) {
                    when (state.tab) {
                        DexTab.MARKET -> MarketTab(state, local, viewModel)
                        DexTab.ORDERS -> OrdersTab(state, viewModel)
                        DexTab.LISTINGS -> ListingsTab(state, viewModel)
                        DexTab.HISTORY -> HistoryTab(local, viewModel)
                        DexTab.SETTINGS -> SettingsTab(state, local, viewModel)
                    }
                }
            } else {
                Column(Modifier.fillMaxSize().verticalScroll(rememberScrollState())) {
                    ConnectPanel(state, viewModel)
                    RecentMarkets(state, viewModel)
                }
            }
        }
    }
}

@Composable
private fun TabStrip(tab: DexTab, onSelect: (DexTab) -> Unit) {
    PrimaryScrollableTabRow(selectedTabIndex = tab.ordinal, edgePadding = 12.dp) {
        DexTab.entries.forEach { t ->
            Tab(
                selected = t == tab,
                onClick = { onSelect(t) },
                text = { Text(stringResource(tabLabel(t)), maxLines = 1) },
            )
        }
    }
}

private fun tabLabel(tab: DexTab): Int = when (tab) {
    DexTab.MARKET -> R.string.dex_tab_market
    DexTab.ORDERS -> R.string.dex_tab_orders
    DexTab.LISTINGS -> R.string.dex_tab_listings
    DexTab.HISTORY -> R.string.dex_tab_history
    DexTab.SETTINGS -> R.string.dex_tab_settings
}

/** Onboarding, and a market that has nothing to sell yet (App.jsx). */
@Composable
private fun Banners(state: DexUiState, local: DexLocal, vm: DexViewModel) {
    val register = !local.registered && state.tab != DexTab.SETTINGS
    val notReady = state.ready == false && (state.tab == DexTab.MARKET || state.tab == DexTab.LISTINGS)
    if (!register && !notReady) return
    Column(Modifier.padding(start = 16.dp, end = 16.dp, top = 12.dp)) {
        if (register) {
            DexBanner(
                stringResource(R.string.dex_banner_register_title) + " " + stringResource(R.string.dex_banner_register_body),
                warn = false,
                action = stringResource(R.string.dex_open_settings),
            ) { vm.selectTab(DexTab.SETTINGS) }
        }
        if (notReady) {
            if (register) Spacer(Modifier.height(8.dp))
            DexBanner(stringResource(R.string.dex_banner_not_ready), warn = true)
        }
    }
}

// --- pick a market and dial it ---

@Composable
private fun ConnectPanel(state: DexUiState, viewModel: DexViewModel) {
    val entry = state.entry.trim()
    val malformed = entry.isNotEmpty() && !state.entryValid

    Card(
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            // Cards hold real prose: content must default to ink, not the
            // muted onSurfaceVariant this container would otherwise imply.
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
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
            OutlinedTextField(
                value = state.entry,
                onValueChange = viewModel::setEntry,
                singleLine = true,
                isError = malformed,
                enabled = !state.busy,
                label = { Text(stringResource(R.string.dex_market_label)) },
                textStyle = MaterialTheme.typography.bodyMedium
                    .copy(fontFamily = FontFamily.Monospace),
                modifier = Modifier.fillMaxWidth(),
            )

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

/** Markets this phone joined before: one tap rejoins, ✕ forgets (ConnectGate.jsx). */
@Composable
private fun RecentMarkets(state: DexUiState, viewModel: DexViewModel) {
    if (state.recents.isEmpty()) return
    var confirmClear by remember { mutableStateOf(false) }
    val canConnect = state.coreReady && !state.busy
    Column(Modifier.padding(horizontal = 20.dp)) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                stringResource(R.string.dex_recents),
                style = MaterialTheme.typography.titleSmall,
                modifier = Modifier.weight(1f),
            )
            TextButton(onClick = { confirmClear = true }, enabled = !state.busy) { Text(stringResource(R.string.dex_clear_recents)) }
        }
        state.recents.forEachIndexed { i, market ->
            if (i > 0) HorizontalDivider()
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(enabled = canConnect) { viewModel.connectTo(market) }
                    .padding(vertical = 8.dp),
            ) {
                Column(Modifier.weight(1f)) {
                    if (market.name.isNotEmpty()) {
                        Text(market.name, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
                    }
                    Text(
                        shortMarketPk(market.pk),
                        style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (market.ts > 0) {
                    Text(dexAgo(market.ts), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                IconButton(onClick = { viewModel.removeRecent(market) }, enabled = !state.busy) {
                    Icon(Icons.Rounded.Close, stringResource(R.string.dex_remove_recent), Modifier.size(18.dp))
                }
            }
        }
        Spacer(Modifier.height(24.dp))
    }
    if (confirmClear) {
        DexConfirm(stringResource(R.string.dex_clear_recents_confirm), stringResource(R.string.dex_clear_recents), viewModel::clearRecents) { confirmClear = false }
    }
}

/**
 * Once there is a market, the native header shrinks to one line: which
 * market, and Disconnect — which also stops skydex-client, so no trading API
 * stays open on the phone for nothing.
 */
@Composable
private fun ConnectedHeader(state: DexUiState, onDisconnect: () -> Unit) {
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
                .background(SkyAccents.success),
        )
        Spacer(Modifier.width(10.dp))
        Column(
            Modifier
                .weight(1f)
                .clickable {
                    copyText(context, pk)
                    android.widget.Toast.makeText(context, copied, android.widget.Toast.LENGTH_SHORT).show()
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
        FilledTonalButton(onClick = onDisconnect, enabled = !state.busy) {
            Text(stringResource(R.string.disconnect))
        }
    }
}

package com.skycoin.skywire.ui.wallet

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.outlined.AccountBalanceWallet
import androidx.compose.material.icons.outlined.Add
import androidx.compose.material.icons.outlined.ArrowDownward
import androidx.compose.material.icons.outlined.ArrowUpward
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.Schedule
import androidx.compose.material.icons.outlined.Search
import androidx.compose.material3.Button
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.skywire.wallet.CoinSpec
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/**
 * Wallet tab root. With no wallet for the selected coin it opens straight
 * into setup; with one it is the balance screen. Either way the coin chip
 * sits on top — the tab is one surface for every coin the user holds.
 */
@Composable
fun WalletScreen(
    viewModel: WalletViewModel,
    onBack: () -> Unit,
    onCreate: () -> Unit,
    onRestore: () -> Unit,
    onReceive: () -> Unit,
    onSend: () -> Unit,
    onHistory: () -> Unit,
    onTx: (String) -> Unit,
    onWallets: () -> Unit,
    onAddCoin: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    var coinSheet by remember { mutableStateOf(false) }

    LaunchedEffect(state.message) {
        state.message?.let {
            snackbar.showSnackbar(it)
            viewModel.messageShown()
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        // The wallet design left the tab root bare and opened straight into
        // the balance. It carries the shared header now, so every tab but
        // Home is topped by the same three things in the same places.
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.tab_wallet),
                onBack = onBack,
                help = HelpTopic(R.string.help_wallet_title, R.string.help_wallet_body),
            )
        },
    ) { padding ->
        if (!state.ready) {
            Box(Modifier.fillMaxSize().padding(padding), contentAlignment = Alignment.Center) {
                CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
            }
            return@Scaffold
        }

        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, top = 8.dp, bottom = 16.dp),
        ) {
            item {
                CoinChip(coin = state.coin) { coinSheet = true }
            }

            if (state.active == null) {
                item {
                    IntroContent(
                        coin = state.coin,
                        onCreate = {
                            viewModel.startCreate()
                            onCreate()
                        },
                        onRestore = {
                            viewModel.startRestore()
                            onRestore()
                        },
                    )
                }
            } else {
                item {
                    BalanceHeader(state)
                    if (state.stale) StaleBanner(state)
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(top = 22.dp),
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        FilledTonalButton(
                            onClick = onReceive,
                            modifier = Modifier.weight(1f).height(50.dp),
                        ) {
                            Icon(Icons.Outlined.ArrowDownward, null, Modifier.size(17.dp))
                            Spacer(Modifier.size(8.dp))
                            Text(stringResource(R.string.wallet_receive), fontWeight = FontWeight.Bold)
                        }
                        Button(
                            onClick = onSend,
                            enabled = !state.stale,
                            modifier = Modifier.weight(1f).height(50.dp),
                        ) {
                            Icon(Icons.Outlined.ArrowUpward, null, Modifier.size(17.dp))
                            Spacer(Modifier.size(8.dp))
                            Text(stringResource(R.string.wallet_send), fontWeight = FontWeight.Bold)
                        }
                    }
                    if (state.stale) {
                        Text(
                            stringResource(R.string.wallet_stale_send_note),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.fillMaxWidth().padding(top = 10.dp),
                            textAlign = TextAlign.Center,
                        )
                    }
                    Row(
                        modifier = Modifier.fillMaxWidth().padding(top = 32.dp, bottom = 12.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            stringResource(R.string.wallet_recent).uppercase(),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.weight(1f))
                        Text(
                            stringResource(R.string.wallet_see_all),
                            style = MaterialTheme.typography.labelLarge,
                            color = MaterialTheme.colorScheme.primary,
                            modifier = Modifier.clickable(onClick = onHistory),
                        )
                    }
                }

                item {
                    val recent = state.snapshot?.txs?.take(3) ?: emptyList()
                    Card(
                        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            // Cards hold real prose: content must default to ink, not the
            // muted onSurfaceVariant this container would otherwise imply.
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
                        shape = RoundedCornerShape(16.dp),
                        modifier = Modifier.fillMaxWidth(),
                    ) {
                        if (recent.isEmpty()) {
                            Column(
                                Modifier.fillMaxWidth().padding(vertical = 34.dp, horizontal = 20.dp),
                                horizontalAlignment = Alignment.CenterHorizontally,
                            ) {
                                Text(
                                    stringResource(R.string.wallet_no_activity_title),
                                    style = MaterialTheme.typography.bodyLarge,
                                    fontWeight = FontWeight.Bold,
                                )
                                Text(
                                    stringResource(R.string.wallet_no_activity_body),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(top = 6.dp),
                                    textAlign = TextAlign.Center,
                                )
                            }
                        } else {
                            Column {
                                recent.forEachIndexed { i, tx ->
                                    TxRow(
                                        coin = state.coin,
                                        tx = tx,
                                        showStatus = false,
                                        topDivider = i > 0,
                                        onClick = { onTx(tx.txid) },
                                    )
                                }
                            }
                        }
                    }
                }

                item {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(top = 14.dp)
                            .clip(RoundedCornerShape(16.dp))
                            .background(MaterialTheme.colorScheme.surfaceVariant)
                            .clickable(onClick = onWallets)
                            .padding(horizontal = 16.dp, vertical = 15.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(12.dp),
                    ) {
                        Icon(
                            Icons.Outlined.AccountBalanceWallet,
                            null,
                            Modifier.size(18.dp),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Text(
                            stringResource(R.string.wallet_wallets_row),
                            style = MaterialTheme.typography.bodyLarge,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.weight(1f),
                        )
                        Text(
                            state.coinWallets.size.toString(),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Icon(
                            Icons.AutoMirrored.Outlined.KeyboardArrowRight,
                            null,
                            Modifier.size(18.dp),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }
        }
    }

    if (coinSheet) {
        CoinSheet(
            state = state,
            onPick = {
                viewModel.selectCoin(it)
                coinSheet = false
            },
            onAddCoin = {
                coinSheet = false
                onAddCoin()
            },
            onDismiss = { coinSheet = false },
        )
    }
}

@Composable
private fun CoinChip(coin: CoinSpec, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .clip(RoundedCornerShape(22.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .clickable(onClick = onClick)
            .padding(start = 7.dp, end = 12.dp, top = 7.dp, bottom = 7.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        CoinBadge(coin.ticker, 30)
        Text(coin.name, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Bold)
        Icon(
            Icons.Outlined.KeyboardArrowDown,
            contentDescription = stringResource(R.string.wallet_coin_chip_description),
            modifier = Modifier.size(16.dp),
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** Small circular ticker badge — the coin's "logo" everywhere in the tab. */
@Composable
fun CoinBadge(ticker: String, sizeDp: Int) {
    Box(
        modifier = Modifier
            .size(sizeDp.dp)
            .clip(CircleShape)
            .background(MaterialTheme.colorScheme.secondaryContainer),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            ticker.take(4),
            style = MaterialTheme.typography.labelSmall,
            fontWeight = FontWeight.Bold,
            color = MaterialTheme.colorScheme.primary,
        )
    }
}

@Composable
private fun BalanceHeader(state: WalletUiState) {
    val coin = state.coin
    val snapshot = state.snapshot
    Column(Modifier.padding(top = 22.dp)) {
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalAlignment = Alignment.CenterVertically) {
            Text(
                coin.amountText(snapshot?.confirmed ?: 0uL),
                style = MaterialTheme.typography.displaySmall,
                fontWeight = FontWeight.Bold,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
                modifier = Modifier.alignByBaseline(),
            )
            Text(
                coin.ticker,
                style = MaterialTheme.typography.titleMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.alignByBaseline(),
            )
            if (state.refreshing) {
                CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
            }
        }
        Text(
            text = when (coin.kind) {
                CoinKind.BTC -> stringResource(R.string.wallet_btc_sub, snapshot?.spendableOutputs ?: 0)
                CoinKind.ETH -> stringResource(R.string.wallet_eth_sub)
                CoinKind.ERC20 -> stringResource(R.string.wallet_erc20_sub, coin.ticker)
                else -> stringResource(R.string.wallet_hours_sub, hoursText(snapshot?.hours ?: 0uL))
            },
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 9.dp),
        )
    }
}

@Composable
private fun StaleBanner(state: WalletUiState) {
    val snapshot = state.snapshot
    val text = if (snapshot == null || snapshot.fetchedAtMs == 0L) {
        stringResource(R.string.wallet_stale_never)
    } else {
        val at = Instant.ofEpochMilli(snapshot.fetchedAtMs).atZone(ZoneId.systemDefault())
            .format(DateTimeFormatter.ofPattern("HH:mm"))
        val minutes = ((System.currentTimeMillis() - snapshot.fetchedAtMs) / 60000L).coerceAtLeast(1)
        val age = when {
            minutes >= 60 -> "${minutes / 60} h ${minutes % 60} min"
            minutes == 1L -> "1 minute"
            else -> "$minutes minutes"
        }
        stringResource(R.string.wallet_stale_banner, at, age)
    }
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 18.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(PENDING_AMBER.copy(alpha = 0.12f))
            .padding(horizontal = 14.dp, vertical = 13.dp),
        horizontalArrangement = Arrangement.spacedBy(9.dp),
    ) {
        Icon(Icons.Outlined.Schedule, null, Modifier.size(16.dp), tint = PENDING_AMBER)
        Text(text, style = MaterialTheme.typography.bodySmall, color = PENDING_AMBER)
    }
}

@Composable
private fun IntroContent(coin: CoinSpec, onCreate: () -> Unit, onRestore: () -> Unit) {
    Column(Modifier.padding(top = 38.dp)) {
        Text(
            stringResource(R.string.wallet_intro_title, coin.name),
            style = MaterialTheme.typography.headlineMedium,
            fontWeight = FontWeight.Bold,
        )
        Text(
            stringResource(R.string.wallet_intro_body),
            style = MaterialTheme.typography.bodyLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 12.dp),
        )
        Spacer(Modifier.height(32.dp))
        IntroCard(
            icon = { Icon(Icons.Outlined.Add, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.primary) },
            title = stringResource(R.string.wallet_intro_create),
            subtitle = stringResource(R.string.wallet_intro_create_sub),
            onClick = onCreate,
        )
        Spacer(Modifier.height(12.dp))
        IntroCard(
            icon = { Icon(Icons.Outlined.ArrowDownward, null, Modifier.size(20.dp), tint = MaterialTheme.colorScheme.primary) },
            title = stringResource(R.string.wallet_intro_restore),
            subtitle = stringResource(R.string.wallet_intro_restore_sub),
            onClick = onRestore,
        )
    }
}

@Composable
private fun IntroCard(
    icon: @Composable () -> Unit,
    title: String,
    subtitle: String,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(16.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .clickable(onClick = onClick)
            .padding(20.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        Box(
            modifier = Modifier
                .size(44.dp)
                .clip(CircleShape)
                .background(MaterialTheme.colorScheme.secondaryContainer),
            contentAlignment = Alignment.Center,
        ) { icon() }
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Bold)
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 3.dp),
            )
        }
        Icon(
            Icons.AutoMirrored.Outlined.KeyboardArrowRight,
            null,
            Modifier.size(16.dp),
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CoinSheet(
    state: WalletUiState,
    onPick: (String) -> Unit,
    onAddCoin: () -> Unit,
    onDismiss: () -> Unit,
) {
    var query by remember { mutableStateOf("") }
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState()) {
        Column(Modifier.padding(bottom = 28.dp)) {
            Text(
                stringResource(R.string.wallet_coins_title),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
            )
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                leadingIcon = { Icon(Icons.Outlined.Search, null, Modifier.size(16.dp)) },
                placeholder = { Text(stringResource(R.string.wallet_coins_search, state.coins.size)) },
                singleLine = true,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 12.dp),
                shape = RoundedCornerShape(12.dp),
            )
            val q = query.trim().lowercase()
            val filtered = state.coins.filter {
                q.isEmpty() || it.name.lowercase().contains(q) || it.ticker.lowercase().contains(q)
            }
            filtered.forEach { coin ->
                val walletCount = state.allWallets.count { it.coinId == coin.id }
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable { onPick(coin.id) }
                        .background(
                            if (coin.id == state.coin.id) MaterialTheme.colorScheme.surfaceVariant
                            else Color.Transparent,
                        )
                        .padding(horizontal = 20.dp, vertical = 13.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(13.dp),
                ) {
                    CoinBadge(coin.ticker, 36)
                    Column(Modifier.weight(1f)) {
                        Text(coin.name, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Bold)
                        Text(
                            when {
                                coin.id == CoinSpec.SKY.id -> stringResource(R.string.wallet_coin_native)
                                coin.kind == CoinKind.BTC -> stringResource(R.string.wallet_coin_btc)
                                coin.kind == CoinKind.ETH -> stringResource(R.string.wallet_coin_eth)
                                coin.kind == CoinKind.ERC20 -> stringResource(R.string.wallet_coin_erc20)
                                else -> stringResource(R.string.wallet_coin_fiber)
                            },
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 2.dp),
                        )
                    }
                    Text(
                        when (walletCount) {
                            0 -> "—"
                            1 -> stringResource(R.string.wallet_count_one)
                            else -> stringResource(R.string.wallet_count_many, walletCount)
                        },
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(onClick = onAddCoin)
                    .padding(horizontal = 20.dp, vertical = 15.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(13.dp),
            ) {
                Box(
                    modifier = Modifier
                        .size(36.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.surfaceContainerHighest),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(Icons.Outlined.Add, null, Modifier.size(18.dp), tint = MaterialTheme.colorScheme.primary)
                }
                Text(
                    stringResource(R.string.wallet_coins_add),
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.primary,
                )
            }
        }
    }
}

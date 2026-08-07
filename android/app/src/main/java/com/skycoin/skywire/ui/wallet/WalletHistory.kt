package com.skycoin.skywire.ui.wallet

import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.OpenInNew
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.net.toUri
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.CachedTx
import com.skycoin.wallet.Amounts
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlinx.coroutines.launch

private enum class TxFilter { ALL, SENT, RECEIVED, PENDING }

/** Full history: filter chips, transactions grouped by day. */
@Composable
fun WalletHistoryScreen(
    viewModel: WalletViewModel,
    onTx: (String) -> Unit,
    onReceive: () -> Unit,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val coin = state.coin
    var filter by remember { mutableStateOf(TxFilter.ALL) }

    val txs = state.snapshot?.txs.orEmpty().filter {
        when (filter) {
            TxFilter.ALL -> true
            TxFilter.SENT -> !it.incoming
            TxFilter.RECEIVED -> it.incoming
            TxFilter.PENDING -> !it.confirmed
        }
    }
    val groups = txs.groupBy { dayLabel(it.timestamp) }

    Scaffold(topBar = { SkyTopBar(stringResource(R.string.wallet_history_title), onBack = onBack) }) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(bottom = 24.dp),
        ) {
            item {
                Row(
                    modifier = Modifier
                        .horizontalScroll(rememberScrollState())
                        .padding(horizontal = 20.dp, vertical = 4.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    FilterChipButton(stringResource(R.string.wallet_filter_all), filter == TxFilter.ALL) { filter = TxFilter.ALL }
                    FilterChipButton(stringResource(R.string.wallet_filter_sent), filter == TxFilter.SENT) { filter = TxFilter.SENT }
                    FilterChipButton(stringResource(R.string.wallet_filter_received), filter == TxFilter.RECEIVED) { filter = TxFilter.RECEIVED }
                    FilterChipButton(stringResource(R.string.wallet_filter_pending), filter == TxFilter.PENDING) { filter = TxFilter.PENDING }
                }
            }

            if (groups.isEmpty()) {
                item {
                    Column(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(horizontal = 40.dp, vertical = 80.dp),
                        horizontalAlignment = Alignment.CenterHorizontally,
                    ) {
                        Box(
                            Modifier
                                .size(52.dp)
                                .clip(CircleShape)
                                .background(MaterialTheme.colorScheme.surfaceVariant),
                        )
                        Text(
                            stringResource(R.string.wallet_history_empty_title),
                            style = MaterialTheme.typography.titleMedium,
                            modifier = Modifier.padding(top = 20.dp),
                        )
                        Text(
                            stringResource(
                                if (filter == TxFilter.ALL) R.string.wallet_history_empty_all
                                else R.string.wallet_history_empty_filter,
                                coin.ticker,
                            ),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 8.dp),
                            textAlign = TextAlign.Center,
                        )
                        FilledTonalButton(onClick = onReceive, modifier = Modifier.padding(top = 20.dp)) {
                            Text(stringResource(R.string.wallet_history_show_address), fontWeight = FontWeight.Bold)
                        }
                    }
                }
            } else {
                groups.forEach { (day, dayTxs) ->
                    item {
                        Text(
                            day.uppercase(),
                            style = MaterialTheme.typography.labelSmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(start = 20.dp, end = 20.dp, top = 22.dp, bottom = 10.dp),
                        )
                        Card(
                            colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            // Cards hold real prose: content must default to ink, not the
            // muted onSurfaceVariant this container would otherwise imply.
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
                            shape = RoundedCornerShape(16.dp),
                            modifier = Modifier
                                .fillMaxWidth()
                                .padding(horizontal = 20.dp),
                        ) {
                            Column {
                                dayTxs.forEachIndexed { i, tx ->
                                    TxRow(
                                        coin = coin,
                                        tx = tx,
                                        showStatus = true,
                                        topDivider = i > 0,
                                        onClick = { onTx(tx.txid) },
                                    )
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun FilterChipButton(label: String, selected: Boolean, onClick: () -> Unit) {
    Text(
        label,
        style = MaterialTheme.typography.labelLarge,
        color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier
            .clip(RoundedCornerShape(18.dp))
            .background(
                if (selected) MaterialTheme.colorScheme.secondaryContainer
                else MaterialTheme.colorScheme.surfaceVariant,
            )
            .clickable(onClick = onClick)
            .padding(horizontal = 15.dp, vertical = 9.dp),
    )
}

/** One transaction, in full. */
@Composable
fun WalletTxScreen(
    viewModel: WalletViewModel,
    txid: String,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val coin = state.coin
    val tx = state.snapshot?.txs?.firstOrNull { it.txid == txid }
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val copied = stringResource(R.string.wallet_txid_copied)

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_tx_title), onBack = onBack) },
    ) { padding ->
        if (tx == null) {
            Box(Modifier.padding(padding).fillMaxWidth().padding(40.dp), contentAlignment = Alignment.Center) {
                Text(
                    stringResource(R.string.wallet_history_empty_title),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            return@Scaffold
        }
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            Column(
                modifier = Modifier.fillMaxWidth().padding(top = 12.dp, bottom = 24.dp),
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                Text(
                    (if (tx.incoming) "+" else "−") + coin.amountText(tx.amount) + " " + coin.ticker,
                    style = MaterialTheme.typography.headlineMedium,
                    fontWeight = FontWeight.Bold,
                    color = txColor(tx),
                )
                Row(
                    modifier = Modifier
                        .padding(top = 12.dp)
                        .clip(RoundedCornerShape(16.dp))
                        .background(txColor(tx).copy(alpha = 0.12f))
                        .padding(horizontal = 13.dp, vertical = 7.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(7.dp),
                ) {
                    Box(
                        Modifier
                            .size(6.dp)
                            .clip(CircleShape)
                            .background(txColor(tx)),
                    )
                    Text(
                        stringResource(
                            if (tx.confirmed) R.string.wallet_tx_confirmed
                            else R.string.wallet_tx_pending_long,
                        ),
                        style = MaterialTheme.typography.labelLarge,
                        color = txColor(tx),
                    )
                }
            }

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
                Column(Modifier.padding(horizontal = 16.dp)) {
                    DetailRow(
                        stringResource(if (tx.incoming) R.string.wallet_tx_from else R.string.wallet_tx_to),
                        tx.party?.let { shortAddress(it) } ?: "—",
                        first = true,
                    )
                    DetailRow(stringResource(R.string.wallet_tx_wallet), state.active?.name ?: "—")
                    DetailRow(
                        stringResource(R.string.wallet_tx_date),
                        Instant.ofEpochSecond(tx.timestamp).atZone(ZoneId.systemDefault())
                            .format(DateTimeFormatter.ofPattern("d MMMM, HH:mm")),
                    )
                    DetailRow(stringResource(R.string.wallet_tx_fee), feeText(coin, tx.fee))
                    DetailRow(
                        stringResource(R.string.wallet_tx_confirmations),
                        if (tx.confirmed) Amounts.groupThousands(tx.confirmations.toString()) else "0 of 1",
                    )
                    HorizontalDivider(color = MaterialTheme.colorScheme.surfaceContainerHighest)
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .clickable {
                                clipboard.setText(AnnotatedString(tx.txid))
                                scope.launch { snackbar.showSnackbar(copied) }
                            }
                            .padding(vertical = 14.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        Text(
                            stringResource(R.string.wallet_tx_id),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                        Spacer(Modifier.weight(1f))
                        Text(
                            shortAddress(tx.txid, 8, 6),
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.Bold,
                        )
                        Icon(
                            Icons.Outlined.ContentCopy, null,
                            Modifier.padding(start = 8.dp).size(15.dp),
                            tint = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            }

            coin.explorerTxUrl?.let { template ->
                FilledTonalButton(
                    onClick = {
                        val url = template.format(tx.txid)
                        context.startActivity(Intent(Intent.ACTION_VIEW, url.toUri()))
                    },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 14.dp)
                        .height(50.dp),
                ) {
                    Text(stringResource(R.string.wallet_tx_explorer), fontWeight = FontWeight.Bold)
                    Icon(
                        Icons.AutoMirrored.Outlined.OpenInNew, null,
                        Modifier.padding(start = 9.dp).size(15.dp),
                    )
                }
                Text(
                    stringResource(
                        R.string.wallet_tx_explorer_note,
                        template.format("").removeSuffix("/").toUri().host ?: "the explorer",
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 12.dp, bottom = 24.dp),
                    textAlign = TextAlign.Center,
                )
            }
        }
    }
}

@Composable
private fun DetailRow(label: String, value: String, first: Boolean = false) {
    if (!first) {
        HorizontalDivider(color = MaterialTheme.colorScheme.surfaceContainerHighest)
    }
    Row(Modifier.padding(vertical = 14.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.weight(1f))
        Text(value, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Bold)
    }
}

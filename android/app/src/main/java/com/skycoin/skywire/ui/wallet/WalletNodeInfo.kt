package com.skycoin.skywire.ui.wallet

import android.text.format.DateUtils
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.wallet.Amounts
import com.skycoin.wallet.skycoin.ChainInfo
import com.skycoin.wallet.skycoin.PeerInfo
import com.skycoin.wallet.skycoin.PendingInfo
import kotlinx.coroutines.launch
import java.text.DateFormat
import java.util.Date

private enum class NodeTab { CHAIN, NETWORK, PENDING, OUTPUTS }

/**
 * A fiber node's own pages, as skycoin-web's settings menu has them: the
 * chain's height and supply, the node's peers, its unconfirmed pool, and the
 * wallet's unspent outputs.
 */
@Composable
fun WalletNodeInfoScreen(viewModel: WalletViewModel, onBack: () -> Unit) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    var tab by remember { mutableStateOf(NodeTab.CHAIN) }
    var reload by remember { mutableIntStateOf(0) }
    var loading by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var chain by remember { mutableStateOf<ChainInfo?>(null) }
    var peers by remember { mutableStateOf<List<PeerInfo>>(emptyList()) }
    var pending by remember { mutableStateOf<List<PendingInfo>>(emptyList()) }
    var allPending by remember { mutableStateOf(false) }
    var removeTarget by remember { mutableStateOf<String?>(null) }
    val removed = stringResource(R.string.wallet_pending_removed)

    LaunchedEffect(tab, reload) {
        loading = true
        error = null
        when (tab) {
            NodeTab.CHAIN -> viewModel.chainInfo().onSuccess { chain = it }.onFailure { error = it.message }
            NodeTab.NETWORK -> viewModel.peers().onSuccess { peers = it }.onFailure { error = it.message }
            NodeTab.PENDING -> viewModel.pending().onSuccess { pending = it }.onFailure { error = it.message }
            NodeTab.OUTPUTS -> viewModel.loadUtxos()
        }
        loading = false
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_nodeinfo_title), onBack = onBack) },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, bottom = 24.dp),
        ) {
            item {
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    NodeTab.entries.forEachIndexed { i, t ->
                        SegmentedButton(
                            selected = tab == t,
                            onClick = { tab = t },
                            shape = SegmentedButtonDefaults.itemShape(i, NodeTab.entries.size),
                        ) {
                            Text(
                                stringResource(
                                    when (t) {
                                        NodeTab.CHAIN -> R.string.wallet_nodeinfo_tab_chain
                                        NodeTab.NETWORK -> R.string.wallet_nodeinfo_tab_network
                                        NodeTab.PENDING -> R.string.wallet_nodeinfo_tab_pending
                                        NodeTab.OUTPUTS -> R.string.wallet_nodeinfo_tab_outputs
                                    },
                                ),
                                maxLines = 1,
                            )
                        }
                    }
                }
                if (loading) {
                    CircularProgressIndicator(Modifier.padding(top = 24.dp).size(24.dp), strokeWidth = 2.dp)
                }
                error?.let {
                    Text(
                        it,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.error,
                        modifier = Modifier.padding(top = 18.dp),
                    )
                }
            }
            when (tab) {
                NodeTab.CHAIN -> chain?.let { c ->
                    item {
                        InfoCard(
                            listOf(
                                stringResource(R.string.wallet_chain_blocks) to Amounts.groupThousands(c.height.toString()),
                                stringResource(R.string.wallet_chain_time) to
                                    DateFormat.getDateTimeInstance(DateFormat.MEDIUM, DateFormat.SHORT).format(Date(c.timestamp * 1000)),
                                stringResource(R.string.wallet_chain_hash) to shortAddress(c.blockHash, 10, 8),
                                stringResource(R.string.wallet_chain_current_supply) to "${supply(c.currentSupply)} ${state.coin.ticker}",
                                stringResource(R.string.wallet_chain_total_supply) to "${supply(c.totalSupply)} ${state.coin.ticker}",
                            ),
                        )
                    }
                }
                NodeTab.NETWORK -> {
                    if (!loading && error == null && peers.isEmpty()) item { Empty(stringResource(R.string.wallet_peers_none)) }
                    items(peers, key = { it.address }) { p ->
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .padding(top = 12.dp)
                                .clip(RoundedCornerShape(14.dp))
                                .background(MaterialTheme.colorScheme.surfaceVariant)
                                .padding(horizontal = 16.dp, vertical = 12.dp),
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(p.address, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Bold)
                                Text(
                                    stringResource(
                                        R.string.wallet_peer_line,
                                        Amounts.groupThousands(p.height.toString()),
                                        DateUtils.getRelativeTimeSpanString(p.lastReceived * 1000).toString(),
                                    ),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(top = 2.dp),
                                )
                                Text(
                                    stringResource(if (p.trusted) R.string.wallet_peer_default else R.string.wallet_peer_exchange),
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                )
                            }
                            Text(
                                stringResource(if (p.outgoing) R.string.wallet_peer_outgoing else R.string.wallet_peer_incoming),
                                style = MaterialTheme.typography.labelMedium,
                                color = MaterialTheme.colorScheme.primary,
                            )
                        }
                    }
                }
                NodeTab.PENDING -> {
                    item {
                        SingleChoiceSegmentedButtonRow(Modifier.padding(top = 14.dp)) {
                            listOf(false, true).forEachIndexed { i, all ->
                                SegmentedButton(
                                    selected = allPending == all,
                                    onClick = { allPending = all },
                                    shape = SegmentedButtonDefaults.itemShape(i, 2),
                                ) {
                                    Text(stringResource(if (all) R.string.wallet_pending_all else R.string.wallet_pending_mine))
                                }
                            }
                        }
                    }
                    val shown = pending.filter { allPending || it.mine }
                    if (!loading && error == null && shown.isEmpty()) item { Empty(stringResource(R.string.wallet_pending_none)) }
                    items(shown, key = { it.txid }) { p ->
                        Row(
                            Modifier
                                .fillMaxWidth()
                                .padding(top = 12.dp)
                                .clip(RoundedCornerShape(14.dp))
                                .background(MaterialTheme.colorScheme.surfaceVariant)
                                .padding(start = 16.dp, end = 4.dp, top = 10.dp, bottom = 10.dp),
                            verticalAlignment = Alignment.CenterVertically,
                        ) {
                            Column(Modifier.weight(1f)) {
                                Text(shortAddress(p.txid, 10, 8), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Bold)
                                Text(
                                    "${state.coin.amountText(p.coins)} ${state.coin.ticker} · ${p.received.take(19).replace('T', ' ')}",
                                    style = MaterialTheme.typography.bodySmall,
                                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                                    modifier = Modifier.padding(top = 2.dp),
                                )
                            }
                            // Only the wallet's own: on a shared node, removing someone else's would be acting for them.
                            if (p.mine) {
                                IconButton(onClick = { removeTarget = p.txid }) {
                                    Icon(
                                        Icons.Outlined.DeleteOutline,
                                        contentDescription = stringResource(R.string.wallet_pending_delete),
                                        tint = MaterialTheme.colorScheme.error,
                                    )
                                }
                            }
                        }
                    }
                }
                NodeTab.OUTPUTS -> {
                    val outputs = state.send.utxos
                    if (!loading && outputs.isEmpty()) item { Empty(stringResource(R.string.wallet_outputs_none_wallet)) }
                    outputs.groupBy { it.address }.forEach { (addr, outs) ->
                        item(key = addr) {
                            Text(
                                shortAddress(addr),
                                style = MaterialTheme.typography.labelLarge,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(top = 18.dp, bottom = 4.dp),
                            )
                            InfoCard(
                                outs.map {
                                    shortAddress(it.uxid, 8, 6) to
                                        "${state.coin.amountText(it.coins)} ${state.coin.ticker} · ${hoursText(it.hours)}"
                                },
                            )
                        }
                    }
                }
            }
        }
    }

    removeTarget?.let { txid ->
        AlertDialog(
            onDismissRequest = { removeTarget = null },
            text = { Text(stringResource(R.string.wallet_pending_delete_confirm)) },
            confirmButton = {
                TextButton(onClick = {
                    removeTarget = null
                    scope.launch {
                        viewModel.deletePending(txid)
                            .onSuccess {
                                snackbar.showSnackbar(removed)
                                reload++
                            }
                            .onFailure { snackbar.showSnackbar(it.message.orEmpty()) }
                    }
                }) { Text(stringResource(R.string.wallet_pending_delete), color = MaterialTheme.colorScheme.error) }
            },
            dismissButton = { TextButton(onClick = { removeTarget = null }) { Text(stringResource(R.string.cancel)) } },
        )
    }
}

@Composable
private fun InfoCard(rows: List<Pair<String, String>>) {
    Column(
        Modifier
            .fillMaxWidth()
            .padding(top = 12.dp)
            .clip(RoundedCornerShape(14.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(horizontal = 16.dp, vertical = 4.dp),
        verticalArrangement = Arrangement.spacedBy(0.dp),
    ) {
        rows.forEach { (label, value) ->
            Row(Modifier.padding(vertical = 10.dp)) {
                Text(
                    label,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.weight(1f),
                )
                Text(value, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Bold)
            }
        }
    }
}

@Composable
private fun Empty(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 24.dp),
    )
}

/** A supply as the node writes it, "24239050.000000", shown grouped and without the empty decimals. */
private fun supply(text: String): String {
    val whole = text.substringBefore('.')
    val frac = text.substringAfter('.', "").trimEnd('0')
    return Amounts.groupThousands(whole) + if (frac.isEmpty()) "" else ".$frac"
}

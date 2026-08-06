package com.skycoin.skywire.ui.wallet

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Slider
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TextField
import androidx.compose.material3.TextFieldDefaults
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
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
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.CONNECTED_GREEN
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.wallet.Amounts
import kotlinx.coroutines.launch

/** Send: recipient, amount, the per-chain fee card, then review → sign. */
@Composable
fun WalletSendScreen(
    viewModel: WalletViewModel,
    onSent: () -> Unit,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val coin = state.coin
    val send = state.send
    val snackbar = remember { SnackbarHostState() }
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    var reviewSheet by remember { mutableStateOf(false) }

    val scanLauncher = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.let { raw ->
            // Accept plain addresses and bitcoin:/skycoin: URIs.
            val addr = raw.trim().substringAfterLast(":").substringBefore("?")
            viewModel.updateSend { it.copy(to = addr) }
        }
    }

    LaunchedEffect(Unit) { viewModel.loadFeePresets() }
    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }
    LaunchedEffect(state.active) { if (state.active == null) onBack() }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_send_title), onBack = onBack) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            SectionLabel(stringResource(R.string.wallet_send_to))
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 9.dp)
                    .clip(RoundedCornerShape(14.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .padding(start = 4.dp, end = 6.dp, top = 4.dp, bottom = 4.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                TextField(
                    value = send.to,
                    onValueChange = { new -> viewModel.updateSend { it.copy(to = new, planError = null) } },
                    placeholder = {
                        Text(
                            if (coin.kind == CoinKind.BTC) stringResource(R.string.wallet_send_to_hint_btc)
                            else stringResource(R.string.wallet_send_to_hint_fiber, coin.name),
                        )
                    },
                    singleLine = true,
                    colors = transparentFieldColors(),
                    modifier = Modifier.weight(1f),
                )
                TextButton(onClick = {
                    clipboard.getText()?.text?.trim()?.let { pasted ->
                        viewModel.updateSend { it.copy(to = pasted, planError = null) }
                    }
                }) {
                    Text(stringResource(R.string.wallet_paste), fontWeight = FontWeight.Bold)
                }
                IconButton(onClick = {
                    scanLauncher.launch(
                        ScanOptions()
                            .setDesiredBarcodeFormats(ScanOptions.QR_CODE)
                            .setBeepEnabled(false)
                            .setOrientationLocked(true),
                    )
                }) {
                    Icon(
                        Icons.Outlined.QrCodeScanner,
                        contentDescription = stringResource(R.string.wallet_scan_description),
                        modifier = Modifier.size(20.dp),
                    )
                }
            }

            Row(
                modifier = Modifier.fillMaxWidth().padding(top = 26.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                SectionLabel(stringResource(R.string.wallet_send_amount))
                Spacer(Modifier.weight(1f))
                Text(
                    stringResource(
                        R.string.wallet_send_available,
                        coin.amountText(state.snapshot?.confirmed ?: 0uL),
                        coin.ticker,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 9.dp)
                    .clip(RoundedCornerShape(14.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .padding(start = 4.dp, end = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                TextField(
                    value = send.amountText,
                    onValueChange = { new ->
                        viewModel.updateSend { it.copy(amountText = new, sendMax = false, planError = null) }
                    },
                    placeholder = { Text("0") },
                    singleLine = true,
                    textStyle = MaterialTheme.typography.headlineSmall.copy(fontWeight = FontWeight.Bold),
                    keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal),
                    colors = transparentFieldColors(),
                    modifier = Modifier.weight(1f),
                )
                Text(
                    coin.ticker,
                    style = MaterialTheme.typography.titleMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                TextButton(onClick = {
                    val max = viewModel.sendMaxAmount()
                    viewModel.updateSend {
                        it.copy(
                            amountText = Amounts.format(max, coin.exponent, coin.displayDecimals).replace(",", ""),
                            sendMax = true,
                            planError = null,
                        )
                    }
                }) {
                    Text(stringResource(R.string.wallet_max), fontWeight = FontWeight.Bold)
                }
            }
            Text(
                text = if (coin.kind == CoinKind.BTC) stringResource(R.string.wallet_max_note_btc)
                else stringResource(R.string.wallet_max_note_fiber, coin.ticker),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )

            SectionLabel(
                stringResource(R.string.wallet_fee),
                modifier = Modifier.padding(top = 26.dp),
            )
            if (coin.kind == CoinKind.BTC) {
                BtcFeeCard(viewModel)
            } else {
                FiberFeeCard(viewModel)
            }

            send.planError?.let { err ->
                Text(
                    err,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 12.dp),
                )
            }

            Button(
                onClick = { viewModel.buildPlan { reviewSheet = true } },
                enabled = !send.planning && !state.stale,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 26.dp, bottom = 24.dp)
                    .height(52.dp),
            ) {
                if (send.planning) {
                    CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(10.dp))
                }
                Text(stringResource(R.string.wallet_review), fontWeight = FontWeight.Bold)
            }
        }
    }

    if (reviewSheet && send.plan != null) {
        ReviewSheet(
            viewModel = viewModel,
            onDismiss = { reviewSheet = false },
            onConfirm = {
                reviewSheet = false
                confirmWithBiometrics(
                    context = context,
                    title = context.getString(R.string.wallet_bio_send_title),
                    subtitle = context.getString(R.string.wallet_bio_send_subtitle),
                ) {
                    viewModel.signAndSend(onSent = onSent)
                }
            },
        )
    }
}

@Composable
private fun SectionLabel(text: String, modifier: Modifier = Modifier) {
    Text(
        text.uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = modifier,
    )
}

@Composable
private fun transparentFieldColors() = TextFieldDefaults.colors(
    focusedContainerColor = Color.Transparent,
    unfocusedContainerColor = Color.Transparent,
    disabledContainerColor = Color.Transparent,
    focusedIndicatorColor = Color.Transparent,
    unfocusedIndicatorColor = Color.Transparent,
    disabledIndicatorColor = Color.Transparent,
)

/** The Coin Hours card: what burns now, what remains after. */
@Composable
private fun FiberFeeCard(viewModel: WalletViewModel) {
    val state by viewModel.uiState.collectAsState()
    val hours = state.snapshot?.hours ?: 0uL
    val plan = state.send.plan
    // Before a plan exists the burn is a projection off the whole balance —
    // a tenth of held hours — replaced by exact numbers once planned.
    val burned = plan?.fee ?: ((hours + 9uL) / 10uL)
    val after = if (hours >= burned) hours - burned else 0uL
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 9.dp)
            .clip(RoundedCornerShape(14.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(16.dp),
    ) {
        Row {
            Text(stringResource(R.string.wallet_hours_burned), style = MaterialTheme.typography.bodyLarge)
            Spacer(Modifier.weight(1f))
            Text(
                hoursText(burned),
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Bold,
            )
        }
        HorizontalDivider(
            modifier = Modifier.padding(vertical = 13.dp),
            color = MaterialTheme.colorScheme.surfaceContainerHighest,
        )
        Row {
            Text(
                stringResource(R.string.wallet_hours_after),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.weight(1f))
            Text(
                hoursText(after),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Text(
            stringResource(R.string.wallet_fee_note_fiber, state.coin.ticker),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 12.dp),
        )
    }
}

/** The sat/vB card: presets, slider, estimated fee. */
@Composable
private fun BtcFeeCard(viewModel: WalletViewModel) {
    val state by viewModel.uiState.collectAsState()
    val send = state.send
    val presets = send.presets
    Column(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 9.dp)
            .clip(RoundedCornerShape(14.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(16.dp),
    ) {
        if (presets != null) {
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FeePresetButton(
                    stringResource(R.string.wallet_fee_economy),
                    stringResource(R.string.wallet_fee_eta_economy),
                    presets.economy, send.feeRate, viewModel,
                    Modifier.weight(1f),
                )
                FeePresetButton(
                    stringResource(R.string.wallet_fee_normal),
                    stringResource(R.string.wallet_fee_eta_normal),
                    presets.normal, send.feeRate, viewModel,
                    Modifier.weight(1f),
                )
                FeePresetButton(
                    stringResource(R.string.wallet_fee_priority),
                    stringResource(R.string.wallet_fee_eta_priority),
                    presets.priority, send.feeRate, viewModel,
                    Modifier.weight(1f),
                )
            }
        }
        Row(modifier = Modifier.padding(top = 18.dp)) {
            Text(stringResource(R.string.wallet_fee_rate), style = MaterialTheme.typography.bodyLarge)
            Spacer(Modifier.weight(1f))
            Text(
                stringResource(R.string.wallet_fee_rate_value, send.feeRate),
                style = MaterialTheme.typography.bodyLarge,
                fontWeight = FontWeight.Bold,
            )
        }
        Slider(
            value = send.feeRate.toFloat(),
            onValueChange = { v ->
                viewModel.updateSend { it.copy(feeRate = v.toInt().coerceAtLeast(1), plan = null) }
            },
            valueRange = 1f..60f,
            modifier = Modifier.padding(top = 4.dp),
        )
        val vsize = send.plan?.vsize
        if (vsize != null) {
            HorizontalDivider(
                modifier = Modifier.padding(vertical = 13.dp),
                color = MaterialTheme.colorScheme.surfaceContainerHighest,
            )
            Row {
                Text(
                    stringResource(R.string.wallet_fee_estimate, vsize),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.weight(1f))
                Text(
                    "${Amounts.format(send.plan?.fee ?: 0uL, 8, 8)} BTC",
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

@Composable
private fun FeePresetButton(
    name: String,
    eta: String,
    rate: Int,
    currentRate: Int,
    viewModel: WalletViewModel,
    modifier: Modifier = Modifier,
) {
    val selected = rate == currentRate
    Column(
        modifier = modifier
            .clip(RoundedCornerShape(11.dp))
            .background(
                if (selected) MaterialTheme.colorScheme.secondaryContainer
                else MaterialTheme.colorScheme.surfaceContainerHighest,
            )
            .clickable { viewModel.updateSend { it.copy(feeRate = rate, plan = null) } }
            .padding(vertical = 11.dp, horizontal = 6.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(
            name,
            style = MaterialTheme.typography.labelLarge,
            color = if (selected) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            eta,
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 2.dp),
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun ReviewSheet(
    viewModel: WalletViewModel,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val coin = state.coin
    val send = state.send
    val plan = send.plan ?: return
    val snapshot = state.snapshot

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState()) {
        Column(Modifier.padding(horizontal = 20.dp).padding(bottom = 26.dp)) {
            Text(stringResource(R.string.wallet_review_title), style = MaterialTheme.typography.titleMedium)
            Text(
                "${coin.amountText(plan.amount)} ${coin.ticker}",
                style = MaterialTheme.typography.headlineMedium,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(top = 16.dp),
            )
            Text(
                // The network is the coin's own chain — Skycoin, this fiber
                // coin's, or Bitcoin. Skycoin is not a fiber coin.
                stringResource(
                    R.string.wallet_review_dest,
                    shortAddress(plan.toAddress),
                    coin.name,
                ),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 18.dp)
                    .clip(RoundedCornerShape(14.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .padding(horizontal = 15.dp, vertical = 2.dp),
            ) {
                if (coin.kind == CoinKind.BTC) {
                    val total = plan.amount + plan.fee
                    val after = (snapshot?.confirmed ?: 0uL).let { if (it >= total) it - total else 0uL }
                    ReviewRow(stringResource(R.string.wallet_review_amount), "${coin.amountText(plan.amount)} BTC", first = true)
                    ReviewRow(
                        stringResource(R.string.wallet_review_miner_fee, plan.feeRate ?: 0),
                        "${Amounts.format(plan.fee, 8, 8)} BTC",
                    )
                    ReviewRow(stringResource(R.string.wallet_review_total), "${Amounts.format(total, 8, 8)} BTC")
                    ReviewRow(stringResource(R.string.wallet_review_balance_after, "BTC"), Amounts.format(after, 8, 8))
                } else {
                    val afterCoins = (snapshot?.confirmed ?: 0uL).let { if (it >= plan.amount) it - plan.amount else 0uL }
                    val hoursNow = snapshot?.hours ?: 0uL
                    val hoursAfter = (plan.hoursToRecipient ?: 0uL).let { toDest ->
                        val kept = hoursNow - minOf(hoursNow, plan.fee + toDest)
                        kept
                    }
                    ReviewRow(
                        stringResource(R.string.wallet_review_amount),
                        "${coin.amountText(plan.amount)} ${coin.ticker}",
                        first = true,
                    )
                    ReviewRow(stringResource(R.string.wallet_hours_burned), hoursText(plan.fee))
                    ReviewRow(
                        stringResource(R.string.wallet_review_balance_after, coin.ticker),
                        coin.amountText(afterCoins),
                    )
                    ReviewRow(stringResource(R.string.wallet_review_hours_after), hoursText(hoursAfter))
                }
            }
            Row(
                modifier = Modifier.fillMaxWidth().padding(top = 18.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                FilledTonalButton(onClick = onDismiss, modifier = Modifier.weight(1f).height(50.dp)) {
                    Text(stringResource(R.string.wallet_review_back), fontWeight = FontWeight.Bold)
                }
                Button(
                    onClick = onConfirm,
                    enabled = !send.sending,
                    modifier = Modifier.weight(1.4f).height(50.dp),
                ) {
                    if (send.sending) {
                        CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                        Spacer(Modifier.width(10.dp))
                    }
                    Text(stringResource(R.string.wallet_review_sign), fontWeight = FontWeight.Bold)
                }
            }
        }
    }
}

@Composable
private fun ReviewRow(label: String, value: String, first: Boolean = false) {
    if (!first) {
        HorizontalDivider(color = MaterialTheme.colorScheme.surfaceContainerHighest)
    }
    Row(Modifier.padding(vertical = 13.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.weight(1f))
        Text(value, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Bold)
    }
}

/** The broadcast confirmation — a full screen, not a toast. */
@Composable
fun WalletResultScreen(
    viewModel: WalletViewModel,
    onDone: () -> Unit,
    onHistory: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val coin = state.coin
    val send = state.send
    val clipboard = LocalClipboardManager.current
    val snackbar = remember { SnackbarHostState() }
    val scope = rememberCoroutineScope()
    val copied = stringResource(R.string.wallet_txid_copied)

    Scaffold(snackbarHost = { SnackbarHost(snackbar) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 24.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Spacer(Modifier.height(60.dp))
            Box(
                modifier = Modifier
                    .size(64.dp)
                    .clip(CircleShape)
                    .background(CONNECTED_GREEN.copy(alpha = 0.15f)),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    Icons.Outlined.CheckCircle, null,
                    Modifier.size(32.dp),
                    tint = CONNECTED_GREEN,
                )
            }
            Text(
                stringResource(R.string.wallet_result_title),
                style = MaterialTheme.typography.headlineSmall,
                fontWeight = FontWeight.Bold,
                modifier = Modifier.padding(top = 22.dp),
                textAlign = TextAlign.Center,
            )
            Text(
                stringResource(
                    R.string.wallet_result_body,
                    coin.amountText(send.sentAmount),
                    coin.ticker,
                    shortAddress(send.sentTo, 6, 4),
                ),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 10.dp),
                textAlign = TextAlign.Center,
            )
            send.sentTxid?.let { txid ->
                Row(
                    modifier = Modifier
                        .padding(top = 22.dp)
                        .clip(RoundedCornerShape(22.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant)
                        .clickable {
                            clipboard.setText(AnnotatedString(txid))
                            scope.launch { snackbar.showSnackbar(copied) }
                        }
                        .padding(horizontal = 16.dp, vertical = 11.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(9.dp),
                ) {
                    Text(
                        shortAddress(txid, 10, 8),
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Bold,
                    )
                    Icon(
                        Icons.Outlined.ContentCopy, null,
                        Modifier.size(15.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(38.dp))
            Button(
                onClick = {
                    viewModel.resetSend()
                    onDone()
                },
                modifier = Modifier.fillMaxWidth().height(52.dp),
            ) {
                Text(stringResource(R.string.wallet_result_done), fontWeight = FontWeight.Bold)
            }
            TextButton(
                onClick = {
                    viewModel.resetSend()
                    onHistory()
                },
                modifier = Modifier.fillMaxWidth().padding(top = 6.dp, bottom = 24.dp),
            ) {
                Text(stringResource(R.string.wallet_result_history), fontWeight = FontWeight.Bold)
            }
        }
    }
}

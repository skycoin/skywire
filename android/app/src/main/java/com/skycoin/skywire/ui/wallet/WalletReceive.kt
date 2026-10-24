package com.skycoin.skywire.ui.wallet

import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.KeyboardArrowRight
import androidx.compose.material.icons.outlined.ContentCopy
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.KeyboardArrowUp
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.saveable.rememberSaveable
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
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.skywire.wallet.exposedAddresses
import kotlinx.coroutines.launch

/** Receive: the QR, tap-to-copy, share, and the other-addresses sheet. */
@Composable
fun WalletReceiveScreen(
    viewModel: WalletViewModel,
    onHistory: (String) -> Unit,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val coin = state.coin
    val wallet = state.active
    val snackbar = remember { SnackbarHostState() }
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var addressSheet by remember { mutableStateOf(false) }
    var outputsOf by remember { mutableStateOf<String?>(null) }
    var requestOpen by remember { mutableStateOf(false) }
    var reqAmount by remember { mutableStateOf("") }
    var reqHours by remember { mutableStateOf("") }
    var reqMessage by remember { mutableStateOf("") }

    val address = wallet?.receiveAddresses?.getOrNull(state.receiveIndex)
        ?: wallet?.receiveAddresses?.firstOrNull()

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }
    LaunchedEffect(wallet) { if (wallet == null) onBack() }
    if (wallet == null || address == null) return

    val request = coin.paymentRequest(address, reqAmount, reqHours, reqMessage)
    // The QR, Copy and Share carry the request once one is filled in; the chip stays the address.
    val shared = request.uri ?: address
    val copiedText = stringResource(R.string.wallet_address_copied)
    val requestCopiedText = stringResource(R.string.wallet_request_copied)
    fun copy(text: String = shared) {
        clipboard.setText(AnnotatedString(text))
        scope.launch { snackbar.showSnackbar(if (text == address) copiedText else requestCopiedText) }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_receive_title), onBack = onBack) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                stringResource(R.string.wallet_receive_only, coin.ticker),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            if (coin.kind == CoinKind.SKY_FIBER && address in state.snapshot?.exposedAddresses.orEmpty()) {
                Text(
                    stringResource(R.string.wallet_receive_exposed),
                    style = MaterialTheme.typography.bodySmall,
                    color = PENDING_AMBER,
                    textAlign = TextAlign.Center,
                    modifier = Modifier.padding(top = 8.dp),
                )
            }
            Box(
                modifier = Modifier
                    .padding(top = 20.dp)
                    .clip(RoundedCornerShape(20.dp))
                    .background(Color.White)
                    .padding(16.dp),
            ) {
                QrImage(shared)
            }
            Row(
                modifier = Modifier
                    .padding(top = 20.dp)
                    .clip(RoundedCornerShape(24.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .clickable { copy(address) }
                    .padding(horizontal = 18.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Text(
                    shortAddress(address),
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Bold,
                )
                Icon(
                    Icons.Outlined.ContentCopy, null,
                    Modifier.size(16.dp),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Text(
                stringResource(R.string.wallet_receive_tap_copy),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp),
            )
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 26.dp),
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Button(onClick = { copy() }, modifier = Modifier.weight(1f).height(50.dp)) {
                    Text(
                        stringResource(if (request.uri != null) R.string.wallet_request_copy else R.string.wallet_copy),
                        fontWeight = FontWeight.Bold,
                    )
                }
                FilledTonalButton(
                    onClick = {
                        val send = Intent(Intent.ACTION_SEND).apply {
                            type = "text/plain"
                            putExtra(Intent.EXTRA_TEXT, shared)
                        }
                        context.startActivity(Intent.createChooser(send, null))
                    },
                    modifier = Modifier.weight(1f).height(50.dp),
                ) {
                    Text(stringResource(R.string.wallet_share), fontWeight = FontWeight.Bold)
                }
            }
            Column(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 12.dp)
                    .clip(RoundedCornerShape(16.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant),
            ) {
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable { requestOpen = !requestOpen }
                        .padding(horizontal = 16.dp, vertical = 15.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        stringResource(R.string.wallet_request_title),
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.Bold,
                        modifier = Modifier.weight(1f),
                    )
                    Icon(
                        if (requestOpen) Icons.Outlined.KeyboardArrowUp else Icons.Outlined.KeyboardArrowDown,
                        null,
                        Modifier.size(18.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                if (requestOpen) {
                    Column(Modifier.padding(start = 16.dp, end = 16.dp, bottom = 16.dp)) {
                        OutlinedTextField(
                            value = reqAmount,
                            onValueChange = { reqAmount = it.take(40) },
                            label = { Text(stringResource(R.string.wallet_request_amount, coin.ticker)) },
                            isError = request.badAmount,
                            singleLine = true,
                            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal),
                            modifier = Modifier.fillMaxWidth(),
                        )
                        if (coin.kind == CoinKind.SKY_FIBER) {
                            OutlinedTextField(
                                value = reqHours,
                                onValueChange = { reqHours = it.take(18) },
                                label = { Text(stringResource(R.string.wallet_request_hours)) },
                                isError = request.badHours,
                                singleLine = true,
                                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                            )
                        }
                        if (!coin.isEthFamily) {
                            OutlinedTextField(
                                value = reqMessage,
                                onValueChange = { reqMessage = it.take(REQUEST_MESSAGE_MAX) },
                                label = { Text(stringResource(R.string.wallet_request_message)) },
                                singleLine = true,
                                modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
                            )
                        }
                        Text(
                            stringResource(
                                when {
                                    request.badAmount -> R.string.wallet_request_bad_amount
                                    request.badHours -> R.string.wallet_request_bad_hours
                                    else -> R.string.wallet_request_note
                                },
                            ),
                            style = MaterialTheme.typography.bodySmall,
                            color = if (request.badAmount || request.badHours) {
                                MaterialTheme.colorScheme.error
                            } else {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            },
                            modifier = Modifier.padding(top = 10.dp),
                        )
                    }
                }
            }
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 12.dp, bottom = 24.dp)
                    .clip(RoundedCornerShape(16.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .clickable { addressSheet = true }
                    .padding(horizontal = 16.dp, vertical = 15.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(12.dp),
            ) {
                Text(
                    stringResource(R.string.wallet_other_addresses),
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Bold,
                    modifier = Modifier.weight(1f),
                )
                Text(
                    wallet.receiveAddresses.size.toString(),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Icon(
                    Icons.AutoMirrored.Outlined.KeyboardArrowRight, null,
                    Modifier.size(16.dp),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }

    if (addressSheet) {
        AddressSheet(
            viewModel = viewModel,
            onDismiss = { addressSheet = false },
            onPicked = { addressSheet = false },
            onHistory = { addr ->
                addressSheet = false
                onHistory(addr)
            },
            onOutputs = { addr ->
                addressSheet = false
                outputsOf = addr
            },
        )
    }
    outputsOf?.let { addr ->
        OutputsSheet(viewModel = viewModel, address = addr, onDismiss = { outputsOf = null })
    }
}

/** The unspent outputs one address holds, as skycoin-web's per-address outputs view. Fiber only. */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun OutputsSheet(viewModel: WalletViewModel, address: String, onDismiss: () -> Unit) {
    val state by viewModel.uiState.collectAsState()
    LaunchedEffect(address) { viewModel.loadUtxos() }
    val outputs = state.send.utxos.filter { it.address == address }
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState()) {
        Column(Modifier.padding(bottom = 24.dp)) {
            Text(
                stringResource(R.string.wallet_outputs_title, shortAddress(address)),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 20.dp),
            )
            if (outputs.isEmpty()) {
                Text(
                    stringResource(R.string.wallet_outputs_none),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 20.dp, vertical = 12.dp),
                )
            }
            outputs.forEach { u ->
                Column(Modifier.fillMaxWidth().padding(horizontal = 20.dp, vertical = 10.dp)) {
                    Text(
                        "${state.coin.amountText(u.coins)} ${state.coin.ticker}",
                        style = MaterialTheme.typography.bodyMedium,
                        fontWeight = FontWeight.Bold,
                    )
                    Text(
                        stringResource(R.string.wallet_utxo_line, hoursText(u.hours), shortAddress(u.uxid, 10, 8)),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(top = 2.dp),
                    )
                }
            }
        }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AddressSheet(
    viewModel: WalletViewModel,
    onDismiss: () -> Unit,
    onPicked: () -> Unit,
    onHistory: (String) -> Unit,
    onOutputs: (String) -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val wallet = state.active ?: return
    val generated = stringResource(R.string.wallet_address_generated, wallet.name)
    var hideEmpty by rememberSaveable { mutableStateOf(false) }
    val exposed = state.snapshot?.exposedAddresses.orEmpty().takeIf { state.coin.kind == CoinKind.SKY_FIBER }.orEmpty()
    var menuFor by remember { mutableStateOf<String?>(null) }
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState()) {
        Column(Modifier.padding(bottom = 24.dp)) {
            Text(
                stringResource(R.string.wallet_addresses_in, wallet.name),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 20.dp),
            )
            Text(
                stringResource(R.string.wallet_addresses_note),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 6.dp),
            )
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable { hideEmpty = !hideEmpty }
                    .padding(horizontal = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Checkbox(checked = hideEmpty, onCheckedChange = { hideEmpty = it })
                Text(stringResource(R.string.wallet_addresses_hide_empty), style = MaterialTheme.typography.bodyMedium)
            }
            val perAddress = state.snapshot?.perAddress.orEmpty()
            wallet.receiveAddresses.forEachIndexed { i, addr ->
                val held = perAddress[addr]
                // The address being shown stays, so hiding never takes away what is on screen.
                if (hideEmpty && (held == null || held == 0uL) && i != state.receiveIndex) return@forEachIndexed
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clickable {
                            viewModel.pickReceiveIndex(i)
                            onPicked()
                        }
                        .padding(horizontal = 20.dp, vertical = 13.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(12.dp),
                ) {
                    Text(
                        "${i + 1}",
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.width(16.dp),
                    )
                    Column(Modifier.weight(1f)) {
                        Text(
                            shortAddress(addr),
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.Bold,
                        )
                        if (i == 0) {
                            Text(
                                stringResource(R.string.wallet_address_default),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(top = 2.dp),
                            )
                        } else if (held == null || held == 0uL) {
                            // With coins on it the balance line says more
                            // than "never used" ever could.
                            Text(
                                stringResource(R.string.wallet_address_unused),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.padding(top = 2.dp),
                            )
                        }
                        if (addr in exposed) {
                            Text(
                                stringResource(
                                    if ((held ?: 0uL) > 0uL) R.string.wallet_address_exposed_funded
                                    else R.string.wallet_address_exposed,
                                ),
                                style = MaterialTheme.typography.bodySmall,
                                color = PENDING_AMBER,
                                modifier = Modifier.padding(top = 2.dp),
                            )
                        }
                    }
                    if (held != null) {
                        Text(
                            state.coin.amountText(held),
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = if (held > 0uL) FontWeight.Bold else FontWeight.Normal,
                            color = if (held > 0uL) {
                                MaterialTheme.colorScheme.onSurface
                            } else {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            },
                        )
                    }
                    if (i == state.receiveIndex) {
                        Box(
                            Modifier
                                .size(8.dp)
                                .clip(androidx.compose.foundation.shape.CircleShape)
                                .background(MaterialTheme.colorScheme.primary),
                        )
                    }
                    Box {
                        IconButton(onClick = { menuFor = addr }, modifier = Modifier.size(32.dp)) {
                            Icon(
                                Icons.Outlined.MoreVert,
                                contentDescription = stringResource(R.string.wallet_address_actions),
                                modifier = Modifier.size(18.dp),
                            )
                        }
                        DropdownMenu(expanded = menuFor == addr, onDismissRequest = { menuFor = null }) {
                            DropdownMenuItem(
                                text = { Text(stringResource(R.string.wallet_address_history)) },
                                onClick = {
                                    menuFor = null
                                    onHistory(addr)
                                },
                            )
                            if (state.coin.kind == CoinKind.SKY_FIBER) {
                                DropdownMenuItem(
                                    text = { Text(stringResource(R.string.wallet_address_outputs)) },
                                    onClick = {
                                        menuFor = null
                                        onOutputs(addr)
                                    },
                                )
                            }
                        }
                    }
                }
            }
            FilledTonalButton(
                onClick = {
                    viewModel.generateNewAddress { }
                },
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(horizontal = 20.dp, vertical = 8.dp)
                    .height(48.dp),
            ) {
                Text(stringResource(R.string.wallet_address_new), fontWeight = FontWeight.Bold)
            }
        }
    }
}

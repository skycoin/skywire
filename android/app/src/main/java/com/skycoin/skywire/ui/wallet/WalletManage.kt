package com.skycoin.skywire.ui.wallet

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
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.CheckCircle
import androidx.compose.material.icons.outlined.DeleteOutline
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.MoreVert
import androidx.compose.material.icons.outlined.Visibility
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
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
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.SecureWindow
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.WalletMeta
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** Wallets of the selected coin: switch, rename, reveal, remove, add. */
@Composable
fun WalletManageScreen(
    viewModel: WalletViewModel,
    onAddWallet: () -> Unit,
    onRestoreWallet: () -> Unit,
    onReveal: (String) -> Unit,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    val context = LocalContext.current
    val scope = androidx.compose.runtime.rememberCoroutineScope()
    var sheetWallet by remember { mutableStateOf<WalletMeta?>(null) }
    var removeTarget by remember { mutableStateOf<WalletMeta?>(null) }
    var renameTarget by remember { mutableStateOf<WalletMeta?>(null) }

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_wallets_title), onBack = onBack) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
            verticalArrangement = Arrangement.spacedBy(10.dp),
        ) {
            state.coinWallets.forEach { wallet ->
                val isActive = wallet.id == state.active?.id
                Row(
                    modifier = Modifier
                        .fillMaxWidth()
                        .clip(RoundedCornerShape(16.dp))
                        .background(MaterialTheme.colorScheme.surfaceVariant)
                        .clickable { sheetWallet = wallet }
                        .padding(16.dp),
                    verticalAlignment = Alignment.CenterVertically,
                    horizontalArrangement = Arrangement.spacedBy(13.dp),
                ) {
                    Box(
                        modifier = Modifier
                            .size(38.dp)
                            .clip(CircleShape)
                            .background(MaterialTheme.colorScheme.secondaryContainer),
                        contentAlignment = Alignment.Center,
                    ) {
                        Text(
                            wallet.name.take(2).uppercase(),
                            style = MaterialTheme.typography.labelMedium,
                            color = MaterialTheme.colorScheme.primary,
                        )
                    }
                    Column(Modifier.weight(1f)) {
                        Row(
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(8.dp),
                        ) {
                            Text(
                                wallet.name,
                                style = MaterialTheme.typography.bodyLarge,
                                fontWeight = FontWeight.Bold,
                            )
                            if (isActive) {
                                Text(
                                    stringResource(R.string.wallet_active_badge),
                                    style = MaterialTheme.typography.labelSmall,
                                    color = MaterialTheme.colorScheme.primary,
                                    modifier = Modifier
                                        .clip(RoundedCornerShape(8.dp))
                                        .background(MaterialTheme.colorScheme.secondaryContainer)
                                        .padding(horizontal = 7.dp, vertical = 2.dp),
                                )
                            }
                        }
                        Text(
                            "${shortAddress(wallet.receiveAddresses.first(), 6, 3)} · " +
                                addressCount(wallet),
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.padding(top = 3.dp),
                        )
                    }
                    Icon(
                        Icons.Outlined.MoreVert, null,
                        Modifier.size(17.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Row(Modifier.fillMaxWidth().padding(top = 6.dp), horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                FilledTonalButton(
                    onClick = {
                        viewModel.startCreate()
                        onAddWallet()
                    },
                    modifier = Modifier.weight(1f).height(48.dp),
                ) {
                    Text(stringResource(R.string.wallet_manage_create), fontWeight = FontWeight.Bold, maxLines = 1)
                }
                FilledTonalButton(
                    onClick = {
                        viewModel.startRestore()
                        onRestoreWallet()
                    },
                    modifier = Modifier.weight(1f).height(48.dp),
                ) {
                    Text(stringResource(R.string.wallet_intro_restore), fontWeight = FontWeight.Bold, maxLines = 1)
                }
            }
            Spacer(Modifier.height(14.dp))
        }
    }

    sheetWallet?.let { wallet ->
        WalletActionSheet(
            wallet = wallet,
            isActive = wallet.id == state.active?.id,
            onUse = {
                viewModel.useWallet(wallet.id)
                sheetWallet = null
            },
            onRename = {
                sheetWallet = null
                renameTarget = wallet
            },
            onReveal = {
                sheetWallet = null
                confirmWithBiometrics(
                    context = context,
                    title = context.getString(R.string.wallet_bio_reveal_title),
                    subtitle = context.getString(R.string.wallet_bio_reveal_subtitle, wallet.name),
                ) {
                    onReveal(wallet.id)
                }
            },
            onRemove = {
                sheetWallet = null
                removeTarget = wallet
            },
            onDismiss = { sheetWallet = null },
        )
    }

    renameTarget?.let { wallet ->
        RenameDialog(
            wallet = wallet,
            onRename = { name ->
                viewModel.renameWallet(wallet.id, name)
                renameTarget = null
            },
            onDismiss = { renameTarget = null },
        )
    }

    removeTarget?.let { wallet ->
        val removedText = stringResource(R.string.wallet_removed, wallet.name)
        AlertDialog(
            onDismissRequest = { removeTarget = null },
            title = { Text(stringResource(R.string.wallet_remove_title, wallet.name)) },
            text = { Text(stringResource(R.string.wallet_remove_body)) },
            confirmButton = {
                TextButton(onClick = {
                    removeTarget = null
                    viewModel.removeWallet(wallet.id) {
                        scope.launch { snackbar.showSnackbar(removedText) }
                    }
                }) {
                    Text(
                        stringResource(R.string.wallet_remove_confirm),
                        color = MaterialTheme.colorScheme.error,
                        fontWeight = FontWeight.Bold,
                    )
                }
            },
            dismissButton = {
                TextButton(onClick = { removeTarget = null }) {
                    Text(stringResource(R.string.wallet_remove_keep), fontWeight = FontWeight.Bold)
                }
            },
        )
    }
}

@Composable
private fun addressCount(wallet: WalletMeta): String {
    val n = wallet.receiveAddresses.size
    return if (n == 1) stringResource(R.string.wallet_address_count_one)
    else stringResource(R.string.wallet_address_count_many, n)
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun WalletActionSheet(
    wallet: WalletMeta,
    isActive: Boolean,
    onUse: () -> Unit,
    onRename: () -> Unit,
    onReveal: () -> Unit,
    onRemove: () -> Unit,
    onDismiss: () -> Unit,
) {
    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = rememberModalBottomSheetState()) {
        Column(Modifier.padding(bottom = 20.dp)) {
            Text(
                wallet.name,
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.padding(horizontal = 20.dp),
            )
            Text(
                stringResource(
                    R.string.wallet_created_on,
                    addressCount(wallet),
                    Instant.ofEpochMilli(wallet.createdAtMs).atZone(ZoneId.systemDefault())
                        .format(DateTimeFormatter.ofPattern("d MMMM yyyy")),
                ),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 5.dp),
            )
            Spacer(Modifier.height(10.dp))
            if (!isActive) {
                SheetAction(
                    icon = { Icon(Icons.Outlined.CheckCircle, null, Modifier.size(19.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant) },
                    title = stringResource(R.string.wallet_use),
                    onClick = onUse,
                )
            }
            SheetAction(
                icon = { Icon(Icons.Outlined.Edit, null, Modifier.size(19.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant) },
                title = stringResource(R.string.wallet_rename),
                onClick = onRename,
            )
            SheetAction(
                icon = { Icon(Icons.Outlined.Visibility, null, Modifier.size(19.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant) },
                title = stringResource(R.string.wallet_reveal_action),
                subtitle = stringResource(R.string.wallet_reveal_action_sub),
                onClick = onReveal,
            )
            SheetAction(
                icon = { Icon(Icons.Outlined.DeleteOutline, null, Modifier.size(19.dp), tint = MaterialTheme.colorScheme.error) },
                title = stringResource(R.string.wallet_remove_action),
                subtitle = stringResource(R.string.wallet_remove_action_sub),
                titleColor = MaterialTheme.colorScheme.error,
                subtitleColor = MaterialTheme.colorScheme.error.copy(alpha = 0.8f),
                onClick = onRemove,
            )
        }
    }
}

@Composable
private fun SheetAction(
    icon: @Composable () -> Unit,
    title: String,
    subtitle: String? = null,
    titleColor: androidx.compose.ui.graphics.Color = MaterialTheme.colorScheme.onSurface,
    subtitleColor: androidx.compose.ui.graphics.Color = MaterialTheme.colorScheme.onSurfaceVariant,
    onClick: () -> Unit,
) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(horizontal = 20.dp, vertical = 14.dp),
        horizontalArrangement = Arrangement.spacedBy(14.dp),
    ) {
        icon()
        Column {
            Text(title, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Bold, color = titleColor)
            subtitle?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = subtitleColor,
                    modifier = Modifier.padding(top = 3.dp),
                )
            }
        }
    }
}

@Composable
private fun RenameDialog(
    wallet: WalletMeta,
    onRename: (String) -> Unit,
    onDismiss: () -> Unit,
) {
    var name by remember { mutableStateOf(wallet.name) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.wallet_rename_title)) },
        text = {
            OutlinedTextField(
                value = name,
                onValueChange = { name = it },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
        },
        confirmButton = {
            TextButton(
                onClick = { if (name.isNotBlank()) onRename(name) },
                enabled = name.isNotBlank(),
            ) {
                Text(stringResource(R.string.save), fontWeight = FontWeight.Bold)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text(stringResource(R.string.cancel), fontWeight = FontWeight.Bold)
            }
        },
    )
}

/** The phrase in the clear — reached only through the biometric confirm. */
@Composable
fun WalletRevealScreen(
    viewModel: WalletViewModel,
    walletId: String,
    onBack: () -> Unit,
) {
    SecureWindow()
    val state by viewModel.uiState.collectAsState()
    val wallet = state.allWallets.firstOrNull { it.id == walletId }
    var seed by remember { mutableStateOf<String?>(null) }
    var unavailable by remember { mutableStateOf(false) }
    var secondsLeft by remember { mutableStateOf(120) }

    LaunchedEffect(walletId) {
        val s = viewModel.revealSeed(walletId)
        if (s == null) unavailable = true else seed = s
    }
    LaunchedEffect(seed) {
        if (seed == null) return@LaunchedEffect
        while (secondsLeft > 0) {
            delay(1000)
            secondsLeft--
        }
        onBack()
    }

    Scaffold(topBar = { SkyTopBar(stringResource(R.string.wallet_seed_title), onBack = onBack) }) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(14.dp))
                    .background(MaterialTheme.colorScheme.errorContainer)
                    .padding(horizontal = 15.dp, vertical = 14.dp),
                horizontalArrangement = Arrangement.spacedBy(10.dp),
            ) {
                Icon(
                    Icons.Outlined.ErrorOutline, null,
                    Modifier.size(17.dp),
                    tint = MaterialTheme.colorScheme.onErrorContainer,
                )
                Text(
                    stringResource(R.string.wallet_reveal_warning, wallet?.name ?: ""),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onErrorContainer,
                )
            }
            Spacer(Modifier.height(20.dp))
            when {
                unavailable -> Text(
                    stringResource(R.string.wallet_seed_unavailable),
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.error,
                )
                seed != null -> SeedGrid(seed!!.split(" "))
            }
            if (seed != null) {
                Text(
                    stringResource(
                        R.string.wallet_reveal_autohide,
                        "%d:%02d".format(secondsLeft / 60, secondsLeft % 60),
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.fillMaxWidth().padding(top = 16.dp),
                    textAlign = TextAlign.Center,
                )
            }
            FilledTonalButton(
                onClick = onBack,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 22.dp, bottom = 24.dp)
                    .height(52.dp),
            ) {
                Text(stringResource(R.string.wallet_reveal_hide), fontWeight = FontWeight.Bold)
            }
        }
    }
}

/** Add a fiber coin: name + ticker + node URL. */
@Composable
fun WalletAddCoinScreen(
    viewModel: WalletViewModel,
    onAdded: () -> Unit,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    var name by remember { mutableStateOf("") }
    var ticker by remember { mutableStateOf("") }
    var node by remember { mutableStateOf("") }
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_add_coin_title), onBack = onBack) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            Text(
                stringResource(R.string.wallet_add_coin_body),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(24.dp))
            LabeledField(stringResource(R.string.wallet_add_coin_name), name, stringResource(R.string.wallet_add_coin_name_hint)) { name = it }
            LabeledField(stringResource(R.string.wallet_add_coin_ticker), ticker, stringResource(R.string.wallet_add_coin_ticker_hint)) { ticker = it }
            LabeledField(stringResource(R.string.wallet_add_coin_node), node, stringResource(R.string.wallet_add_coin_node_hint)) { node = it }
            Button(
                onClick = { viewModel.addFiberCoin(name, ticker, node, onDone = onAdded) },
                enabled = name.isNotBlank() && ticker.isNotBlank() && node.isNotBlank(),
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 12.dp, bottom = 24.dp)
                    .height(52.dp),
            ) {
                Text(stringResource(R.string.wallet_add_coin_save), fontWeight = FontWeight.Bold)
            }
        }
    }
}

@Composable
private fun LabeledField(label: String, value: String, hint: String, onChange: (String) -> Unit) {
    Column(Modifier.padding(bottom = 16.dp)) {
        Text(
            label,
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(bottom = 7.dp),
        )
        OutlinedTextField(
            value = value,
            onValueChange = onChange,
            placeholder = { Text(hint) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
            shape = RoundedCornerShape(12.dp),
        )
    }
}

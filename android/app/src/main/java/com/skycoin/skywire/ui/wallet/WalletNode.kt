package com.skycoin.skywire.ui.wallet

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.LockOpen
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.nodeUrlEditable

/**
 * Which node this coin's wallet talks to.
 *
 * Every balance, every transaction list and every broadcast for a fiber coin
 * goes to one daemon, and the app ships with an address for it. That address
 * is reachable from most places and not from all: it can be blocked, or
 * throttled to the point where a wallet with real history cannot finish
 * reading it. The escape is to say where else to look — a mirror, a node the
 * user runs, a plain-http one on their own network — and it is also the only
 * way to correct the address of a coin they added themselves, which used to
 * be fixed at the moment it was created.
 *
 * Nothing secret is being handed to whoever is named here. Keys never leave
 * the device and signing is local, so a hostile node can refuse to answer or
 * lie about a balance, but cannot spend anything. What it does see is which
 * addresses are asked about together, which is why the shipped address is
 * https and why saying so on this screen is worth the line.
 */
@Composable
fun WalletNodeScreen(
    viewModel: WalletViewModel,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    val coin = state.coin
    // Seeded from the address in force, so the field opens on what is
    // actually being used rather than on an empty box the user has to guess
    // the shape of.
    var url by remember(coin.id, coin.nodeUrl) { mutableStateOf(coin.nodeUrl) }

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_node_title), onBack = onBack) },
    ) { padding ->
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            Text(
                stringResource(R.string.wallet_node_body, coin.name),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 6.dp, bottom = 18.dp),
            )

            Text(
                stringResource(R.string.wallet_node_field),
                style = MaterialTheme.typography.labelLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(bottom = 7.dp),
            )
            OutlinedTextField(
                value = url,
                onValueChange = { url = it },
                placeholder = { Text(state.defaultNodeUrl) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp),
            )

            // Said where the choice is made, not in a settings page nobody
            // opens: an address typed here decides whether the address book
            // travels in the clear, and that is not recoverable afterwards.
            TransportNote(url)

            Spacer(Modifier.height(20.dp))
            Button(
                onClick = { viewModel.setNodeUrl(url, onBack) },
                enabled = url.isNotBlank() && url.trim() != coin.nodeUrl,
                modifier = Modifier.fillMaxWidth().height(50.dp),
            ) {
                Text(stringResource(R.string.wallet_node_save), fontWeight = FontWeight.Bold)
            }
            if (coin.nodeUrl != state.defaultNodeUrl || url.trim() != state.defaultNodeUrl) {
                TextButton(
                    onClick = { viewModel.setNodeUrl("", onBack) },
                    modifier = Modifier.fillMaxWidth().padding(top = 4.dp),
                ) {
                    Text(stringResource(R.string.wallet_node_default, state.defaultNodeUrl))
                }
            }
        }
    }
}

/** Whether what is typed will be encrypted on the wire, said plainly. */
@Composable
private fun TransportNote(url: String) {
    val cleartext = url.trim().startsWith("http://", ignoreCase = true)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = 14.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(
                if (cleartext) PENDING_AMBER.copy(alpha = 0.12f)
                else MaterialTheme.colorScheme.surfaceVariant,
            )
            .padding(horizontal = 14.dp, vertical = 13.dp),
        horizontalArrangement = Arrangement.spacedBy(9.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Icon(
            if (cleartext) Icons.Outlined.LockOpen else Icons.Outlined.Lock,
            null,
            Modifier.size(16.dp),
            tint = if (cleartext) PENDING_AMBER else MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            stringResource(
                if (cleartext) R.string.wallet_node_cleartext else R.string.wallet_node_encrypted,
            ),
            style = MaterialTheme.typography.bodySmall,
            color = if (cleartext) PENDING_AMBER else MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** Only the coins whose node address is the whole story — see [nodeUrlEditable]. */
internal fun canEditNode(state: WalletUiState): Boolean = state.coin.nodeUrlEditable

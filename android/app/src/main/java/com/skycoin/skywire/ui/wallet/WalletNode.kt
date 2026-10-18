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
import androidx.compose.material.icons.outlined.Close
import androidx.compose.material.icons.outlined.KeyboardArrowDown
import androidx.compose.material.icons.outlined.KeyboardArrowUp
import androidx.compose.material.icons.outlined.Lock
import androidx.compose.material.icons.outlined.LockOpen
import androidx.compose.material3.Button
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.CoinKind
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull

/**
 * Which nodes this coin's wallet talks to, and in what order.
 *
 * Every balance, every transaction list and every broadcast for a coin goes
 * to a daemon, and the app ships with an address for it. That address is
 * reachable from most places and not from all: it can be blocked, or
 * throttled to the point where a wallet with real history cannot finish
 * reading it. The escape is to say where else to look — a mirror, a node the
 * user runs, a plain-http one on their own network — and more than one can
 * be named, ordered: the first is asked first, and the shipped address rides
 * behind the list as the fallback.
 *
 * Nothing secret is being handed to whoever is named here. Keys never leave
 * the device and signing is local, so a hostile node can refuse to answer or
 * lie about a balance, but cannot spend anything. What it does see is which
 * addresses are asked about together, which is why the shipped address is
 * https and why saying so on this screen is worth the line.
 *
 * The Ethereum family reads history from an indexer apart from its node, so
 * there the screen holds that address too, saved on its own.
 */
@Composable
fun WalletNodeScreen(
    viewModel: WalletViewModel,
    onBack: () -> Unit,
) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    val coin = state.coin
    val context = LocalContext.current
    val shipped = state.defaultNodeUrl
    val shippedIndexer = state.defaultIndexerUrl
    var indexer by remember(coin.id, coin.indexerUrl) { mutableStateOf(coin.indexerUrl.orEmpty()) }
    val hasIndexer = shippedIndexer != null
    var add by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    fun typed(value: String) = value.trim().removeSuffix("/")

    // Every mutation saves at once: the order on screen is the order the
    // wallet asks in, with nothing waiting on a save button.
    fun apply(urls: List<String>, indexerUrl: String? = null) {
        viewModel.saveNodeUrls(urls, indexerUrl)
    }

    fun addNode() {
        val url = typed(add)
        when {
            url.isEmpty() -> Unit
            url.toHttpUrlOrNull() == null -> error = context.getString(R.string.wallet_node_invalid)
            url == shipped || url in state.nodeUrls ->
                error = context.getString(R.string.wallet_node_duplicate)
            else -> {
                error = null
                add = ""
                apply(state.nodeUrls + url)
            }
        }
    }

    fun move(index: Int, up: Boolean) {
        val to = if (up) index - 1 else index + 1
        if (to !in state.nodeUrls.indices) return
        val next = state.nodeUrls.toMutableList()
        java.util.Collections.swap(next, index, to)
        apply(next)
    }

    val indexerChanged = hasIndexer && typed(indexer) != coin.indexerUrl

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
                stringResource(
                    if (hasIndexer) R.string.wallet_node_body_indexer else R.string.wallet_node_body,
                    coin.name,
                ),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 6.dp, bottom = 10.dp),
            )
            Text(
                stringResource(R.string.wallet_node_list_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(bottom = 14.dp),
            )

            state.nodeUrls.forEachIndexed { i, url ->
                NodeRow(
                    position = i + 1,
                    url = url,
                    onUp = { move(i, up = true) },
                    onDown = { move(i, up = false) },
                    onRemove = { apply(state.nodeUrls.filterIndexed { j, _ -> j != i }) },
                )
            }
            // The fallback: fixed at the end, always there, not removable —
            // a coin is never left with nothing to ask.
            ShippedRow(shipped)

            Spacer(Modifier.height(18.dp))
            OutlinedTextField(
                value = add,
                onValueChange = { add = it; error = null },
                placeholder = { Text(stringResource(R.string.wallet_node_field)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
                modifier = Modifier.fillMaxWidth(),
                shape = RoundedCornerShape(12.dp),
            )
            error?.let { err ->
                Text(
                    err,
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(top = 6.dp),
                )
            }
            Button(
                onClick = { addNode() },
                enabled = typed(add).isNotEmpty(),
                modifier = Modifier.fillMaxWidth().padding(top = 10.dp).height(50.dp),
            ) {
                Text(stringResource(R.string.wallet_node_add), fontWeight = FontWeight.Bold)
            }
            TransportNote(typed(add))

            if (shippedIndexer != null) {
                Spacer(Modifier.height(20.dp))
                UrlField(
                    label = stringResource(R.string.wallet_indexer_field),
                    hint = stringResource(R.string.wallet_indexer_hint),
                    value = indexer,
                    placeholder = shippedIndexer,
                    onChange = { indexer = it },
                )
                Button(
                    onClick = { apply(state.nodeUrls, typed(indexer)) },
                    enabled = indexerChanged,
                    modifier = Modifier.fillMaxWidth().padding(top = 12.dp).height(50.dp),
                ) {
                    Text(stringResource(R.string.wallet_node_save), fontWeight = FontWeight.Bold)
                }
            }
        }
    }
}

/** One of the user's nodes: its priority, its address, and the reorder and
 *  remove controls. Position 1 is asked first. */
@Composable
private fun NodeRow(position: Int, url: String, onUp: () -> Unit, onDown: () -> Unit, onRemove: () -> Unit) {
    val cleartext = url.startsWith("http://", ignoreCase = true)
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 5.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant)
            .padding(start = 14.dp, end = 2.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            position.toString(),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            fontWeight = FontWeight.Bold,
        )
        Text(
            url,
            style = MaterialTheme.typography.bodyMedium,
            fontWeight = FontWeight.Bold,
            maxLines = 1,
            modifier = Modifier
                .weight(1f)
                .padding(horizontal = 10.dp),
        )
        Icon(
            if (cleartext) Icons.Outlined.LockOpen else Icons.Outlined.Lock,
            contentDescription = null,
            modifier = Modifier.size(14.dp),
            tint = if (cleartext) PENDING_AMBER else MaterialTheme.colorScheme.onSurfaceVariant,
        )
        IconButton(onClick = onUp, enabled = position > 1) {
            Icon(
                Icons.Outlined.KeyboardArrowUp,
                contentDescription = stringResource(R.string.wallet_node_up),
                modifier = Modifier.size(18.dp),
            )
        }
        IconButton(onClick = onDown) {
            Icon(
                Icons.Outlined.KeyboardArrowDown,
                contentDescription = stringResource(R.string.wallet_node_down),
                modifier = Modifier.size(18.dp),
            )
        }
        IconButton(onClick = onRemove) {
            Icon(
                Icons.Outlined.Close,
                contentDescription = stringResource(R.string.wallet_node_remove),
                modifier = Modifier.size(18.dp),
            )
        }
    }
}

/** The shipped address, fixed as the last resort. */
@Composable
private fun ShippedRow(url: String) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = 5.dp)
            .clip(RoundedCornerShape(12.dp))
            .background(MaterialTheme.colorScheme.surfaceVariant.copy(alpha = 0.5f))
            .padding(horizontal = 14.dp, vertical = 11.dp),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Icon(
            Icons.Outlined.Lock,
            contentDescription = null,
            modifier = Modifier.size(14.dp),
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            url,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            modifier = Modifier.weight(1f),
        )
        Text(
            stringResource(R.string.wallet_node_shipped),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** One address field: its label, what kind of server it takes, and whether it is encrypted. */
@Composable
private fun UrlField(
    label: String,
    hint: String?,
    value: String,
    placeholder: String,
    onChange: (String) -> Unit,
) {
    Text(
        label,
        style = MaterialTheme.typography.labelLarge,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(bottom = 7.dp),
    )
    OutlinedTextField(
        value = value,
        onValueChange = onChange,
        placeholder = { Text(placeholder) },
        singleLine = true,
        keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Uri),
        modifier = Modifier.fillMaxWidth(),
        shape = RoundedCornerShape(12.dp),
    )
    if (hint != null) {
        Text(
            hint,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.padding(top = 6.dp),
        )
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
                if (cleartext && url.isNotBlank()) PENDING_AMBER.copy(alpha = 0.12f)
                else MaterialTheme.colorScheme.surfaceVariant,
            )
            .padding(horizontal = 14.dp, vertical = 13.dp),
        horizontalArrangement = Arrangement.spacedBy(9.dp),
        verticalAlignment = Alignment.Top,
    ) {
        Icon(
            if (cleartext && url.isNotBlank()) Icons.Outlined.LockOpen else Icons.Outlined.Lock,
            null,
            Modifier.size(16.dp),
            tint = if (cleartext && url.isNotBlank()) PENDING_AMBER else MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Text(
            stringResource(
                if (cleartext && url.isNotBlank()) R.string.wallet_node_cleartext else R.string.wallet_node_encrypted,
            ),
            style = MaterialTheme.typography.bodySmall,
            color = if (cleartext && url.isNotBlank()) PENDING_AMBER else MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

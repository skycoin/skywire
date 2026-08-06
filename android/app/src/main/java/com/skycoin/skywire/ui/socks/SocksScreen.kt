package com.skycoin.skywire.ui.socks

import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
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
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.api.AppConnection
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.ui.components.CONNECTED_GREEN
import com.skycoin.skywire.ui.components.InfoRow
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.SavedServer
import com.skycoin.skywire.ui.components.SectionCard
import com.skycoin.skywire.ui.components.ServerRow
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.TransportPreferenceCard
import com.skycoin.skywire.ui.components.TransportPreferenceSheet
import com.skycoin.skywire.ui.components.flagEmoji
import com.skycoin.skywire.ui.components.formatBytes
import com.skycoin.skywire.ui.components.shortPk

/**
 * SkySOCKS: pick a public proxy server, point skysocks-client at it, and
 * watch what it does. The pattern every app screen repeats — list from
 * service discovery, configure, start, observe, plus a per-app `Logs`
 * action in the bar.
 */
@Composable
fun SocksScreen(
    onBack: () -> Unit,
    viewModel: SocksViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    var portSheetOpen by remember { mutableStateOf(false) }
    var transportSheetOpen by remember { mutableStateOf(false) }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_skysocks),
                onBack = onBack,
                help = HelpTopic(R.string.help_socks_title, R.string.help_socks_body),
            )
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item { StatusCard(state, viewModel) }
            item { ProxyAddressCard(state, onChangePort = { portSheetOpen = true }) }
            item {
                TransportPreferenceCard(
                    primary = state.transportPrimary,
                    // Changeable with the core down too — it is stored on the
                    // phone and applied when the visor next starts.
                    enabled = !state.busy,
                    onClick = { transportSheetOpen = true },
                )
            }

            if (state.coreReady) {
                item { ServersHeader(state, viewModel) }
                items(state.filteredServers, key = { it.address }) { server ->
                    ServerRow(
                        server = server,
                        selected = server.pk == state.selectedPk,
                        enabled = !state.busy,
                        onClick = { viewModel.connect(SavedServer.of(server)) },
                    )
                }
                item { ServersFooter(state, viewModel) }
            }
        }
    }

    if (portSheetOpen) {
        PortSheet(
            current = state.listenPort,
            onDismiss = { portSheetOpen = false },
            onSave = { port ->
                viewModel.setListenPort(port)
                portSheetOpen = false
            },
        )
    }

    if (transportSheetOpen) {
        TransportPreferenceSheet(
            current = state.transportPrimary,
            onDismiss = { transportSheetOpen = false },
            onSelect = { type ->
                viewModel.setTransportPrimary(type)
                transportSheetOpen = false
            },
        )
    }
}

// --- status ---

@Composable
private fun StatusCard(state: SocksUiState, viewModel: SocksViewModel) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copied = stringResource(R.string.copied_to_clipboard)

    SectionCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            val (label, color) = statusLabel(state)
            Box(
                Modifier
                    .size(10.dp)
                    .clip(CircleShape)
                    .background(color),
            )
            Spacer(Modifier.width(8.dp))
            Text(label, style = MaterialTheme.typography.titleMedium)
            if (state.busy) {
                Spacer(Modifier.width(10.dp))
                CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            }
        }

        // The visor's own wording for what the app is doing ("Starting",
        // "Connection failed, reconnecting", …) — more useful than the
        // numeric status whenever it disagrees with the label above.
        state.app?.detailedStatus
            ?.takeIf { it.isNotEmpty() && !it.equals(RUNNING_STATUS, ignoreCase = true) }
            ?.let { detail ->
                Spacer(Modifier.height(4.dp))
                Text(
                    detail,
                    style = MaterialTheme.typography.bodySmall,
                    color = if (state.errored) {
                        MaterialTheme.colorScheme.error
                    } else {
                        MaterialTheme.colorScheme.onSurfaceVariant
                    },
                )
            }

        state.selectedPk?.let { pk ->
            Spacer(Modifier.height(12.dp))
            InfoRow(
                label = stringResource(R.string.socks_server),
                value = listOfNotNull(
                    state.selectedCountry?.let(::flagEmoji),
                    shortPk(pk),
                ).joinToString(" "),
                mono = true,
                modifier = Modifier.clickable {
                    clipboard.setText(AnnotatedString(pk))
                    Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                },
            )
        }

        state.connection?.let { connection ->
            ConnectionRows(connection)
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

@Composable
private fun ConnectionRows(connection: AppConnection) {
    // skysocks-client never measures round trips, so its latency is a
    // constant zero — showing "0 ms" would just be wrong.
    connection.latencyMs.takeIf { it > 0 }?.let { latency ->
        InfoRow(
            label = stringResource(R.string.socks_latency),
            value = "$latency ms",
        )
    }
    InfoRow(
        label = stringResource(R.string.socks_transferred),
        value = "↑ ${formatBytes(connection.bandwidthSent)}  ↓ ${formatBytes(connection.bandwidthReceived)}",
    )
    connection.error.takeIf { it.isNotEmpty() }?.let {
        Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
    }
}

@Composable
private fun ConnectControl(state: SocksUiState, viewModel: SocksViewModel) {
    if (!state.coreReady) {
        Text(
            stringResource(
                if (state.coreState is CoreState.Stopped) {
                    R.string.socks_core_offline
                } else {
                    R.string.socks_core_starting
                },
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        return
    }

    val active = state.running || state.starting
    Button(
        onClick = { if (active) viewModel.disconnect() else viewModel.reconnect() },
        enabled = !state.busy && (active || state.selectedPk != null),
        modifier = Modifier.fillMaxWidth(),
        colors = if (active) {
            ButtonDefaults.buttonColors(
                containerColor = MaterialTheme.colorScheme.secondaryContainer,
                contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
            )
        } else {
            ButtonDefaults.buttonColors()
        },
    ) {
        Text(
            stringResource(
                when {
                    active -> R.string.disconnect
                    state.selectedPk != null -> R.string.socks_reconnect
                    else -> R.string.connect
                },
            ),
        )
    }
    if (!active && state.selectedPk == null) {
        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.socks_pick_server),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// --- the proxy address other apps use ---

@Composable
private fun ProxyAddressCard(state: SocksUiState, onChangePort: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copied = stringResource(R.string.copied_to_clipboard)

    SectionCard {
        Text(
            stringResource(R.string.socks_proxy_title),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(
                state.listenAddress,
                style = MaterialTheme.typography.titleMedium.copy(fontFamily = FontFamily.Monospace),
                modifier = Modifier
                    .weight(1f)
                    .clickable {
                        clipboard.setText(AnnotatedString(state.listenAddress))
                        Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                    },
            )
            TextButton(onClick = onChangePort, enabled = state.coreReady && !state.busy) {
                Text(stringResource(R.string.socks_change_port))
            }
        }
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.socks_proxy_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun PortSheet(current: Int, onDismiss: () -> Unit, onSave: (Int) -> Unit) {
    val sheetState = rememberModalBottomSheetState()
    var text by remember { mutableStateOf(current.toString()) }
    val port = text.toIntOrNull()
    val valid = port != null && port in MIN_PORT..MAX_PORT

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 32.dp)) {
            Text(
                stringResource(R.string.socks_port_title),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.socks_port_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = text,
                onValueChange = { text = it.filter(Char::isDigit).take(5) },
                singleLine = true,
                isError = text.isNotEmpty() && !valid,
                label = { Text(stringResource(R.string.socks_port_label)) },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth(),
            )
            if (text.isNotEmpty() && !valid) {
                Spacer(Modifier.height(6.dp))
                Text(
                    stringResource(R.string.socks_port_invalid),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Spacer(Modifier.height(20.dp))
            Row(horizontalArrangement = Arrangement.End, modifier = Modifier.fillMaxWidth()) {
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
                Spacer(Modifier.width(8.dp))
                Button(onClick = { onSave(port!!) }, enabled = valid) {
                    Text(stringResource(R.string.save))
                }
            }
        }
    }
}

// --- server list ---

@Composable
private fun ServersHeader(state: SocksUiState, viewModel: SocksViewModel) {
    Column {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(
                stringResource(R.string.socks_servers, state.servers.size),
                style = MaterialTheme.typography.titleMedium,
            )
            if (state.serversLoading) {
                CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
            } else {
                IconButton(onClick = viewModel::refreshServers) {
                    Icon(
                        Icons.Default.Refresh,
                        contentDescription = stringResource(R.string.socks_refresh),
                    )
                }
            }
        }
        OutlinedTextField(
            value = state.query,
            onValueChange = viewModel::setQuery,
            singleLine = true,
            placeholder = { Text(stringResource(R.string.socks_search_hint)) },
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun ServersFooter(state: SocksUiState, viewModel: SocksViewModel) {
    val serversError = state.serversError
    when {
        serversError != null -> Column {
            Text(
                serversError,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            TextButton(onClick = viewModel::refreshServers) {
                Text(stringResource(R.string.socks_retry))
            }
        }
        state.serversLoading && state.servers.isEmpty() -> Unit
        state.filteredServers.isEmpty() -> Text(
            stringResource(
                if (state.servers.isEmpty()) R.string.socks_servers_none else R.string.socks_servers_empty,
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

// --- small shared pieces ---

@Composable
private fun statusLabel(state: SocksUiState): Pair<String, Color> = when {
    !state.coreReady -> stringResource(R.string.state_disconnected) to
        MaterialTheme.colorScheme.onSurfaceVariant
    state.running -> stringResource(R.string.state_connected) to CONNECTED_GREEN
    state.starting -> stringResource(R.string.socks_state_connecting) to PENDING_AMBER
    state.errored -> stringResource(R.string.socks_state_error) to MaterialTheme.colorScheme.error
    else -> stringResource(R.string.state_disconnected) to
        MaterialTheme.colorScheme.onSurfaceVariant
}

private const val RUNNING_STATUS = "Running"
private const val MIN_PORT = 1024
private const val MAX_PORT = 65535

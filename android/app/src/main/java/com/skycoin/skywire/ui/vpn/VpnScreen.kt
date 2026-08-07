package com.skycoin.skywire.ui.vpn

import android.app.Activity
import android.content.Intent
import android.net.VpnService
import android.provider.Settings
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
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
import com.skycoin.skywire.ui.components.formatDuration
import com.skycoin.skywire.ui.components.shortPk

/**
 * SkyVPN: pick an exit, route the whole phone through it.
 *
 * The same list-and-connect shape as SkySOCKS, with the two things a
 * whole-phone tunnel adds — the killswitch, and stats worth watching while it
 * carries your traffic. What makes it whole-phone is not on this screen: it
 * is [com.skycoin.skywire.core.SkyVpnService], which owns the network
 * interface and hands its descriptor to the visor.
 */
@Composable
fun VpnScreen(
    onBack: () -> Unit,
    viewModel: VpnViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    var transportSheetOpen by remember { mutableStateOf(false) }

    // Android will not create a TUN until the user has agreed to it, in the
    // system's own dialog. prepare() returns the intent that asks; a null
    // means consent is already given and we can go straight to connecting.
    var pendingServer by remember { mutableStateOf<SavedServer?>(null) }
    val consentDenied = stringResource(R.string.vpn_error_consent_denied)
    val consent = rememberLauncherForActivityResult(
        ActivityResultContracts.StartActivityForResult(),
    ) { result ->
        val server = pendingServer
        pendingServer = null
        if (result.resultCode == Activity.RESULT_OK && server != null) {
            viewModel.connect(server)
        } else {
            viewModel.fail(consentDenied)
        }
    }
    val connect: (SavedServer) -> Unit = { server ->
        val ask = VpnService.prepare(context)
        if (ask == null) {
            viewModel.connect(server)
        } else {
            pendingServer = server
            consent.launch(ask)
        }
    }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_skyvpn),
                onBack = onBack,
                help = HelpTopic(R.string.help_vpn_title, R.string.help_vpn_body),
            )
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item { StatusCard(state, viewModel, connect) }
            if (state.tunnel.established || state.running) {
                item { StatsCard(state) }
            }
            item {
                KillswitchCard(
                    on = state.killswitch,
                    // Changeable with the core down too — it is stored on the
                    // phone and applied when the visor next starts.
                    enabled = !state.busy,
                    onChange = viewModel::setKillswitch,
                )
            }
            item {
                TransportPreferenceCard(
                    primary = state.transportPrimary,
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
                        onClick = { connect(SavedServer.of(server)) },
                    )
                }
                item { ServersFooter(state, viewModel) }
            }
        }
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
private fun StatusCard(
    state: VpnUiState,
    viewModel: VpnViewModel,
    onConnect: (SavedServer) -> Unit,
) {
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

        // The visor's own wording for what the app is doing ("Connecting",
        // "Connection failed, reconnecting") — more useful than the numeric
        // status whenever it disagrees with the label above.
        state.app?.detailedStatus
            ?.takeIf { it.isNotEmpty() && !it.equals(VpnStatus.RUNNING, ignoreCase = true) }
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
                label = stringResource(R.string.vpn_server),
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

        if (state.blocked && state.killswitch) {
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.vpn_hint_blocked),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }

        state.connection?.error?.takeIf { it.isNotEmpty() }?.let {
            Spacer(Modifier.height(8.dp))
            Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }

        listOfNotNull(state.error, state.tunnel.error).forEach { error ->
            Spacer(Modifier.height(8.dp))
            Text(
                error,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }

        Spacer(Modifier.height(16.dp))
        ConnectControl(state, viewModel, onConnect)
    }
}

@Composable
private fun ConnectControl(
    state: VpnUiState,
    viewModel: VpnViewModel,
    onConnect: (SavedServer) -> Unit,
) {
    if (!state.coreReady) {
        Text(
            stringResource(
                if (state.coreState is CoreState.Stopped) {
                    R.string.vpn_core_offline
                } else {
                    R.string.vpn_core_starting
                },
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        return
    }

    val active = state.running || state.starting || state.tunnel.established
    val pk = state.selectedPk
    Button(
        onClick = {
            if (active) {
                viewModel.disconnect()
            } else if (pk != null) {
                onConnect(state.lastServer?.takeIf { it.pk == pk } ?: SavedServer(pk))
            }
        },
        enabled = !state.busy && (active || pk != null),
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
                    pk != null -> R.string.vpn_reconnect
                    else -> R.string.connect
                },
            ),
        )
    }
    if (!active && pk == null) {
        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.vpn_pick_server),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// --- stats ---

@Composable
private fun StatsCard(state: VpnUiState) {
    val connection = state.connection

    SectionCard {
        Text(
            stringResource(R.string.vpn_stats_title),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))

        // Round-trip to the exit, as the route group measures it. Zero until
        // the first measurement lands, and showing "0 ms" would be a claim.
        connection?.latencyMs?.takeIf { it > 0 }?.let { latency ->
            InfoRow(label = stringResource(R.string.vpn_stats_ping), value = "$latency ms")
        }
        InfoRow(
            label = stringResource(R.string.vpn_stats_transferred),
            value = "↑ ${formatBytes(connection?.bandwidthSent ?: 0)}" +
                "  ↓ ${formatBytes(connection?.bandwidthReceived ?: 0)}",
        )
        connection?.takeIf { it.uploadSpeed > 0 || it.downloadSpeed > 0 }?.let {
            InfoRow(
                label = stringResource(R.string.vpn_stats_speed),
                value = "↑ ${formatBytes(it.uploadSpeed)}/s  ↓ ${formatBytes(it.downloadSpeed)}/s",
            )
        }
        connection?.connectionSeconds?.takeIf { it > 0 }?.let { seconds ->
            InfoRow(
                label = stringResource(R.string.vpn_stats_session),
                value = formatDuration(seconds),
            )
        }
        InfoRow(
            label = stringResource(R.string.vpn_stats_total),
            value = formatBytes(state.lifetimeBytes),
        )
        state.tunnel.address.takeIf { it.isNotEmpty() }?.let { address ->
            InfoRow(
                label = stringResource(R.string.vpn_stats_interface),
                value = address,
                mono = true,
            )
        }

        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.vpn_hint_excluded),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// --- killswitch ---

@Composable
private fun KillswitchCard(on: Boolean, enabled: Boolean, onChange: (Boolean) -> Unit) {
    val context = LocalContext.current
    val noSettings = stringResource(R.string.vpn_error_settings)

    SectionCard {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Text(
                stringResource(R.string.vpn_killswitch_title),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.weight(1f),
            )
            Switch(checked = on, onCheckedChange = onChange, enabled = enabled)
        }
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.vpn_killswitch_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.vpn_killswitch_always_on_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        FilledTonalButton(
            onClick = {
                // Not every build ships the VPN settings screen; a phone
                // without it says so instead of crashing.
                val opened = runCatching {
                    context.startActivity(
                        Intent(Settings.ACTION_VPN_SETTINGS)
                            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK),
                    )
                }.isSuccess
                if (!opened) Toast.makeText(context, noSettings, Toast.LENGTH_SHORT).show()
            },
            contentPadding = PaddingValues(0.dp),
        ) {
            Text(stringResource(R.string.vpn_killswitch_always_on))
        }
    }
}

// --- exit list ---

@Composable
private fun ServersHeader(state: VpnUiState, viewModel: VpnViewModel) {
    Column {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.SpaceBetween,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Text(
                stringResource(R.string.vpn_servers, state.servers.size),
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
            placeholder = { Text(stringResource(R.string.vpn_search_hint)) },
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun ServersFooter(state: VpnUiState, viewModel: VpnViewModel) {
    val serversError = state.serversError
    when {
        serversError != null -> Column {
            Text(
                serversError,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
            FilledTonalButton(onClick = viewModel::refreshServers) {
                Text(stringResource(R.string.socks_retry))
            }
        }
        state.serversLoading && state.servers.isEmpty() -> Unit
        state.filteredServers.isEmpty() -> Text(
            stringResource(
                if (state.servers.isEmpty()) R.string.vpn_servers_none else R.string.vpn_servers_empty,
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
            modifier = Modifier.fillMaxWidth(),
        )
    }
}

@Composable
private fun statusLabel(state: VpnUiState): Pair<String, Color> = when {
    !state.coreReady -> stringResource(R.string.state_disconnected) to
        MaterialTheme.colorScheme.onSurfaceVariant
    // Named only when the setting is what is holding it — the same state with
    // the killswitch off is a tunnel about to come back, not a block.
    state.blocked && state.killswitch ->
        stringResource(R.string.vpn_state_blocked) to PENDING_AMBER
    state.carrying -> stringResource(R.string.state_connected) to CONNECTED_GREEN
    state.running || state.starting ->
        stringResource(R.string.vpn_state_connecting) to PENDING_AMBER
    state.errored -> stringResource(R.string.vpn_state_error) to MaterialTheme.colorScheme.error
    else -> stringResource(R.string.state_disconnected) to
        MaterialTheme.colorScheme.onSurfaceVariant
}

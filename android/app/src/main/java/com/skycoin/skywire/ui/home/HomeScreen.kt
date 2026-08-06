package com.skycoin.skywire.ui.home

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.ExpandLess
import androidx.compose.material.icons.filled.ExpandMore
import androidx.compose.material3.Button
import androidx.compose.material3.ButtonDefaults
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
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
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.api.VisorSummary
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.ui.components.InfoRow
import com.skycoin.skywire.ui.components.formatUptime
import com.skycoin.skywire.ui.components.shortPk
import com.skycoin.skywire.ui.logs.LogSources

/**
 * Home tab: the big Connect control plus the live visor-info card. Connect
 * starts the core foreground service; the card appears once the local API
 * answers and a session is established.
 */
@Composable
fun HomeScreen(
    onOpenLogs: (String) -> Unit,
    viewModel: HomeViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current

    // The FGS notification needs POST_NOTIFICATIONS on 33+; the service runs
    // either way, so Connect proceeds whatever the user answers.
    val permissionLauncher = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { viewModel.connect() }
    val connect = {
        if (Build.VERSION.SDK_INT >= 33 &&
            ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) !=
            PackageManager.PERMISSION_GRANTED
        ) {
            permissionLauncher.launch(Manifest.permission.POST_NOTIFICATIONS)
        } else {
            viewModel.connect()
        }
    }

    Column(
        modifier = Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(24.dp),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Spacer(Modifier.height(24.dp))
        StatusLine(state)
        Spacer(Modifier.height(20.dp))
        ConnectButton(state, onConnect = connect, onDisconnect = viewModel::disconnect)
        Spacer(Modifier.height(16.dp))
        StatusCaption(state, onOpenLogs)
        state.summary?.let { summary ->
            Spacer(Modifier.height(24.dp))
            VisorInfoCard(state, summary, onOpenLogs)
        }
        Spacer(Modifier.height(24.dp))
    }
}

@Composable
private fun StatusLine(state: HomeUiState) {
    val core = state.coreState
    val (label, color) = when {
        state.connected -> stringResource(R.string.state_connected) to Color(0xFF16A34A)
        core is CoreState.Starting || core is CoreState.Running ->
            stringResource(R.string.state_starting) to Color(0xFFF59E0B)
        core is CoreState.Restarting ->
            stringResource(R.string.state_restarting, core.nextAttempt) to Color(0xFFF59E0B)
        core is CoreState.Stopping ->
            stringResource(R.string.state_stopping) to MaterialTheme.colorScheme.onSurfaceVariant
        core is CoreState.Failed ->
            stringResource(R.string.home_error_start) to MaterialTheme.colorScheme.error
        else ->
            stringResource(R.string.state_disconnected) to MaterialTheme.colorScheme.onSurfaceVariant
    }
    Row(verticalAlignment = Alignment.CenterVertically) {
        Box(
            Modifier
                .size(10.dp)
                .clip(CircleShape)
                .background(color),
        )
        Spacer(Modifier.width(8.dp))
        Text(label, style = MaterialTheme.typography.titleMedium)
    }
}

@Composable
private fun ConnectButton(
    state: HomeUiState,
    onConnect: () -> Unit,
    onDisconnect: () -> Unit,
) {
    val core = state.coreState
    // Disabled only for the brief transitional phases. While the core runs
    // (even before the API answers) or crash-loops, the button stays live as
    // Disconnect — the user must always be able to stop the core.
    val busy = core is CoreState.Starting || core is CoreState.Stopping
    val showDisconnect = core is CoreState.Running || core is CoreState.Restarting

    Button(
        onClick = { if (showDisconnect) onDisconnect() else onConnect() },
        enabled = !busy,
        shape = CircleShape,
        colors = if (showDisconnect) {
            ButtonDefaults.buttonColors(
                containerColor = MaterialTheme.colorScheme.secondaryContainer,
                contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
            )
        } else {
            ButtonDefaults.buttonColors()
        },
        modifier = Modifier.size(180.dp),
    ) {
        when {
            busy -> CircularProgressIndicator(
                modifier = Modifier.size(44.dp),
                color = MaterialTheme.colorScheme.primary,
                strokeWidth = 4.dp,
            )
            showDisconnect && !state.connected -> Column(
                horizontalAlignment = Alignment.CenterHorizontally,
            ) {
                CircularProgressIndicator(
                    modifier = Modifier.size(28.dp),
                    color = MaterialTheme.colorScheme.primary,
                    strokeWidth = 3.dp,
                )
                Spacer(Modifier.height(10.dp))
                Text(
                    stringResource(R.string.disconnect),
                    style = MaterialTheme.typography.titleMedium,
                    textAlign = TextAlign.Center,
                )
            }
            else -> Text(
                stringResource(if (showDisconnect) R.string.disconnect else R.string.connect),
                style = MaterialTheme.typography.titleLarge,
                textAlign = TextAlign.Center,
            )
        }
    }
}

@Composable
private fun StatusCaption(state: HomeUiState, onOpenLogs: (String) -> Unit) {
    val core = state.coreState
    val error = state.error
    when {
        core is CoreState.Failed -> {
            Text(
                core.message,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.Center,
            )
            TextButton(onClick = { onOpenLogs(LogSources.PROCESS) }) {
                Text(stringResource(R.string.view_logs))
            }
        }
        core is CoreState.Stopped ->
            Text(
                stringResource(R.string.home_hint_disconnected),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
        core is CoreState.Stopping -> Unit
        // Startup can take minutes on a slow network (dmsg discovery retries),
        // so give the user something to watch instead of a bare spinner.
        !state.connected -> {
            Text(
                stringResource(R.string.home_hint_starting),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                textAlign = TextAlign.Center,
            )
            TextButton(onClick = { onOpenLogs(LogSources.PROCESS) }) {
                Text(stringResource(R.string.view_logs))
            }
        }
        error != null ->
            Text(
                error,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
                textAlign = TextAlign.Center,
            )
    }
}

@Composable
private fun VisorInfoCard(
    state: HomeUiState,
    summary: VisorSummary,
    onOpenLogs: (String) -> Unit,
) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copiedText = stringResource(R.string.copied_to_clipboard)
    val pk = summary.overview.localPk
    var expanded by rememberSaveable { mutableStateOf(false) }

    Card(
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
        ),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(20.dp)) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.SpaceBetween,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    stringResource(R.string.visor_info_title),
                    style = MaterialTheme.typography.titleMedium,
                )
                TextButton(onClick = { onOpenLogs(LogSources.CORE) }) {
                    Text(stringResource(R.string.view_logs))
                }
            }
            Spacer(Modifier.height(4.dp))

            InfoRow(
                label = stringResource(R.string.visor_public_key),
                value = shortPk(pk),
                mono = true,
                modifier = Modifier.clickable {
                    clipboard.setText(AnnotatedString(pk))
                    Toast.makeText(context, copiedText, Toast.LENGTH_SHORT).show()
                },
            )
            InfoRow(
                label = stringResource(R.string.visor_version),
                value = listOfNotNull(
                    summary.overview.buildInfo?.version?.takeIf { it.isNotEmpty() },
                    summary.buildTag.takeIf { it.isNotEmpty() },
                ).joinToString(" · ").ifEmpty { "—" },
            )
            InfoRow(
                label = stringResource(R.string.visor_uptime),
                value = formatUptime(summary.uptime),
            )
            // Everything above is what someone checks at a glance — am I who I
            // think I am, on what build, for how long. What follows is
            // diagnostics: real when you need it, and a wall of keys and health
            // rows when you don't. Collapsed by default, one tap away.
            if (expanded) {
                SectionDivider()
                Text(
                    stringResource(R.string.visor_transports),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                val transports = summary.overview.transports
                if (transports.isEmpty()) {
                    Text("—", style = MaterialTheme.typography.bodyMedium)
                } else {
                    // Grouped by carrier rather than listed one per remote: on
                    // a phone the interesting fact is WHICH carriers came up
                    // (dmsg always, stcpr/sudph almost never behind carrier
                    // NAT), and a bare total said nothing about that.
                    transports
                        .groupBy { it.type.ifEmpty { "?" } }
                        .toSortedMap()
                        .forEach { (type, entries) ->
                            InfoRow(
                                label = type,
                                value = entries.size.toString(),
                            )
                        }
                    InfoRow(
                        label = stringResource(R.string.visor_transports_total),
                        value = transports.size.toString(),
                    )
                }

                SectionDivider()
                Text(
                    stringResource(R.string.visor_dmsg_servers),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                val dmsgServers = summary.dmsgServers
                if (dmsgServers.isEmpty()) {
                    Text("—", style = MaterialTheme.typography.bodyMedium)
                } else {
                    dmsgServers.take(MAX_DMSG_ROWS).forEach { server ->
                        // How the session is carried, always — plus the latency
                        // when there is one. The visor measures that with a
                        // self-ping through each server at startup and then
                        // hourly, so a server that joined since the last pass
                        // (or whose ping did not come back) has none, and every
                        // row would otherwise read a bare, false "0 ms".
                        InfoRow(
                            label = shortPk(server.pk),
                            value = listOfNotNull(
                                server.protocol.ifEmpty { server.carrier }
                                    .takeIf { it.isNotEmpty() },
                                server.latencyNs.takeIf { it > 0 }
                                    ?.let { "${it / 1_000_000} ms" },
                            ).joinToString(" · ").ifEmpty { "—" },
                            mono = true,
                        )
                    }
                }

                SectionDivider()
                Text(
                    stringResource(R.string.visor_service_health),
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(4.dp))
                if (state.serviceHealth.isEmpty()) {
                    Text("—", style = MaterialTheme.typography.bodyMedium)
                } else {
                    state.serviceHealth.forEach { entry ->
                        InfoRow(
                            label = entry.name,
                            value = entry.status.ifEmpty { entry.error.ifEmpty { "?" } },
                            valueColor = if (entry.status.equals("healthy", ignoreCase = true)) {
                                Color(0xFF16A34A)
                            } else {
                                MaterialTheme.colorScheme.onSurfaceVariant
                            },
                        )
                    }
                }
            }

            Spacer(Modifier.height(4.dp))
            TextButton(
                onClick = { expanded = !expanded },
                modifier = Modifier.align(Alignment.CenterHorizontally),
            ) {
                Text(
                    stringResource(
                        if (expanded) R.string.visor_show_less else R.string.visor_show_more,
                    ),
                )
                Icon(
                    imageVector = if (expanded) Icons.Default.ExpandLess
                    else Icons.Default.ExpandMore,
                    contentDescription = null,
                )
            }
        }
    }
}

@Composable
private fun SectionDivider() {
    HorizontalDivider(
        modifier = Modifier.padding(vertical = 10.dp),
        color = MaterialTheme.colorScheme.surfaceContainerHighest,
    )
}

/** The visor keeps a handful of dmsg sessions; the card shows the first few. */
private const val MAX_DMSG_ROWS = 4

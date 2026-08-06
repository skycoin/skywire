package com.skycoin.skywire.ui.fleet

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
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material.icons.filled.Refresh
import androidx.compose.material.icons.outlined.Edit
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
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
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.api.VisorSummary
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.ui.components.CONNECTED_GREEN
import com.skycoin.skywire.ui.components.InfoRow
import com.skycoin.skywire.ui.components.SectionCard
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.formatDuration
import com.skycoin.skywire.ui.components.formatUptime
import com.skycoin.skywire.ui.components.shortPk
import com.skycoin.skywire.ui.logs.LogSources
import java.time.Instant

/**
 * Fleet: the visors this user runs elsewhere, seen from the phone.
 *
 * Off until asked for. With the toggle off the phone serves no dmsg ingest at
 * all and there is nothing to list; turning it on makes this key reachable as
 * a status endpoint, and any visor carrying it in its own `hypervisors` list
 * connects in. What arrives is status — and one action, Restart. Everything
 * else the hypervisor API can do to a visor is deliberately not here.
 */
@Composable
fun FleetScreen(
    onBack: () -> Unit,
    onOpenLogs: (String) -> Unit,
    viewModel: FleetViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    var confirmToggle by remember { mutableStateOf<Boolean?>(null) }
    var confirmRestart by remember { mutableStateOf<String?>(null) }
    var addVisorOpen by remember { mutableStateOf(false) }
    var renaming by remember { mutableStateOf<String?>(null) }
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(state.message) {
        state.message?.let { message ->
            snackbar.showSnackbar(message)
            viewModel.messageShown()
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_fleet),
                onBack = onBack,
                // Fleet's help is a sheet of its own (see AddVisorSheet): it
                // ends in a command with a copy button, which a plain help
                // dialog has nowhere to put.
                actions = {
                    IconButton(onClick = { addVisorOpen = true }) {
                        Icon(
                            Icons.AutoMirrored.Outlined.HelpOutline,
                            contentDescription = stringResource(R.string.help_open),
                        )
                    }
                },
            )
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item {
                EnableCard(
                    state = state,
                    onToggle = { wanted ->
                        // A running core has to go down and come back for the
                        // change to land, and that drops any SkySOCKS or SkyVPN
                        // connection with it — worth a question first.
                        if (state.coreState is CoreState.Stopped ||
                            state.coreState is CoreState.Failed
                        ) {
                            viewModel.setEnabled(wanted)
                        } else {
                            confirmToggle = wanted
                        }
                    },
                )
            }

            if (!state.enabled) {
                item { WhatFleetIsCard() }
                return@LazyColumn
            }

            if (!state.coreReady) {
                item { CoreNotReadyNote(state) }
                return@LazyColumn
            }

            item { VisorsHeader(state, viewModel) }
            items(state.visors, key = { it.overview.localPk }) { visor ->
                VisorCard(
                    visor = visor,
                    name = state.names[visor.overview.localPk].orEmpty(),
                    restarting = state.restarting == visor.overview.localPk,
                    onRename = { renaming = visor.overview.localPk },
                    onRestart = { confirmRestart = visor.overview.localPk },
                    onOpenLogs = { onOpenLogs(LogSources.visor(visor.overview.localPk)) },
                )
            }
            item { VisorsFooter(state) }
        }
    }

    if (addVisorOpen) {
        AddVisorSheet(state, onDismiss = { addVisorOpen = false })
    }

    renaming?.let { pk ->
        RenameDialog(
            pk = pk,
            current = state.names[pk].orEmpty(),
            onDismiss = { renaming = null },
            onSave = { name ->
                viewModel.rename(pk, name)
                renaming = null
            },
        )
    }

    confirmToggle?.let { wanted ->
        ConfirmDialog(
            title = stringResource(
                if (wanted) R.string.fleet_confirm_on_title else R.string.fleet_confirm_off_title,
            ),
            body = stringResource(R.string.fleet_confirm_restart_body),
            confirm = stringResource(R.string.fleet_confirm_restart_action),
            onDismiss = { confirmToggle = null },
            onConfirm = {
                viewModel.setEnabled(wanted)
                confirmToggle = null
            },
        )
    }

    confirmRestart?.let { pk ->
        // Ask about the machine by the name the user gave it — a dialog that
        // quotes a key back at someone who named it "home server" is asking
        // them to re-do the identification they already did.
        val label = state.names[pk]?.takeIf { it.isNotEmpty() } ?: shortPk(pk)
        ConfirmDialog(
            title = stringResource(R.string.fleet_restart),
            body = stringResource(R.string.fleet_restart_confirm, label),
            confirm = stringResource(R.string.fleet_restart),
            onDismiss = { confirmRestart = null },
            onConfirm = {
                viewModel.restartVisor(pk, label)
                confirmRestart = null
            },
        )
    }
}

// --- the toggle ---

@Composable
private fun EnableCard(state: FleetUiState, onToggle: (Boolean) -> Unit) {
    SectionCard {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Column(Modifier.weight(1f)) {
                Text(
                    stringResource(R.string.fleet_enable),
                    style = MaterialTheme.typography.titleMedium,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    stringResource(R.string.fleet_enable_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(12.dp))
            Switch(
                checked = state.enabled,
                onCheckedChange = onToggle,
                // The switch writes a config field and restarts the core; both
                // are meaningless while the core is already on its way up or
                // down, and a second flip mid-restart would queue another one.
                enabled = !state.coreCycling,
            )
        }
        if (state.coreCycling) {
            Spacer(Modifier.height(10.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(10.dp))
                Text(
                    stringResource(R.string.fleet_core_restarting),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        state.error?.let { error ->
            Spacer(Modifier.height(8.dp))
            Text(
                error,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.error,
            )
        }
    }
}

/** The off state: what this is for, and what turning it on actually does. */
@Composable
private fun WhatFleetIsCard() {
    SectionCard {
        Text(
            stringResource(R.string.fleet_off_title),
            style = MaterialTheme.typography.titleMedium,
        )
        Spacer(Modifier.height(8.dp))
        Text(
            stringResource(R.string.fleet_off_body),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        Text(
            stringResource(R.string.fleet_off_reachable),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// --- adding a visor ---

/**
 * How to point a visor at this phone. Instructions read once and then never
 * again, so they live behind the app bar's ? instead of holding a card open
 * above the list forever.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun AddVisorSheet(state: FleetUiState, onDismiss: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copied = stringResource(R.string.copied_to_clipboard)
    val pk = state.localPk

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(),
    ) {
        Column(
            Modifier
                .navigationBarsPadding()
                .padding(horizontal = 24.dp)
                .padding(bottom = 24.dp),
        ) {
            // Fleet's ? is the only one that opens a sheet rather than a
            // dialog, because unlike the other tabs its guidance ends in a
            // command the user has to copy. It answers the same question
            // first — what is this tab — before getting to the command.
            Text(
                stringResource(R.string.help_fleet_title),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.help_fleet_body),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(20.dp))
            Text(
                stringResource(R.string.fleet_add_title),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.fleet_add_body),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))

            if (pk.isEmpty()) {
                Text(
                    stringResource(R.string.fleet_add_pk_pending),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                return@Column
            }

            InfoRow(
                label = stringResource(R.string.fleet_this_phone),
                value = shortPk(pk),
                mono = true,
                modifier = Modifier.clickable {
                    clipboard.setText(AnnotatedString(pk))
                    Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                },
            )
            Spacer(Modifier.height(10.dp))
            // The whole command with the key already in it — the point is that
            // it can be copied once and pasted on the other machine, not read.
            val command = stringResource(R.string.fleet_add_command, pk)
            Text(
                command,
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                modifier = Modifier
                    .fillMaxWidth()
                    .clip(RoundedCornerShape(10.dp))
                    .background(MaterialTheme.colorScheme.surfaceVariant)
                    .clickable {
                        clipboard.setText(AnnotatedString(command))
                        Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                    }
                    .padding(horizontal = 12.dp, vertical = 10.dp),
            )
            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(R.string.fleet_add_tap_to_copy),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(12.dp))
            Text(
                stringResource(R.string.fleet_add_then_restart),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

// --- the list ---

@Composable
private fun CoreNotReadyNote(state: FleetUiState) {
    SectionCard {
        Text(
            stringResource(
                if (state.coreState is CoreState.Stopped || state.coreState is CoreState.Failed) {
                    R.string.fleet_core_offline
                } else {
                    R.string.fleet_core_starting
                },
            ),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun VisorsHeader(state: FleetUiState, viewModel: FleetViewModel) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.SpaceBetween,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Text(
            stringResource(R.string.fleet_visors, state.visors.size),
            style = MaterialTheme.typography.titleMedium,
        )
        if (state.loading) {
            CircularProgressIndicator(Modifier.size(20.dp), strokeWidth = 2.dp)
        } else {
            IconButton(onClick = viewModel::refresh) {
                Icon(
                    Icons.Default.Refresh,
                    contentDescription = stringResource(R.string.fleet_refresh),
                )
            }
        }
    }
}

@Composable
private fun VisorsFooter(state: FleetUiState) {
    if (state.visors.isNotEmpty() || state.loading) return
    Text(
        stringResource(R.string.fleet_visors_none),
        style = MaterialTheme.typography.bodyMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        textAlign = TextAlign.Center,
        modifier = Modifier.fillMaxWidth(),
    )
}

@Composable
private fun VisorCard(
    visor: VisorSummary,
    name: String,
    restarting: Boolean,
    onRename: () -> Unit,
    onRestart: () -> Unit,
    onOpenLogs: () -> Unit,
) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copied = stringResource(R.string.copied_to_clipboard)
    val pk = visor.overview.localPk

    SectionCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Box(
                Modifier
                    .size(10.dp)
                    .clip(CircleShape)
                    .background(
                        if (visor.online) CONNECTED_GREEN
                        else MaterialTheme.colorScheme.onSurfaceVariant,
                    ),
            )
            Spacer(Modifier.width(8.dp))
            val copyPk = Modifier.clickable {
                clipboard.setText(AnnotatedString(pk))
                Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
            }
            Column(Modifier.weight(1f)) {
                // The name the user gave this machine leads; an unnamed one is
                // its key, which is all there is to call it by.
                Text(
                    name.ifEmpty { shortPk(pk) },
                    style = if (name.isEmpty()) {
                        MaterialTheme.typography.titleMedium.copy(fontFamily = FontFamily.Monospace)
                    } else {
                        MaterialTheme.typography.titleMedium
                    },
                    modifier = if (name.isEmpty()) copyPk else Modifier,
                )
                Text(
                    stringResource(
                        if (visor.online) R.string.state_connected else R.string.fleet_state_offline,
                    ),
                    style = MaterialTheme.typography.bodySmall,
                    color = if (visor.online) CONNECTED_GREEN
                    else MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            if (restarting) {
                CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(8.dp))
            }
            IconButton(onClick = onRename, modifier = Modifier.size(32.dp)) {
                Icon(
                    Icons.Outlined.Edit,
                    contentDescription = stringResource(R.string.fleet_rename),
                    modifier = Modifier.size(18.dp),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }

        // A named row still has to show the key somewhere — it is what the
        // other machine's config holds, and what a support question quotes.
        // An unnamed row already IS the key, above.
        if (name.isNotEmpty()) {
            Spacer(Modifier.height(4.dp))
            Text(
                shortPk(pk),
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.clickable {
                    clipboard.setText(AnnotatedString(pk))
                    Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                },
            )
        }

        Spacer(Modifier.height(8.dp))
        InfoRow(
            label = stringResource(R.string.visor_version),
            value = listOfNotNull(
                visor.overview.buildInfo?.version?.takeIf { it.isNotEmpty() },
                visor.buildTag.takeIf { it.isNotEmpty() },
            ).joinToString(" · ").ifEmpty { "—" },
        )
        InfoRow(
            label = stringResource(R.string.visor_uptime),
            value = formatUptime(visor.uptime),
        )
        TransportRows(visor)
        InfoRow(
            label = stringResource(R.string.fleet_health),
            value = healthLabel(visor),
            valueColor = healthColor(visor),
        )
        // How old the numbers above actually are. Shown whenever they are not
        // current — which includes rows that say Connected: the server keeps
        // serving a visor from its last snapshot for three minutes after the
        // RPC connection drops, on the reasoning that a peer redialing is not
        // a peer that is down. True, but it means an uptime can be minutes
        // stale under a green dot, and it is the same gap in which Restart
        // answers "currently disconnected". Saying the age costs one line.
        val staleFor = visor.lastSeenAt?.let(::secondsSince)
        if (staleFor != null && (!visor.online || staleFor >= STALE_AFTER_S)) {
            Spacer(Modifier.height(4.dp))
            Text(
                stringResource(R.string.fleet_last_seen, formatDuration(staleFor)),
                style = MaterialTheme.typography.labelSmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }

        Spacer(Modifier.height(8.dp))
        Row(
            horizontalArrangement = Arrangement.End,
            modifier = Modifier.fillMaxWidth(),
        ) {
            TextButton(onClick = onOpenLogs, enabled = visor.online) {
                Text(stringResource(R.string.logs_title))
            }
            TextButton(onClick = onRestart, enabled = visor.online && !restarting) {
                Text(stringResource(R.string.fleet_restart))
            }
        }
    }
}

/**
 * Transports per carrier, not one total. Which types came up is the whole
 * diagnosis on a visor that is reachable but unroutable — three dmsg relays
 * and no stcpr says something a bare "3" does not — so each type gets its own
 * row, with the total under them.
 */
@Composable
private fun TransportRows(visor: VisorSummary) {
    val transports = visor.overview.transports
    if (transports.isEmpty()) {
        InfoRow(label = stringResource(R.string.visor_transports), value = "—")
        return
    }
    Spacer(Modifier.height(4.dp))
    Text(
        stringResource(R.string.visor_transports),
        style = MaterialTheme.typography.labelMedium,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    transports
        .groupBy { it.type.ifEmpty { "?" } }
        .toSortedMap()
        .forEach { (type, entries) ->
            InfoRow(label = "   $type", value = entries.size.toString())
        }
    InfoRow(
        label = "   " + stringResource(R.string.visor_transports_total),
        value = transports.size.toString(),
    )
    Spacer(Modifier.height(4.dp))
}

// --- small shared pieces ---

/** Name this visor on this phone, or clear the name by emptying the field. */
@Composable
private fun RenameDialog(
    pk: String,
    current: String,
    onDismiss: () -> Unit,
    onSave: (String) -> Unit,
) {
    var text by remember { mutableStateOf(current) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.fleet_rename)) },
        text = {
            Column {
                Text(
                    stringResource(R.string.fleet_rename_hint, shortPk(pk)),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it.take(MAX_NAME) },
                    singleLine = true,
                    label = { Text(stringResource(R.string.fleet_rename_label)) },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(onClick = { onSave(text) }) { Text(stringResource(R.string.save)) }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
        },
    )
}

@Composable
private fun ConfirmDialog(
    title: String,
    body: String,
    confirm: String,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = { Text(body) },
        confirmButton = { TextButton(onClick = onConfirm) { Text(confirm) } },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) } },
    )
}

/**
 * One word for the visor's health, since the card has one row for it: the
 * services check is the one that says whether the visor can reach the
 * deployment at all, and it is the only field always populated.
 */
@Composable
private fun healthLabel(visor: VisorSummary): String {
    val health = visor.health?.servicesHealth.orEmpty()
    return when {
        !visor.online -> "—"
        health.isEmpty() -> stringResource(R.string.fleet_health_unknown)
        else -> health
    }
}

@Composable
private fun healthColor(visor: VisorSummary): Color = when {
    !visor.online -> MaterialTheme.colorScheme.onSurfaceVariant
    visor.health?.servicesHealth.equals(HEALTHY, ignoreCase = true) -> CONNECTED_GREEN
    else -> MaterialTheme.colorScheme.onSurfaceVariant
}

/**
 * Seconds since an RFC 3339 instant, or null if it will not parse. The
 * timestamp is stamped by this phone's own visor, not the remote one, so the
 * phone's clock is the right thing to measure it against — no skew to worry
 * about. Clamped at zero rather than rendering a negative age.
 */
private fun secondsSince(rfc3339: String): Long? = runCatching {
    (System.currentTimeMillis() - Instant.parse(rfc3339).toEpochMilli()).coerceAtLeast(0) / 1000
}.getOrNull()

private const val HEALTHY = "healthy"

/**
 * How old a snapshot has to be before the card says so. Comfortably past one
 * poll interval, so a row that is simply between refreshes stays quiet.
 */
private const val STALE_AFTER_S = 45L

/** Matches [com.skycoin.skywire.core.VisorNames]'s own cap. */
private const val MAX_NAME = 40

package com.skycoin.skywire.ui.settings

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
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
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.core.CoreLogLevel
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.ui.components.SectionCard
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.appLabel
import com.skycoin.skywire.ui.logs.LogSources
import java.util.Locale

/**
 * Logs & diagnostics — where the logs are *collected*, not where they are
 * read.
 *
 * The per-screen `Logs` buttons are the ones you reach for while something is
 * failing, because you are already on the screen that is failing. This is the
 * aggregate: every source the phone has, whether or not its screen is open;
 * one file with all of them in it, for handing to someone else; and the one
 * setting that decides how much there is to hand over.
 */
@Composable
fun DiagnosticsScreen(
    onBack: () -> Unit,
    onOpenLogs: (String) -> Unit,
    viewModel: DiagnosticsViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    var confirmLevel by remember { mutableStateOf<String?>(null) }

    LaunchedEffect(state.message) {
        state.message?.let { message ->
            snackbar.showSnackbar(message)
            viewModel.messageShown()
        }
    }

    val exportPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.CreateDocument("application/zip"),
    ) { uri -> uri?.let(viewModel::export) }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(title = stringResource(R.string.diag_title), onBack = onBack) },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item { SourcesCard(viewModel.apps, onOpenLogs) }
            item {
                ExportCard(
                    exporting = state.exporting,
                    onExport = { exportPicker.launch(exportFileName()) },
                )
            }
            item {
                LogLevelCard(
                    state = state,
                    onPick = { level ->
                        // A level change restarts the core, and that drops any
                        // SkySOCKS or SkyVPN connection with it.
                        if (state.coreState is CoreState.Stopped ||
                            state.coreState is CoreState.Failed
                        ) {
                            viewModel.setLogLevel(level)
                        } else {
                            confirmLevel = level
                        }
                    },
                )
            }
        }
    }

    confirmLevel?.let { level ->
        AlertDialog(
            onDismissRequest = { confirmLevel = null },
            title = { Text(stringResource(R.string.diag_level_confirm_title, levelLabel(level))) },
            text = { Text(stringResource(R.string.diag_level_confirm_body)) },
            confirmButton = {
                TextButton(
                    onClick = {
                        viewModel.setLogLevel(level)
                        confirmLevel = null
                    },
                ) {
                    Text(stringResource(R.string.fleet_confirm_restart_action))
                }
            },
            dismissButton = {
                TextButton(onClick = { confirmLevel = null }) {
                    Text(stringResource(R.string.cancel))
                }
            },
        )
    }
}

@Composable
private fun SourcesCard(apps: List<String>, onOpenLogs: (String) -> Unit) {
    SectionCard {
        Text(stringResource(R.string.diag_sources), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.diag_sources_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(8.dp))

        SourceRow(
            title = stringResource(R.string.logs_source_core),
            subtitle = stringResource(R.string.diag_source_core_hint),
            onClick = { onOpenLogs(LogSources.CORE) },
        )
        HorizontalDivider()
        SourceRow(
            title = stringResource(R.string.logs_source_process),
            subtitle = stringResource(R.string.diag_source_process_hint),
            onClick = { onOpenLogs(LogSources.PROCESS) },
        )
        apps.forEach { app ->
            HorizontalDivider()
            SourceRow(
                // Both names: the product the user opened, and the process the
                // log lines and the API route are keyed on. See [appLabel].
                title = appLabel(app),
                subtitle = stringResource(R.string.diag_source_app_hint),
                onClick = { onOpenLogs(LogSources.app(app)) },
            )
        }
    }
}

@Composable
private fun SourceRow(title: String, subtitle: String, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 12.dp),
    ) {
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.bodyLarge)
            Text(
                subtitle,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(
            Icons.AutoMirrored.Filled.KeyboardArrowRight,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun ExportCard(exporting: Boolean, onExport: () -> Unit) {
    SectionCard {
        Text(stringResource(R.string.diag_export), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.diag_export_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        if (exporting) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(10.dp))
                Text(
                    stringResource(R.string.diag_exporting),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        } else {
            OutlinedButton(onClick = onExport) {
                Text(stringResource(R.string.diag_export_action))
            }
        }
    }
}

@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LogLevelCard(state: DiagnosticsUiState, onPick: (String) -> Unit) {
    SectionCard {
        Text(stringResource(R.string.diag_level), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.diag_level_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        // Wrapping, not scrolling: five chips do not fit a phone's width, and
        // a level the user cannot see is a level they will not find.
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalArrangement = Arrangement.spacedBy(6.dp),
        ) {
            CoreLogLevel.LEVELS.forEach { level ->
                FilterChip(
                    selected = state.logLevel == level,
                    onClick = { onPick(level) },
                    enabled = !state.coreCycling,
                    label = {
                        Text(levelLabel(level), style = MaterialTheme.typography.labelSmall)
                    },
                )
            }
        }
    }
}

private fun levelLabel(level: String): String = level.uppercase(Locale.US)

/**
 * A name that sorts and identifies: several bundles from the same phone end up
 * in the same Downloads folder, and "which one was the failing run" is the
 * question they are collected to answer.
 */
private fun exportFileName(): String {
    val stamp = java.text.SimpleDateFormat("yyyyMMdd-HHmmss", Locale.US)
        .format(java.util.Date())
    return "skywire-diagnostics-$stamp.zip"
}

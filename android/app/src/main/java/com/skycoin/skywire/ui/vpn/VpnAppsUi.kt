package com.skycoin.skywire.ui.vpn

import androidx.compose.foundation.Image
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.produceState
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.ImageBitmap
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.graphics.drawable.toBitmap
import com.skycoin.skywire.R
import com.skycoin.skywire.core.InstalledApp
import com.skycoin.skywire.core.VpnAppMode
import com.skycoin.skywire.core.VpnAppRouting
import com.skycoin.skywire.ui.components.SectionCard
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext

/**
 * Which apps the tunnel takes: all of them, only the chosen ones, or all but
 * the chosen ones. The phone-wide default slows down apps that gain nothing
 * from the tunnel; the other two let the user say which apps do.
 */
@Composable
internal fun AppRoutingCard(
    routing: VpnAppRouting,
    enabled: Boolean,
    onMode: (VpnAppMode) -> Unit,
    onChooseApps: () -> Unit,
) {
    SectionCard {
        Text(stringResource(R.string.vpn_apps_title), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        VpnAppMode.entries.forEach { mode ->
            val select = { if (enabled && mode != routing.mode) onMode(mode) }
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier
                    .fillMaxWidth()
                    .clickable(enabled = enabled, onClick = select)
                    .padding(vertical = 4.dp),
            ) {
                RadioButton(selected = mode == routing.mode, onClick = select, enabled = enabled)
                Spacer(Modifier.width(8.dp))
                Column(Modifier.weight(1f)) {
                    Text(stringResource(modeLabel(mode)), style = MaterialTheme.typography.bodyLarge)
                    Text(
                        stringResource(modeHint(mode)),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        }
        if (routing.mode != VpnAppMode.ALL) {
            Spacer(Modifier.height(8.dp))
            val chosen = routing.selected.size
            Text(
                if (chosen == 0 && routing.mode == VpnAppMode.ONLY) {
                    stringResource(R.string.vpn_apps_none)
                } else {
                    pluralStringResource(R.plurals.vpn_apps_count, chosen, chosen)
                },
                style = MaterialTheme.typography.bodySmall,
                color = if (!routing.usable) MaterialTheme.colorScheme.error
                else MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(8.dp))
            FilledTonalButton(onClick = onChooseApps, enabled = enabled) {
                Text(stringResource(R.string.vpn_apps_choose))
            }
        }
    }
}

private fun modeLabel(mode: VpnAppMode): Int = when (mode) {
    VpnAppMode.ALL -> R.string.vpn_apps_mode_all
    VpnAppMode.ONLY -> R.string.vpn_apps_mode_only
    VpnAppMode.EXCEPT -> R.string.vpn_apps_mode_except
}

private fun modeHint(mode: VpnAppMode): Int = when (mode) {
    VpnAppMode.ALL -> R.string.vpn_apps_mode_all_hint
    VpnAppMode.ONLY -> R.string.vpn_apps_mode_only_hint
    VpnAppMode.EXCEPT -> R.string.vpn_apps_mode_except_hint
}

/**
 * The apps to choose from, with a search box and a tick per app. Nothing is
 * saved until Done — dismissing the sheet leaves the choice as it was.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
internal fun AppPickerSheet(
    mode: VpnAppMode,
    apps: List<InstalledApp>?,
    initial: Set<String>,
    onDismiss: () -> Unit,
    onDone: (Set<String>) -> Unit,
) {
    val sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true)
    var chosen by remember(initial) { mutableStateOf(initial) }
    var query by rememberSaveable { mutableStateOf("") }

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                .fillMaxHeight(0.92f)
                .navigationBarsPadding()
                .padding(horizontal = 24.dp),
        ) {
            // Done at the top, where it is reachable without scrolling a list
            // that can run to a hundred apps.
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    stringResource(
                        if (mode == VpnAppMode.EXCEPT) R.string.vpn_apps_picker_except
                        else R.string.vpn_apps_picker_only,
                    ),
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.weight(1f),
                )
                Button(
                    onClick = {
                        // Only apps still installed: one removed since it was
                        // chosen would otherwise count without being listed.
                        val installed = apps?.map { it.packageName }?.toSet()
                        onDone(if (installed == null) chosen else chosen intersect installed)
                    },
                ) { Text(stringResource(R.string.vpn_apps_done)) }
            }
            Spacer(Modifier.height(4.dp))
            Text(
                pluralStringResource(R.plurals.vpn_apps_count, chosen.size, chosen.size),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(8.dp))
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                singleLine = true,
                placeholder = { Text(stringResource(R.string.vpn_apps_search)) },
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(8.dp))
            when {
                apps == null -> Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
                    CircularProgressIndicator()
                }
                else -> {
                    val shown = apps.filter {
                        query.isBlank() ||
                            it.label.contains(query, true) ||
                            it.packageName.contains(query, true)
                    }
                    if (shown.isEmpty()) {
                        Text(
                            stringResource(R.string.vpn_apps_empty),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            textAlign = TextAlign.Center,
                            modifier = Modifier.fillMaxWidth().padding(24.dp),
                        )
                    }
                    LazyColumn(
                        modifier = Modifier.weight(1f),
                        verticalArrangement = Arrangement.spacedBy(2.dp),
                    ) {
                        items(shown, key = { it.packageName }) { app ->
                            val on = app.packageName in chosen
                            AppRow(app, checked = on) {
                                chosen = if (on) chosen - app.packageName else chosen + app.packageName
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun AppRow(app: InstalledApp, checked: Boolean, onToggle: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onToggle)
            .padding(vertical = 6.dp),
    ) {
        AppIcon(app.packageName)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(app.label, style = MaterialTheme.typography.bodyLarge, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(
                app.packageName,
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Checkbox(checked = checked, onCheckedChange = { onToggle() })
    }
}

/** The app's launcher icon, loaded off the main thread as the row appears. */
@Composable
private fun AppIcon(packageName: String) {
    val context = LocalContext.current
    val icon by produceState<ImageBitmap?>(null, packageName) {
        value = withContext(Dispatchers.IO) {
            runCatching {
                context.packageManager.getApplicationIcon(packageName)
                    .toBitmap(ICON_PX, ICON_PX)
                    .asImageBitmap()
            }.getOrNull()
        }
    }
    Box(Modifier.size(36.dp), contentAlignment = Alignment.Center) {
        icon?.let { Image(it, contentDescription = null, modifier = Modifier.size(36.dp)) }
    }
}

private const val ICON_PX = 96

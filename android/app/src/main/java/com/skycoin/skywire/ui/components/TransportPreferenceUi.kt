package com.skycoin.skywire.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.core.TransportPreference

/**
 * The primary-transport control, shared by the app screens that build
 * routes to a remote server — SkySOCKS today, SkyVPN next. The value is a
 * visor-wide setting ([TransportPreference]), so both screens show and
 * change the same thing.
 */
@Composable
fun TransportPreferenceCard(
    primary: String,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    Card(
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            // Cards hold real prose: content must default to ink, not the
            // muted onSurfaceVariant this container would otherwise imply.
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(20.dp)) {
            Text(
                stringResource(R.string.transport_title),
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(6.dp))
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    transportName(primary),
                    style = MaterialTheme.typography.titleMedium,
                    modifier = Modifier.weight(1f),
                )
                FilledTonalButton(onClick = onClick, enabled = enabled) {
                    Text(stringResource(R.string.transport_change))
                }
            }
            Spacer(Modifier.height(4.dp))
            Text(
                stringResource(R.string.transport_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * Picking a row applies it and closes the sheet, so there is no Save (and
 * no Cancel — the scrim, the swipe and the back gesture all dismiss it
 * without choosing).
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun TransportPreferenceSheet(
    current: String,
    onDismiss: () -> Unit,
    onSelect: (String) -> Unit,
) {
    val sheetState = rememberModalBottomSheetState()

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(
            Modifier
                // The three choices make this sheet tall enough to reach the
                // gesture/button bar — without the inset the last row sits
                // under it.
                .navigationBarsPadding()
                .padding(horizontal = 24.dp)
                .padding(bottom = 24.dp),
        ) {
            Text(
                stringResource(R.string.transport_sheet_title),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.transport_sheet_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(12.dp))
            TransportPreference.choices.forEach { type ->
                TransportChoiceRow(
                    type = type,
                    selected = type == current,
                    onClick = { onSelect(type) },
                )
            }
        }
    }
}

@Composable
private fun TransportChoiceRow(type: String, selected: Boolean, onClick: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 10.dp),
    ) {
        RadioButton(selected = selected, onClick = onClick)
        Spacer(Modifier.width(8.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(transportName(type), style = MaterialTheme.typography.bodyLarge)
                if (type == TransportPreference.DEFAULT) {
                    Spacer(Modifier.width(8.dp))
                    RecommendedBadge()
                }
            }
            Text(
                transportDescription(type),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun RecommendedBadge() {
    Box(
        Modifier
            .clip(CircleShape)
            .background(MaterialTheme.colorScheme.secondaryContainer)
            .padding(horizontal = 8.dp, vertical = 2.dp),
    ) {
        Text(
            stringResource(R.string.transport_recommended),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSecondaryContainer,
        )
    }
}

@Composable
private fun transportName(type: String): String = when (type) {
    TransportPreference.DMSG -> stringResource(R.string.transport_dmsg)
    TransportPreference.STCPR -> stringResource(R.string.transport_stcpr)
    TransportPreference.SUDPH -> stringResource(R.string.transport_sudph)
    else -> type
}

@Composable
private fun transportDescription(type: String): String = when (type) {
    TransportPreference.DMSG -> stringResource(R.string.transport_dmsg_hint)
    TransportPreference.STCPR -> stringResource(R.string.transport_stcpr_hint)
    TransportPreference.SUDPH -> stringResource(R.string.transport_sudph_hint)
    else -> ""
}

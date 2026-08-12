package com.skycoin.skywire.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material3.Button
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R

/**
 * The route-length control: how many hops the visor insists on when it builds
 * a route.
 *
 * One hop allows a direct route to the exit, which is the fastest thing the
 * network can do and the default. Two or more forces the traffic through
 * intermediaries, so no single node sees both who is asking and what is being
 * asked — that is the property being bought, and latency is what it costs.
 * The wording says that rather than showing a bare number, because "min_hops"
 * means nothing to someone deciding whether they want it.
 *
 * Visor-wide, not SkyVPN-only: it is a router knob and every route the visor
 * builds obeys it. It lives on the SkyVPN screen because that is where the
 * trade-off is felt.
 */
@Composable
fun MinHopsCard(
    hops: Int,
    enabled: Boolean,
    /**
     * Whether routes through intermediates can be built at all — i.e. whether
     * this visor holds transports to route THROUGH, which on a phone means
     * Connect to public visors is on.
     *
     * With it off, anything above 1 is offered but cannot work: the dial fails
     * before the route finder is even asked, because the visor has no
     * transport and nothing creates one above one hop (see DialRoutes).
     * Offering a choice that only ever fails is worse than not offering it,
     * so those tiles are shown disabled with the reason and where to change
     * it.
     */
    multiHopAvailable: Boolean = true,
    onSelect: (Int) -> Unit,
) {
    var customSheetOpen by remember { mutableStateOf(false) }
    // A route length asked for by number rather than by preset — either typed
    // here or set by an operator who edited the config by hand. Owning it in
    // the fourth tile means such a value shows as a selected state instead of
    // the row going blank.
    val customActive = hops > 0 && hops !in HOP_CHOICES

    SectionCard {
        Text(
            stringResource(R.string.hops_title),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(10.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            HOP_CHOICES.forEach { choice ->
                HopTile(
                    title = pluralStringResource(R.plurals.hub_hops, choice, choice),
                    label = stringResource(
                        when (choice) {
                            1 -> R.string.hops_label_fastest
                            2 -> R.string.hops_label_balanced
                            else -> R.string.hops_label_private
                        },
                    ),
                    selected = hops == choice,
                    // The selected tile is inert: re-selecting it would be a
                    // pointless PUT to the visor.
                    enabled = enabled && (choice == 1 || multiHopAvailable) && hops != choice,
                    onClick = { onSelect(choice) },
                    modifier = Modifier.weight(1f),
                )
            }
            HopTile(
                title = if (customActive) {
                    pluralStringResource(R.plurals.hub_hops, hops, hops)
                } else {
                    stringResource(R.string.hops_custom_more)
                },
                label = stringResource(R.string.hops_label_custom),
                selected = customActive,
                // Stays tappable while selected, unlike the presets — the tap
                // reopens the sheet to change the number.
                enabled = enabled && multiHopAvailable,
                onClick = { customSheetOpen = true },
                modifier = Modifier.weight(1f),
            )
        }
        Spacer(Modifier.height(10.dp))
        Text(
            stringResource(
                when {
                    !multiHopAvailable -> R.string.hops_hint_needs_autoconnect
                    hops == 1 -> R.string.hops_hint_direct
                    hops >= 2 -> R.string.hops_hint_multi
                    else -> R.string.hops_hint_unknown
                },
            ),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }

    if (customSheetOpen) {
        CustomHopsSheet(
            current = if (customActive) hops else 0,
            onDismiss = { customSheetOpen = false },
            onSave = { n ->
                customSheetOpen = false
                if (n != hops) onSelect(n)
            },
        )
    }
}

@Composable
private fun HopTile(
    title: String,
    label: String,
    selected: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        modifier = modifier
            .clip(MaterialTheme.shapes.medium)
            .background(
                if (selected) {
                    MaterialTheme.colorScheme.primary
                } else {
                    MaterialTheme.colorScheme.surfaceContainerHighest
                },
            )
            .clickable(enabled = enabled, onClick = onClick)
            .padding(vertical = 12.dp, horizontal = 8.dp),
    ) {
        // Dimmed when the choice cannot be taken, so "unavailable" is visible
        // before the tap rather than only in the hint underneath.
        val content = (
            if (selected) {
                MaterialTheme.colorScheme.onPrimary
            } else {
                MaterialTheme.colorScheme.onSurface
            }
            ).copy(alpha = if (enabled || selected) 1f else 0.4f)
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            color = content,
        )
        Text(
            text = label,
            style = MaterialTheme.typography.labelSmall,
            color = content.copy(alpha = 0.75f),
            textAlign = TextAlign.Center,
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CustomHopsSheet(current: Int, onDismiss: () -> Unit, onSave: (Int) -> Unit) {
    val sheetState = rememberModalBottomSheetState()
    var text by remember { mutableStateOf(if (current > 0) current.toString() else "") }
    val hops = text.toIntOrNull()
    val valid = hops != null && hops in MIN_CUSTOM_HOPS..MAX_CUSTOM_HOPS

    ModalBottomSheet(onDismissRequest = onDismiss, sheetState = sheetState) {
        Column(Modifier.padding(horizontal = 24.dp).padding(bottom = 32.dp)) {
            Text(
                stringResource(R.string.hops_custom_title),
                style = MaterialTheme.typography.titleMedium,
            )
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.hops_custom_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = text,
                onValueChange = { text = it.filter(Char::isDigit).take(2) },
                singleLine = true,
                isError = text.isNotEmpty() && !valid,
                label = { Text(stringResource(R.string.hops_custom_label)) },
                keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Number),
                modifier = Modifier.fillMaxWidth(),
            )
            if (text.isNotEmpty() && !valid) {
                Spacer(Modifier.height(6.dp))
                Text(
                    stringResource(R.string.hops_custom_invalid),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
            Spacer(Modifier.height(20.dp))
            Row(horizontalArrangement = Arrangement.End, modifier = Modifier.fillMaxWidth()) {
                TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
                Spacer(Modifier.width(8.dp))
                Button(onClick = { onSave(hops!!) }, enabled = valid) {
                    Text(stringResource(R.string.save))
                }
            }
        }
    }
}

/**
 * One, two, three as one-tap presets. Above three the added latency on a
 * phone usually stops being worth the marginal anonymity, and each extra hop
 * is another node that has to stay up for the route to survive — but that is
 * a judgement, not a law, so the fourth tile lets a number be named anyway.
 */
private val HOP_CHOICES = listOf(1, 2, 3)

// Bounds for the typed value: 0 means "routing disabled" to the visor and
// must never be sent, and the route-finder service clamps its graph search
// at 10 hops deep, so anything above that can never be satisfied.
private const val MIN_CUSTOM_HOPS = 1
private const val MAX_CUSTOM_HOPS = 10

package com.skycoin.skywire.ui.components

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
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
    onSelect: (Int) -> Unit,
) {
    SectionCard {
        Text(
            stringResource(R.string.hops_title),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(10.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            HOP_CHOICES.forEach { choice ->
                HopChoice(
                    hops = choice,
                    // Anything the visor reports outside the offered set
                    // (an operator edited the config by hand) leaves all
                    // three unselected rather than silently rounding.
                    selected = hops == choice,
                    enabled = enabled,
                    onClick = { onSelect(choice) },
                    modifier = Modifier.weight(1f),
                )
            }
        }
        Spacer(Modifier.height(10.dp))
        Text(
            stringResource(
                when (hops) {
                    1 -> R.string.hops_hint_direct
                    in 2..Int.MAX_VALUE -> R.string.hops_hint_multi
                    else -> R.string.hops_hint_unknown
                },
            ),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

@Composable
private fun HopChoice(
    hops: Int,
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
            .clickable(enabled = enabled && !selected, onClick = onClick)
            .padding(vertical = 12.dp, horizontal = 8.dp),
    ) {
        val content = if (selected) {
            MaterialTheme.colorScheme.onPrimary
        } else {
            MaterialTheme.colorScheme.onSurface
        }
        Text(
            text = pluralStringResource(R.plurals.hub_hops, hops, hops),
            style = MaterialTheme.typography.titleMedium,
            color = content,
        )
        Text(
            text = stringResource(
                when (hops) {
                    1 -> R.string.hops_label_fastest
                    2 -> R.string.hops_label_balanced
                    else -> R.string.hops_label_private
                },
            ),
            style = MaterialTheme.typography.labelSmall,
            color = content.copy(alpha = 0.75f),
            textAlign = TextAlign.Center,
        )
    }
}

/**
 * One, two, three. Above three the added latency on a phone stops being worth
 * the marginal anonymity, and each extra hop is another node that has to stay
 * up for the route to survive.
 */
private val HOP_CHOICES = listOf(1, 2, 3)

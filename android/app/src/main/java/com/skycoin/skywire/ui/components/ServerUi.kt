package com.skycoin.skywire.ui.components

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ColumnScope
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.api.ServiceEntry
import com.skycoin.skywire.ui.theme.SkyAccents
import kotlinx.serialization.Serializable
import java.util.Locale

/**
 * The pieces every "pick a public server and connect to it" screen is made
 * of. SkySOCKS established the pattern and SkyVPN repeats it exactly — the
 * two differ in which service-discovery type they ask for and what they do
 * once connected, not in how a server is chosen or how a status card reads.
 */

/** The last server the user connected to, kept across app restarts. */
@Serializable
data class SavedServer(
    val pk: String,
    val country: String = "",
    val version: String = "",
) {
    companion object {
        fun of(entry: ServiceEntry) = SavedServer(
            pk = entry.pk,
            country = entry.geo?.country.orEmpty(),
            version = entry.version,
        )
    }
}

/** Two-letter ISO country code → flag emoji; null for anything else. */
fun flagEmoji(country: String): String? {
    if (country.length != 2 || !country.all { it.isLetter() }) return null
    val code = country.uppercase()
    val base = 0x1F1E6 // REGIONAL INDICATOR SYMBOL LETTER A
    return String(Character.toChars(base + (code[0] - 'A'))) +
        String(Character.toChars(base + (code[1] - 'A')))
}

fun shortPk(pk: String): String = if (pk.length <= 20) pk else pk.take(10) + "…" + pk.takeLast(8)

fun formatBytes(bytes: Long): String {
    if (bytes < 1024) return "$bytes B"
    var value = bytes.toDouble() / 1024
    val units = listOf("KB", "MB", "GB", "TB")
    var unit = 0
    while (value >= 1024 && unit < units.lastIndex) {
        value /= 1024
        unit++
    }
    return String.format(Locale.US, "%.1f %s", value, units[unit])
}

/**
 * `2d 3h 4m 5s` — a visor's uptime, which is measured in days rather than the
 * minutes a tunnel session lasts, so the days unit is worth its width and the
 * seconds keep ticking visibly.
 *
 * Composable for the units alone: `d`/`h`/`m`/`s` are English abbreviations,
 * and a language that writes them out (`2天3小时`) also sets them solid — which
 * is why even the separator between the parts is a resource.
 */
@Composable
fun formatUptime(seconds: Double): String {
    val total = seconds.toLong()
    val days = total / 86_400
    val hours = (total % 86_400) / 3_600
    val minutes = (total % 3_600) / 60
    val parts = mutableListOf<String>()
    if (days > 0) parts += stringResource(R.string.unit_days, days.toString())
    if (hours > 0 || days > 0) parts += stringResource(R.string.unit_hours, hours.toString())
    if (minutes > 0 || hours > 0 || days > 0) {
        parts += stringResource(R.string.unit_minutes, minutes.toString())
    }
    parts += stringResource(R.string.unit_seconds, (total % 60).toString())
    return parts.joinToString(stringResource(R.string.unit_separator))
}

/** `1h 04m 12s`, dropping the leading units that are still zero. */
@Composable
fun formatDuration(seconds: Long): String {
    val h = seconds / 3600
    val m = (seconds % 3600) / 60
    val s = seconds % 60
    val separator = stringResource(R.string.unit_separator)
    return when {
        h > 0 -> listOf(
            stringResource(R.string.unit_hours, h.toString()),
            stringResource(R.string.unit_minutes, pad(m)),
            stringResource(R.string.unit_seconds, pad(s)),
        ).joinToString(separator)
        m > 0 -> listOf(
            stringResource(R.string.unit_minutes, m.toString()),
            stringResource(R.string.unit_seconds, pad(s)),
        ).joinToString(separator)
        else -> stringResource(R.string.unit_seconds, s.toString())
    }
}

/** Zero-padded so a running clock does not change width as it ticks. */
private fun pad(value: Long): String = value.toString().padStart(2, '0')

/** Status dot colors shared by the app screens — the theme's accents. */
val CONNECTED_GREEN = SkyAccents.success
val PENDING_AMBER = SkyAccents.warning

/**
 * The app's standard card: the palette's blue-tinted near-white on a
 * hairline border, generous radius, 20dp inside. On the plain background
 * the border is what makes it a card — the fill alone is too close to
 * white to hold an edge.
 */
@Composable
fun SectionCard(content: @Composable ColumnScope.() -> Unit) {
    Card(
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            // Cards hold real prose: content must default to ink, not the
            // muted onSurfaceVariant this container would otherwise imply.
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
        shape = MaterialTheme.shapes.large,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(20.dp), content = content)
    }
}

@Composable
fun InfoRow(
    label: String,
    value: String,
    mono: Boolean = false,
    valueColor: Color = MaterialTheme.colorScheme.onSurface,
    modifier: Modifier = Modifier,
) {
    Row(
        horizontalArrangement = Arrangement.SpaceBetween,
        verticalAlignment = Alignment.CenterVertically,
        modifier = modifier
            .fillMaxWidth()
            .padding(vertical = 4.dp),
    ) {
        // The label keeps its intrinsic width (softWrap off) so a long value
        // can never squeeze it down to one character per line; the value takes
        // the rest and wraps right-aligned.
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 1,
            softWrap = false,
        )
        Spacer(Modifier.width(16.dp))
        Text(
            value,
            style = MaterialTheme.typography.bodyMedium.let {
                if (mono) it.copy(fontFamily = FontFamily.Monospace) else it
            },
            color = valueColor,
            textAlign = TextAlign.End,
            modifier = Modifier.weight(1f),
        )
    }
}

/** One row of a service-discovery list: flag, location, key, version. */
@Composable
fun ServerRow(
    server: ServiceEntry,
    selected: Boolean,
    enabled: Boolean,
    onClick: () -> Unit,
) {
    Card(
        colors = CardDefaults.cardColors(
            containerColor = if (selected) {
                MaterialTheme.colorScheme.secondaryContainer
            } else {
                MaterialTheme.colorScheme.surfaceVariant
            },
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
        modifier = Modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, onClick = onClick),
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier.padding(horizontal = 16.dp, vertical = 12.dp),
        ) {
            flagEmoji(server.geo?.country.orEmpty())?.let { flag ->
                Text(flag, style = MaterialTheme.typography.titleLarge)
                Spacer(Modifier.width(12.dp))
            }
            Column(Modifier.weight(1f)) {
                Text(
                    listOfNotNull(
                        server.geo?.country?.uppercase()?.takeIf { it.isNotEmpty() },
                        server.geo?.region?.takeIf { it.isNotEmpty() },
                    ).joinToString(" · ").ifEmpty { stringResource(R.string.server_location_unknown) },
                    style = MaterialTheme.typography.bodyMedium,
                )
                Text(
                    shortPk(server.pk),
                    style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            server.version.takeIf { it.isNotEmpty() }?.let { version ->
                Spacer(Modifier.width(12.dp))
                Text(
                    version,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

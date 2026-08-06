package com.skycoin.skywire.ui.components

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
 */
fun formatUptime(seconds: Double): String {
    val total = seconds.toLong()
    val days = total / 86_400
    val hours = (total % 86_400) / 3_600
    val minutes = (total % 3_600) / 60
    return buildString {
        if (days > 0) append("${days}d ")
        if (hours > 0 || days > 0) append("${hours}h ")
        if (minutes > 0 || hours > 0 || days > 0) append("${minutes}m ")
        append("${total % 60}s")
    }
}

/** `1h 04m 12s`, dropping the leading units that are still zero. */
fun formatDuration(seconds: Long): String {
    val h = seconds / 3600
    val m = (seconds % 3600) / 60
    val s = seconds % 60
    return when {
        h > 0 -> String.format(Locale.US, "%dh %02dm %02ds", h, m, s)
        m > 0 -> String.format(Locale.US, "%dm %02ds", m, s)
        else -> String.format(Locale.US, "%ds", s)
    }
}

/** Status dot colors shared by the app screens. */
val CONNECTED_GREEN = Color(0xFF16A34A)
val PENDING_AMBER = Color(0xFFF59E0B)

@Composable
fun SectionCard(content: @Composable ColumnScope.() -> Unit) {
    Card(
        colors = CardDefaults.cardColors(containerColor = MaterialTheme.colorScheme.surfaceVariant),
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

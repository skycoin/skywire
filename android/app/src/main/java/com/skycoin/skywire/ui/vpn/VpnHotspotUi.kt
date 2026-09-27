package com.skycoin.skywire.ui.vpn

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.provider.Settings
import android.widget.Toast
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.core.VpnHotspot
import com.skycoin.skywire.core.VpnHotspotState
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.SectionCard

/**
 * The VPN hotspot: SkyVPN for the devices on this phone's hotspot, through a
 * proxy on the hotspot's address (see [VpnHotspot]). The switch is a phone
 * preference like the killswitch — it applies whenever SkyVPN runs — and
 * what the card says underneath is what the other device needs to be told.
 */
@Composable
internal fun HotspotCard(
    on: Boolean,
    hotspot: VpnHotspotState,
    /** SkyVpnService is up, so the proxy port can be. */
    serviceUp: Boolean,
    /** The tunnel is carrying: a shared connection has somewhere to go. */
    carrying: Boolean,
    onChange: (Boolean) -> Unit,
) {
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    val copied = stringResource(R.string.copied_to_clipboard)
    val noSettings = stringResource(R.string.vpn_hotspot_no_settings)

    SectionCard {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Text(
                stringResource(R.string.vpn_hotspot_title),
                style = MaterialTheme.typography.titleMedium,
                modifier = Modifier.weight(1f),
            )
            Switch(checked = on, onCheckedChange = onChange)
        }
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.vpn_hotspot_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (on) {
            Spacer(Modifier.height(12.dp))
            val error = hotspot.error
            when {
                error != null -> Text(
                    stringResource(R.string.vpn_hotspot_error, VpnHotspot.PORT, error),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.error,
                )
                !serviceUp || !hotspot.serving -> Text(
                    stringResource(R.string.vpn_hotspot_waiting),
                    style = MaterialTheme.typography.bodyMedium,
                )
                hotspot.addresses.isEmpty() -> Text(
                    stringResource(R.string.vpn_hotspot_no_hotspot),
                    style = MaterialTheme.typography.bodyMedium,
                    color = PENDING_AMBER,
                )
                else -> {
                    Text(
                        stringResource(R.string.vpn_hotspot_proxy),
                        style = MaterialTheme.typography.labelLarge,
                    )
                    hotspot.addresses.forEach { address ->
                        InfoLine(
                            label = stringResource(R.string.vpn_hotspot_host),
                            value = address,
                            onCopy = {
                                clipboard.setText(AnnotatedString(address))
                                Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                            },
                        )
                    }
                    InfoLine(
                        label = stringResource(R.string.vpn_hotspot_port),
                        value = VpnHotspot.PORT.toString(),
                        onCopy = {
                            clipboard.setText(AnnotatedString(VpnHotspot.PORT.toString()))
                            Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                        },
                    )
                    Spacer(Modifier.height(4.dp))
                    Text(
                        stringResource(R.string.vpn_hotspot_setup),
                        style = MaterialTheme.typography.bodySmall,
                    )
                    if (!carrying) {
                        Spacer(Modifier.height(4.dp))
                        Text(
                            stringResource(R.string.vpn_hotspot_not_carrying),
                            style = MaterialTheme.typography.bodySmall,
                            color = PENDING_AMBER,
                        )
                    }
                    if (hotspot.devices > 0) {
                        Spacer(Modifier.height(4.dp))
                        Text(
                            pluralStringResource(R.plurals.vpn_hotspot_devices, hotspot.devices, hotspot.devices),
                            style = MaterialTheme.typography.bodySmall,
                        )
                    }
                }
            }
            Spacer(Modifier.height(8.dp))
            Text(
                stringResource(R.string.vpn_hotspot_leak_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            FilledTonalButton(
                onClick = {
                    if (!openHotspotSettings(context)) {
                        Toast.makeText(context, noSettings, Toast.LENGTH_SHORT).show()
                    }
                },
            ) {
                Text(stringResource(R.string.vpn_hotspot_settings))
            }
        }
    }
}

/** One "label  value" line whose value copies on tap. */
@Composable
private fun InfoLine(label: String, value: String, onCopy: () -> Unit) {
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onCopy)
            .padding(vertical = 4.dp),
    ) {
        Text(
            label,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.weight(1f),
        )
        Text(value, style = MaterialTheme.typography.bodyLarge, fontFamily = FontFamily.Monospace)
    }
}

/**
 * The system's hotspot & tethering screen. It has no public action, so the
 * Settings app's own entry is tried first and the wireless settings — one tap
 * away from it everywhere — after that.
 */
private fun openHotspotSettings(context: Context): Boolean {
    val tries = listOf(
        Intent(Intent.ACTION_MAIN).setClassName("com.android.settings", "com.android.settings.TetherSettings"),
        Intent(Settings.ACTION_WIRELESS_SETTINGS),
    )
    return tries.any { intent ->
        try {
            context.startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
            true
        } catch (e: ActivityNotFoundException) {
            false
        } catch (e: SecurityException) {
            false
        }
    }
}

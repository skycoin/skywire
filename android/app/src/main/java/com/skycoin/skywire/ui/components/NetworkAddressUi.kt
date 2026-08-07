package com.skycoin.skywire.ui.components

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.height
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.api.Overview
import java.util.Locale

/**
 * What this phone's address actually is, and what SkyVPN does and does not
 * change about it.
 *
 * The honest version of "your IP before and after". A tunnelled phone
 * normally proves itself by fetching its own address and watching it change,
 * and that cannot work here: [com.skycoin.skywire.core.SkyVpnService] excludes
 * this app's UID from the tunnel — it has to, because the visor is a child of
 * the same UID and its dmsg traffic is what CARRIES the tunnel — so any probe
 * the phone makes leaves through the underlay whether or not SkyVPN is up. It
 * would print the same address twice and look broken.
 *
 * So the card shows the two things that are true: the address this device
 * reaches the network from, which SkyVPN does not change, and the country the
 * traffic leaves from, which is the thing that does. The exit's own address is
 * not knowable from here — the VPN handshake carries a public key, a TUN IP
 * and a gateway, and no public address in either direction.
 */
@Composable
fun NetworkAddressCard(overview: Overview?, exitCountry: String?, connected: Boolean) {
    SectionCard {
        Text(
            stringResource(R.string.net_addr_title),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(6.dp))

        InfoRow(
            label = stringResource(R.string.net_addr_device),
            value = deviceAddressText(overview),
            mono = overview?.publicIpOrNull != null,
        )
        InfoRow(
            label = stringResource(R.string.net_addr_exit),
            value = when {
                !connected -> stringResource(R.string.net_addr_exit_none)
                exitCountry.isNullOrEmpty() -> stringResource(R.string.net_addr_exit_unknown)
                else -> countryText(exitCountry)
            },
        )

        Spacer(Modifier.height(8.dp))
        Column {
            Text(
                stringResource(R.string.net_addr_hint),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * The device's own address, or the reason there isn't one. Symmetric NAT is
 * the common case on a mobile carrier and is worth naming: it is not a
 * failure, it means no single public address belongs to this phone.
 */
@Composable
fun deviceAddressText(overview: Overview?): String {
    val ip = overview?.publicIpOrNull
    return when {
        ip != null -> ip
        overview == null -> stringResource(R.string.net_addr_waiting)
        overview.isSymmetricNat -> stringResource(R.string.net_addr_carrier_nat)
        else -> stringResource(R.string.net_addr_unknown)
    }
}

/** `🇨🇦 Canada` — flag plus the name in the reader's own language. */
fun countryText(code: String): String {
    val name = Locale("", code).displayCountry.takeIf { it.isNotBlank() } ?: code
    return listOfNotNull(flagEmoji(code), name).joinToString(" ")
}

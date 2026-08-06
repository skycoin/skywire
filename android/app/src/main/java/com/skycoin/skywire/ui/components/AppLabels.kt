package com.skycoin.skywire.ui.components

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.core.SkychatProfile
import com.skycoin.skywire.core.SkydexProfile
import com.skycoin.skywire.ui.socks.SocksArgs
import com.skycoin.skywire.ui.vpn.VpnArgs

/**
 * The two names every client app has: the product it is (**SkyVPN**) and the
 * process the visor runs it as (`vpn-client`).
 *
 * Everywhere else in the app only the first one is ever shown — the hub tile,
 * the screen, the notification. The log surfaces are the exception, because
 * there the process name is the thing you actually need: it is what the config
 * calls it, what the API route is keyed on, and what a line in the log says.
 * So they show both, product first, rather than dropping the user into a list
 * of process names and expecting them to know which app is which.
 */

/** Product name, or null for an app with no screen of its own. */
@Composable
fun appProductName(app: String): String? = when (app) {
    SkychatProfile.APP -> stringResource(R.string.app_skychat)
    SocksArgs.APP -> stringResource(R.string.app_skysocks)
    VpnArgs.APP -> stringResource(R.string.app_skyvpn)
    SkydexProfile.APP -> stringResource(R.string.app_skydex)
    else -> null
}

/** `SkyVPN (vpn-client)` — for lists, which have the width for both. */
@Composable
fun appLabel(app: String): String = appProductName(app)?.let { "$it ($app)" } ?: app

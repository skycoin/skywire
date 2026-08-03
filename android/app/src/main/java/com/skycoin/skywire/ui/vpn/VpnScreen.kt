package com.skycoin.skywire.ui.vpn

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.AppRouteScaffold

/** SkyVPN route — VpnService TUN handoff, killswitch, live stats. */
@Composable
fun VpnScreen(onBack: () -> Unit) {
    AppRouteScaffold(
        title = stringResource(R.string.app_skyvpn),
        subtitle = stringResource(R.string.vpn_placeholder),
        onBack = onBack,
    )
}

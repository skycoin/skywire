package com.skycoin.skywire.ui.socks

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.AppRouteScaffold

/** SkySOCKS route — server list / connect / expose-port flow. */
@Composable
fun SocksScreen(onBack: () -> Unit) {
    AppRouteScaffold(
        title = stringResource(R.string.app_skysocks),
        subtitle = stringResource(R.string.socks_placeholder),
        onBack = onBack,
    )
}

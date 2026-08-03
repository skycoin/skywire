package com.skycoin.skywire.ui.dex

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.AppRouteScaffold

/** SkyDEX route — market PK entry + embedded skydex UI WebView. */
@Composable
fun DexScreen(onBack: () -> Unit) {
    AppRouteScaffold(
        title = stringResource(R.string.app_skydex),
        subtitle = stringResource(R.string.dex_placeholder),
        onBack = onBack,
    )
}

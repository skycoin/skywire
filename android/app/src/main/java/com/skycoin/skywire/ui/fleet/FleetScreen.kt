package com.skycoin.skywire.ui.fleet

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.AppRouteScaffold

/** Fleet route — opt-in dmsg ingest toggle + status-only visor list. */
@Composable
fun FleetScreen(onBack: () -> Unit) {
    AppRouteScaffold(
        title = stringResource(R.string.app_fleet),
        subtitle = stringResource(R.string.fleet_placeholder),
        onBack = onBack,
    )
}

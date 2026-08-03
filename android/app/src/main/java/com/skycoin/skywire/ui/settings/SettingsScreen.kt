package com.skycoin.skywire.ui.settings

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.PlaceholderTab

/** Settings tab — identity / config / export / app lock / logs hub. */
@Composable
fun SettingsScreen() {
    PlaceholderTab(
        title = stringResource(R.string.tab_settings),
        subtitle = stringResource(R.string.settings_placeholder),
    )
}

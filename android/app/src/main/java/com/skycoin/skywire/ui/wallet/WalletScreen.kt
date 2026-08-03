package com.skycoin.skywire.ui.wallet

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.PlaceholderTab

/** Wallet tab — the native Compose Skycoin wallet is built here. */
@Composable
fun WalletScreen() {
    PlaceholderTab(
        title = stringResource(R.string.app_wallet),
        subtitle = stringResource(R.string.wallet_placeholder),
    )
}

package com.skycoin.skywire.ui.chat

import androidx.compose.runtime.Composable
import androidx.compose.ui.res.stringResource
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.PlaceholderTab

/** Chat tab — the embedded skychat web UI loads here (WebView) when it lands. */
@Composable
fun ChatScreen() {
    PlaceholderTab(
        title = stringResource(R.string.app_skychat),
        subtitle = stringResource(R.string.chat_placeholder),
    )
}

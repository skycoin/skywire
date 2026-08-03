package com.skycoin.skywire.ui.components

import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp

/** Centered placeholder body for tabs whose real screens land later. */
@Composable
fun PlaceholderTab(title: String, subtitle: String) {
    Column(
        modifier = Modifier
            .fillMaxSize()
            .padding(24.dp),
        verticalArrangement = androidx.compose.foundation.layout.Arrangement.Center,
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(title, style = MaterialTheme.typography.headlineMedium)
        Spacer(Modifier.height(8.dp))
        Text(
            subtitle,
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            textAlign = TextAlign.Center,
        )
    }
}

/**
 * Shared scaffold for the full-screen app routes pushed from the hub
 * (SkySOCKS / SkyVPN / SkyDEX / Fleet): [SkyTopBar] with back, placeholder
 * body. Feature work replaces the body; a `Logs` app-bar action joins every
 * app screen so each app's log feed is one tap away.
 */
@Composable
fun AppRouteScaffold(title: String, subtitle: String, onBack: () -> Unit) {
    Scaffold(
        topBar = { SkyTopBar(title = title, onBack = onBack) },
    ) { padding ->
        Column(modifier = Modifier.padding(padding)) {
            PlaceholderTab(title, subtitle)
        }
    }
}

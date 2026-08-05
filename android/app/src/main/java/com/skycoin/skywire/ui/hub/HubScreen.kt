package com.skycoin.skywire.ui.hub

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.outlined.AccountBalanceWallet
import androidx.compose.material.icons.outlined.AltRoute
import androidx.compose.material.icons.outlined.CandlestickChart
import androidx.compose.material.icons.outlined.Hub
import androidx.compose.material.icons.outlined.Videocam
import androidx.compose.material.icons.outlined.VpnLock
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.alpha
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.graphics.vector.rememberVectorPainter
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.navigation.Routes

/**
 * The apps hub behind the center Skycoin-logo slot: adaptive grid of rounded
 * tiles. Tiles accept any [Painter] so the designed logos are a drop-in later.
 *
 * At most ONE greyed "coming soon" tile at a time — currently SkyMeet. A hub
 * with several of them reads as an unfinished app; a single one reads as the
 * next thing being built. The rest arrive as they ship.
 */
private data class HubTile(
    val name: String,
    val icon: @Composable () -> Painter,
    val onClick: (() -> Unit)?,
    val comingSoon: Boolean = false,
)

@Composable
fun HubScreen(
    onOpenRoute: (String) -> Unit,
    onOpenTab: (String) -> Unit,
) {
    val tiles = listOf(
        HubTile(stringResource(R.string.app_skysocks), { rememberVectorPainter(Icons.Outlined.AltRoute) }, { onOpenRoute(Routes.SOCKS) }),
        HubTile(stringResource(R.string.app_skyvpn), { rememberVectorPainter(Icons.Outlined.VpnLock) }, { onOpenRoute(Routes.VPN) }),
        HubTile(stringResource(R.string.app_skydex), { rememberVectorPainter(Icons.Outlined.CandlestickChart) }, { onOpenRoute(Routes.DEX) }),
        // SkyChat / Wallet tiles re-route to the same destinations as their tabs.
        HubTile(stringResource(R.string.app_skychat), { rememberVectorPainter(Icons.AutoMirrored.Outlined.Chat) }, { onOpenTab(Routes.CHAT) }),
        HubTile(stringResource(R.string.app_wallet), { rememberVectorPainter(Icons.Outlined.AccountBalanceWallet) }, { onOpenTab(Routes.WALLET) }),
        HubTile(stringResource(R.string.app_fleet), { rememberVectorPainter(Icons.Outlined.Hub) }, { onOpenRoute(Routes.FLEET) }),
        HubTile(stringResource(R.string.app_skymeet), { rememberVectorPainter(Icons.Outlined.Videocam) }, onClick = null, comingSoon = true),
    )

    LazyVerticalGrid(
        columns = GridCells.Adaptive(minSize = 104.dp),
        modifier = Modifier.fillMaxSize(),
        contentPadding = androidx.compose.foundation.layout.PaddingValues(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
        horizontalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        items(tiles) { tile -> AppTile(tile) }
    }
}

@Composable
private fun AppTile(tile: HubTile) {
    Surface(
        shape = RoundedCornerShape(20.dp),
        color = MaterialTheme.colorScheme.surfaceVariant,
        modifier = Modifier
            .fillMaxWidth()
            .alpha(if (tile.comingSoon) 0.45f else 1f)
            .let { m -> tile.onClick?.let { m.clickable(onClick = it) } ?: m },
    ) {
        Column(
            horizontalAlignment = Alignment.CenterHorizontally,
            modifier = Modifier.padding(vertical = 18.dp, horizontal = 8.dp),
        ) {
            Icon(
                painter = tile.icon(),
                contentDescription = null,
                tint = MaterialTheme.colorScheme.primary,
                modifier = Modifier.size(40.dp),
            )
            Spacer(Modifier.height(10.dp))
            Text(
                text = tile.name,
                style = MaterialTheme.typography.labelLarge,
                textAlign = TextAlign.Center,
            )
            if (tile.comingSoon) {
                Spacer(Modifier.height(4.dp))
                Text(
                    text = stringResource(R.string.coming_soon),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

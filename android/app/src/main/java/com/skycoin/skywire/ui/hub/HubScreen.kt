package com.skycoin.skywire.ui.hub

import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.grid.GridCells
import androidx.compose.foundation.lazy.grid.GridItemSpan
import androidx.compose.foundation.lazy.grid.LazyVerticalGrid
import androidx.compose.foundation.lazy.grid.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.AccountBalanceWallet
import androidx.compose.material.icons.rounded.AltRoute
import androidx.compose.material.icons.rounded.CandlestickChart
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material.icons.rounded.Forum
import androidx.compose.material.icons.rounded.Hub
import androidx.compose.material.icons.rounded.Route
import androidx.compose.material.icons.rounded.Videocam
import androidx.compose.material.icons.rounded.VpnLock
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.CornerRadius
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.PathEffect
import androidx.compose.ui.graphics.drawscope.Stroke
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.pluralStringResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.core.SkychatProfile
import com.skycoin.skywire.core.SkydexProfile
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.countryText
import com.skycoin.skywire.ui.components.formatBytes
import com.skycoin.skywire.ui.components.formatDuration
import com.skycoin.skywire.ui.navigation.Routes
import com.skycoin.skywire.ui.socks.SocksArgs
import com.skycoin.skywire.ui.theme.SkyAccents
import com.skycoin.skywire.ui.theme.SkyHeroGradient
import com.skycoin.skywire.ui.vpn.VpnStatus

/**
 * The apps hub behind the raised cloud: category chips, a live SkyVPN hero
 * card while the tunnel is up (or on its way up), and a grid of app cards
 * whose status dots come from the visor's own app list.
 *
 * At most ONE greyed "coming soon" card at a time — currently SkyMeet. A hub
 * with several of them reads as an unfinished app; a single one reads as the
 * next thing being built. The rest arrive as they ship.
 */
private enum class HubCategory { NETWORK, FINANCE, SOCIAL }

private data class HubTile(
    val name: String,
    val subtitle: String,
    val icon: ImageVector,
    val category: HubCategory,
    /** Visor process whose status drives the card's dot; null = no dot. */
    val statusApp: String? = null,
    /** A count worth interrupting for — SkyChat's unread messages. */
    val badgeCount: Int? = null,
    val wide: Boolean = false,
    val comingSoon: Boolean = false,
    val onClick: (() -> Unit)? = null,
)

@Composable
fun HubScreen(
    onBack: () -> Unit,
    onOpenRoute: (String) -> Unit,
    onOpenTab: (String) -> Unit,
    viewModel: HubViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    var filter by rememberSaveable { mutableStateOf<HubCategory?>(null) }

    // SkyVPN is not in this list: it is the hero card, on or off.
    val tiles = buildList {
        add(
            HubTile(
                name = stringResource(R.string.app_skysocks),
                subtitle = stringResource(R.string.hub_socks_sub),
                icon = Icons.Rounded.AltRoute,
                category = HubCategory.NETWORK,
                statusApp = SocksArgs.APP,
                onClick = { onOpenRoute(Routes.SOCKS) },
            ),
        )
        add(
            HubTile(
                name = stringResource(R.string.app_skydex),
                subtitle = stringResource(R.string.hub_dex_sub),
                icon = Icons.Rounded.CandlestickChart,
                category = HubCategory.FINANCE,
                statusApp = SkydexProfile.APP,
                onClick = { onOpenRoute(Routes.DEX) },
            ),
        )
        add(
            HubTile(
                name = stringResource(R.string.app_skychat),
                subtitle = stringResource(R.string.hub_chat_sub),
                icon = Icons.Rounded.Forum,
                category = HubCategory.SOCIAL,
                statusApp = SkychatProfile.APP,
                badgeCount = state.unreadMessages.takeIf { it > 0 },
                // Same destination as the Chat tab (one screen, two entries).
                onClick = { onOpenTab(Routes.CHAT) },
            ),
        )
        add(
            HubTile(
                name = stringResource(R.string.app_wallet),
                // The one number a wallet is for, when there is one to show.
                subtitle = state.skyBalance
                    ?.let { stringResource(R.string.hub_wallet_balance, it) }
                    ?: stringResource(R.string.hub_wallet_sub),
                icon = Icons.Rounded.AccountBalanceWallet,
                category = HubCategory.FINANCE,
                onClick = { onOpenTab(Routes.WALLET) },
            ),
        )
        add(
            HubTile(
                name = stringResource(R.string.app_fleet),
                // With the ingest on, the subtitle is the fleet's headcount.
                subtitle = state.fleetOnline
                    ?.takeIf { state.fleetEnabled }
                    ?.let { pluralStringResource(R.plurals.hub_fleet_connected, it, it) }
                    ?: stringResource(R.string.hub_fleet_sub),
                icon = Icons.Rounded.Hub,
                category = HubCategory.NETWORK,
                wide = true,
                onClick = { onOpenRoute(Routes.FLEET) },
            ),
        )
        add(
            HubTile(
                name = stringResource(R.string.app_skymeet),
                subtitle = stringResource(R.string.hub_meet_sub),
                icon = Icons.Rounded.Videocam,
                category = HubCategory.SOCIAL,
                wide = true,
                comingSoon = true,
            ),
        )
    }
    // +1: SkyVPN, which lives in the hero card rather than the grid.
    val installed = tiles.count { !it.comingSoon } + 1
    val shown = tiles.filter { filter == null || it.category == filter }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.tab_hub_description),
                subtitle = stringResource(R.string.hub_subtitle, installed, state.runningCount),
                onBack = onBack,
                help = HelpTopic(R.string.help_hub_title, R.string.help_hub_body),
            )
        },
    ) { padding ->
        LazyVerticalGrid(
            columns = GridCells.Fixed(2),
            modifier = Modifier
                .fillMaxSize()
                .padding(padding),
            contentPadding = PaddingValues(start = 16.dp, end = 16.dp, bottom = 24.dp),
            verticalArrangement = Arrangement.spacedBy(11.dp),
            horizontalArrangement = Arrangement.spacedBy(11.dp),
        ) {
            item(span = { GridItemSpan(maxLineSpan) }) {
                CategoryChips(filter, onSelect = { filter = it })
            }
            if (filter == null || filter == HubCategory.NETWORK) {
                item(span = { GridItemSpan(maxLineSpan) }) {
                    VpnHeroCard(
                        state = state,
                        onOpen = { onOpenRoute(Routes.VPN) },
                        // Turning on needs an exit and the system consent;
                        // when the hub has neither, the SkyVPN screen does.
                        onTurnOn = { if (!viewModel.startVpn()) onOpenRoute(Routes.VPN) },
                        onTurnOff = viewModel::stopVpn,
                    )
                }
            }
            item(span = { GridItemSpan(maxLineSpan) }) {
                Text(
                    text = stringResource(R.string.hub_section_apps),
                    style = MaterialTheme.typography.titleSmall,
                    modifier = Modifier.padding(start = 2.dp, top = 8.dp),
                )
            }
            items(
                items = shown,
                span = { tile -> GridItemSpan(if (tile.wide) maxLineSpan else 1) },
            ) { tile ->
                if (tile.comingSoon) {
                    ComingSoonCard(tile)
                } else {
                    AppCard(tile, status = tile.statusApp?.let { state.apps[it] })
                }
            }
        }
    }
}

@Composable
private fun CategoryChips(filter: HubCategory?, onSelect: (HubCategory?) -> Unit) {
    Row(
        horizontalArrangement = Arrangement.spacedBy(8.dp),
        modifier = Modifier
            .fillMaxWidth()
            .horizontalScroll(rememberScrollState())
            .padding(vertical = 2.dp),
    ) {
        CategoryChip(stringResource(R.string.hub_filter_all), filter == null) { onSelect(null) }
        CategoryChip(stringResource(R.string.hub_filter_network), filter == HubCategory.NETWORK) {
            onSelect(HubCategory.NETWORK)
        }
        CategoryChip(stringResource(R.string.hub_filter_finance), filter == HubCategory.FINANCE) {
            onSelect(HubCategory.FINANCE)
        }
        CategoryChip(stringResource(R.string.hub_filter_social), filter == HubCategory.SOCIAL) {
            onSelect(HubCategory.SOCIAL)
        }
    }
}

@Composable
private fun CategoryChip(label: String, selected: Boolean, onClick: () -> Unit) {
    Surface(
        onClick = onClick,
        shape = MaterialTheme.shapes.small,
        color = if (selected) {
            MaterialTheme.colorScheme.primary
        } else {
            MaterialTheme.colorScheme.surfaceContainerLowest
        },
        border = if (selected) {
            null
        } else {
            BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant)
        },
        shadowElevation = if (selected) 4.dp else 0.dp,
    ) {
        Text(
            text = label,
            style = MaterialTheme.typography.labelLarge,
            color = if (selected) {
                MaterialTheme.colorScheme.onPrimary
            } else {
                MaterialTheme.colorScheme.onSurfaceVariant
            },
            modifier = Modifier.padding(horizontal = 15.dp, vertical = 8.dp),
        )
    }
}

/**
 * SkyVPN's card, always at hero size, on or off: status and session length,
 * the exit it points at — flag and country by name — live rates while it
 * carries, and the switch. The card body opens the SkyVPN screen. The switch
 * turns the tunnel off directly; turning it *on* works from here only when
 * nothing needs a screen (exit chosen, consent granted) — otherwise the
 * SkyVPN screen takes over.
 */
@Composable
private fun VpnHeroCard(
    state: HubUiState,
    onOpen: () -> Unit,
    onTurnOn: () -> Unit,
    onTurnOff: () -> Unit,
) {
    val vpn = state.vpn
    val on = state.vpnOn
    val starting = on && (
        vpn?.status == AppState.STATUS_STARTING ||
            vpn?.detailedStatus == VpnStatus.CONNECTING
        )
    val reconnecting = on && vpn?.detailedStatus == VpnStatus.RECONNECTING

    val statusText = when {
        !on -> stringResource(R.string.state_disconnected)
        starting -> stringResource(R.string.hub_hero_starting)
        reconnecting -> stringResource(R.string.hub_hero_reconnecting)
        else -> {
            val duration = state.vpnConnection?.connectionSeconds
                ?.takeIf { it > 0 }?.let { " · " + formatDuration(it) }.orEmpty()
            stringResource(R.string.state_connected) + duration
        }
    }
    val statusDot = when {
        !on -> Color.White.copy(alpha = 0.45f)
        starting || reconnecting -> SkyAccents.warning
        else -> SkyAccents.successBright
    }

    // The exit as flag, country name and enough of the key to tell two
    // exits in one country apart. The exit's own IP is not knowable from
    // this phone — the handshake carries a public key, a TUN IP and a
    // gateway, no public address in either direction (NetworkAddressCard
    // explains) — so the country is the "where" and the prefix the "which".
    val exit = state.vpnExitPk?.let { pk ->
        listOfNotNull(
            state.vpnCountry?.let(::countryText),
            pk.take(EXIT_PK_PREFIX),
        ).joinToString(" · ")
    } ?: stringResource(R.string.hub_hero_no_exit)

    Surface(
        onClick = onOpen,
        shape = MaterialTheme.shapes.large,
        color = Color.Transparent,
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(
            modifier = Modifier
                .background(SkyHeroGradient)
                .drawBehind {
                    // The design's two soft discs in the card's corner.
                    drawCircle(
                        color = Color.White.copy(alpha = 0.10f),
                        radius = 85.dp.toPx(),
                        center = Offset(size.width + 15.dp.toPx(), (-15).dp.toPx()),
                    )
                    drawCircle(
                        color = Color.White.copy(alpha = 0.07f),
                        radius = 60.dp.toPx(),
                        center = Offset(size.width - 44.dp.toPx(), size.height + 30.dp.toPx()),
                    )
                }
                .padding(18.dp),
        ) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Box(
                    contentAlignment = Alignment.Center,
                    modifier = Modifier
                        .size(48.dp)
                        .clip(MaterialTheme.shapes.medium)
                        .background(Color.White.copy(alpha = 0.18f)),
                ) {
                    Icon(
                        Icons.Rounded.VpnLock,
                        contentDescription = null,
                        tint = Color.White,
                        modifier = Modifier.size(26.dp),
                    )
                }
                Spacer(Modifier.width(13.dp))
                Column(Modifier.weight(1f)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        Box(
                            Modifier
                                .size(7.dp)
                                .background(statusDot, CircleShape),
                        )
                        Spacer(Modifier.width(7.dp))
                        Text(
                            text = statusText.uppercase(),
                            style = MaterialTheme.typography.labelSmall.copy(letterSpacing = 1.2.sp),
                            color = Color.White.copy(alpha = 0.75f),
                            maxLines = 1,
                            overflow = TextOverflow.Ellipsis,
                        )
                    }
                    Text(
                        text = stringResource(R.string.app_skyvpn),
                        style = MaterialTheme.typography.titleMedium,
                        color = Color.White,
                    )
                    Text(
                        text = exit,
                        style = MaterialTheme.typography.bodySmall,
                        color = Color.White.copy(alpha = 0.8f),
                        maxLines = 1,
                        overflow = TextOverflow.Ellipsis,
                    )
                }
                Spacer(Modifier.width(8.dp))
                val toggleLabel = stringResource(R.string.hub_vpn_toggle)
                Switch(
                    checked = on,
                    onCheckedChange = { checked -> if (checked) onTurnOn() else onTurnOff() },
                    enabled = !state.vpnBusy,
                    colors = SwitchDefaults.colors(
                        checkedThumbColor = Color.White,
                        checkedTrackColor = SkyAccents.successBright.copy(alpha = 0.9f),
                        uncheckedThumbColor = Color.White,
                        uncheckedTrackColor = Color.White.copy(alpha = 0.28f),
                        uncheckedBorderColor = Color.Transparent,
                    ),
                    modifier = Modifier.semantics { contentDescription = toggleLabel },
                )
            }
            // The stats row is part of the card's shape, not of its state:
            // the card never changes size, the numbers become — when there
            // is nothing to count.
            //
            // Down/Up come from HubViewModel's own sampling of the byte
            // counters, not from the connection's speed fields, which the
            // visor only fills from a route-group ping exchange and which
            // stay at zero on a phone.
            val conn = state.vpnConnection
            val rates = state.vpnRates
            Spacer(Modifier.height(15.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                HeroStat(
                    label = stringResource(R.string.hub_stat_down),
                    value = rates?.let { formatBytes(it.downBytesPerSec) + "/s" } ?: "—",
                    modifier = Modifier.weight(1f),
                )
                HeroStat(
                    label = stringResource(R.string.hub_stat_up),
                    value = rates?.let { formatBytes(it.upBytesPerSec) + "/s" } ?: "—",
                    modifier = Modifier.weight(1f),
                )
                HeroStat(
                    label = stringResource(R.string.hub_stat_data),
                    value = conn?.let { formatBytes(it.bandwidthSent + it.bandwidthReceived) }
                        ?: "—",
                    modifier = Modifier.weight(1f),
                )
            }

            // Two facts that hold whether or not the tunnel is up, so they
            // sit outside the stats row: how many hops the visor dials
            // through, and whether the killswitch would catch a drop.
            Spacer(Modifier.height(10.dp))
            Row(
                horizontalArrangement = Arrangement.spacedBy(8.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                HeroChip(
                    icon = Icons.Rounded.Route,
                    text = if (state.minHops > 0) {
                        pluralStringResource(R.plurals.hub_hops, state.minHops, state.minHops)
                    } else {
                        stringResource(R.string.hub_hops_unknown)
                    },
                )
                KillswitchChip(on = state.killswitch)
            }
        }
    }
}

/**
 * The killswitch spelled out, color-coded: green words when a dropped
 * tunnel would be caught, red when it would not. A glyph alone read as
 * decoration here — the state is worth a sentence fragment.
 */
@Composable
private fun KillswitchChip(on: Boolean) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .clip(MaterialTheme.shapes.small)
            .background(Color.White.copy(alpha = 0.13f))
            .padding(horizontal = 10.dp, vertical = 6.dp),
    ) {
        Text(
            text = stringResource(
                if (on) R.string.hub_killswitch_on else R.string.hub_killswitch_off,
            ),
            style = MaterialTheme.typography.labelMedium,
            color = if (on) SkyAccents.successBright else SkyAccents.dangerBright,
            maxLines = 1,
        )
    }
}

/** A small fact on the hero's gradient: an icon and a word. */
@Composable
private fun HeroChip(icon: ImageVector, text: String) {
    val tint = Color.White.copy(alpha = 0.95f)
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .clip(MaterialTheme.shapes.small)
            .background(Color.White.copy(alpha = 0.13f))
            .padding(horizontal = 10.dp, vertical = 6.dp),
    ) {
        Icon(icon, contentDescription = null, tint = tint, modifier = Modifier.size(15.dp))
        Spacer(Modifier.width(6.dp))
        Text(
            text = text,
            style = MaterialTheme.typography.labelMedium,
            color = tint,
            maxLines = 1,
        )
    }
}

@Composable
private fun HeroStat(label: String, value: String, modifier: Modifier = Modifier) {
    Column(
        modifier = modifier
            .clip(MaterialTheme.shapes.medium)
            .background(Color.White.copy(alpha = 0.13f))
            .padding(horizontal = 12.dp, vertical = 9.dp),
    ) {
        Text(
            text = label.uppercase(),
            style = MaterialTheme.typography.labelSmall.copy(letterSpacing = 0.8.sp),
            color = Color.White.copy(alpha = 0.72f),
        )
        Text(
            text = value,
            style = MaterialTheme.typography.titleMedium,
            color = Color.White,
            maxLines = 1,
        )
    }
}

@Composable
private fun AppCard(tile: HubTile, status: AppState?) {
    Surface(
        onClick = tile.onClick ?: {},
        enabled = tile.onClick != null,
        shape = MaterialTheme.shapes.large,
        color = MaterialTheme.colorScheme.surfaceVariant,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        modifier = Modifier.fillMaxWidth(),
    ) {
        if (tile.wide) {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.padding(14.dp),
            ) {
                IconTile(tile.icon)
                Spacer(Modifier.width(13.dp))
                Column(Modifier.weight(1f)) {
                    Text(tile.name, style = MaterialTheme.typography.titleMedium)
                    CardSubtitle(tile.subtitle)
                }
                Icon(
                    Icons.Rounded.ChevronRight,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.outline,
                    modifier = Modifier.size(20.dp),
                )
            }
        } else {
            Column(modifier = Modifier.padding(14.dp)) {
                Row(
                    horizontalArrangement = Arrangement.SpaceBetween,
                    verticalAlignment = Alignment.Top,
                    modifier = Modifier.fillMaxWidth(),
                ) {
                    IconTile(tile.icon)
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        tile.badgeCount?.let {
                            CountBadge(it)
                            Spacer(Modifier.width(6.dp))
                        }
                        if (tile.statusApp != null) StatusDot(status)
                    }
                }
                Spacer(Modifier.height(9.dp))
                Text(
                    text = tile.name,
                    style = MaterialTheme.typography.titleMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
                CardSubtitle(tile.subtitle)
            }
        }
    }
}

/**
 * The single greyed card: dashed border, muted throughout, and the one
 * "coming soon" badge in the app.
 */
@Composable
private fun ComingSoonCard(tile: HubTile) {
    val border = MaterialTheme.colorScheme.outlineVariant
    val corner = 22.dp
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clip(MaterialTheme.shapes.large)
            .background(MaterialTheme.colorScheme.surfaceContainerLow)
            .drawBehind {
                drawRoundRect(
                    color = border,
                    style = Stroke(
                        width = 1.5.dp.toPx(),
                        pathEffect = PathEffect.dashPathEffect(floatArrayOf(12f, 9f)),
                    ),
                    cornerRadius = CornerRadius(corner.toPx()),
                )
            }
            .padding(14.dp),
    ) {
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(44.dp)
                .clip(MaterialTheme.shapes.medium)
                .background(MaterialTheme.colorScheme.surfaceContainerHigh),
        ) {
            Icon(
                tile.icon,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.outline,
                modifier = Modifier.size(25.dp),
            )
        }
        Spacer(Modifier.width(13.dp))
        Column(Modifier.weight(1f)) {
            Text(
                text = tile.name,
                style = MaterialTheme.typography.titleMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            CardSubtitle(tile.subtitle)
        }
        Text(
            text = stringResource(R.string.coming_soon).uppercase(),
            style = MaterialTheme.typography.labelSmall.copy(letterSpacing = 0.5.sp),
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier
                .clip(MaterialTheme.shapes.extraSmall)
                .background(MaterialTheme.colorScheme.surfaceContainerHigh)
                .padding(horizontal = 10.dp, vertical = 6.dp),
        )
    }
}

/** 44dp icon plate: the app's mark on the palest blue in the palette. */
@Composable
private fun IconTile(icon: ImageVector) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(44.dp)
            .clip(MaterialTheme.shapes.medium)
            .background(MaterialTheme.colorScheme.primaryContainer),
    ) {
        Icon(
            icon,
            contentDescription = null,
            tint = MaterialTheme.colorScheme.primary,
            modifier = Modifier.size(25.dp),
        )
    }
}

/** Unread messages on the SkyChat tile: a filled pill beside the dot. */
@Composable
private fun CountBadge(count: Int) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .clip(CircleShape)
            .background(MaterialTheme.colorScheme.primary)
            .padding(horizontal = 7.dp, vertical = 2.dp),
    ) {
        Text(
            text = if (count > MAX_BADGE_COUNT) "$MAX_BADGE_COUNT+" else count.toString(),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onPrimary,
        )
    }
}

/**
 * The card's status dot with a soft halo: green running, amber starting,
 * error red errored, and the border grey of "not running" otherwise —
 * including whenever the core itself is down and no status exists.
 */
@Composable
private fun StatusDot(status: AppState?) {
    val color = when {
        status == null -> MaterialTheme.colorScheme.outlineVariant
        status.running -> SkyAccents.success
        status.status == AppState.STATUS_STARTING -> SkyAccents.warning
        status.status == AppState.STATUS_ERRORED -> MaterialTheme.colorScheme.error
        else -> MaterialTheme.colorScheme.outlineVariant
    }
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(16.dp)
            .background(color.copy(alpha = 0.18f), CircleShape),
    ) {
        Box(
            Modifier
                .size(8.dp)
                .background(color, CircleShape),
        )
    }
}

@Composable
private fun CardSubtitle(text: String) {
    Text(
        text = text,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        maxLines = 1,
        overflow = TextOverflow.Ellipsis,
        modifier = Modifier.padding(top = 2.dp),
    )
}

/** Enough of an exit key to tell two exits in one country apart. */
private const val EXIT_PK_PREFIX = 6

/** Past this the badge reads "99+" — the message is "a lot", not a number. */
private const val MAX_BADGE_COUNT = 99

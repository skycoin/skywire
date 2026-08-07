package com.skycoin.skywire.ui

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.interaction.MutableInteractionSource
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.automirrored.rounded.Chat
import androidx.compose.material.icons.outlined.AccountBalanceWallet
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material.icons.rounded.AccountBalanceWallet
import androidx.compose.material.icons.rounded.Home
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Surface
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.NavType
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.navArgument
import com.skycoin.skywire.R
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.core.VoiceCalls
import com.skycoin.skywire.ui.call.CallScreen
import com.skycoin.skywire.ui.chat.ChatScreen
import com.skycoin.skywire.ui.dex.DexScreen
import com.skycoin.skywire.ui.fleet.FleetScreen
import com.skycoin.skywire.ui.home.HomeScreen
import com.skycoin.skywire.ui.hub.HubScreen
import com.skycoin.skywire.ui.logs.LogSources
import com.skycoin.skywire.ui.logs.LogViewerScreen
import com.skycoin.skywire.ui.navigation.Routes
import com.skycoin.skywire.ui.components.PulseRing
import com.skycoin.skywire.ui.settings.DiagnosticsScreen
import com.skycoin.skywire.ui.settings.SettingsScreen
import com.skycoin.skywire.ui.theme.SkyButtonGradient
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.ui.socks.SocksScreen
import com.skycoin.skywire.ui.vpn.VpnScreen
import com.skycoin.skywire.ui.wallet.WalletAddCoinScreen
import com.skycoin.skywire.ui.wallet.WalletHistoryScreen
import com.skycoin.skywire.ui.wallet.WalletManageScreen
import com.skycoin.skywire.ui.wallet.WalletReceiveScreen
import com.skycoin.skywire.ui.wallet.WalletRestoreScreen
import com.skycoin.skywire.ui.wallet.WalletResultScreen
import com.skycoin.skywire.ui.wallet.WalletRevealScreen
import com.skycoin.skywire.ui.wallet.WalletScreen
import com.skycoin.skywire.ui.wallet.WalletSeedScreen
import com.skycoin.skywire.ui.wallet.WalletSendScreen
import com.skycoin.skywire.ui.wallet.WalletTxScreen
import com.skycoin.skywire.ui.wallet.WalletVerifyScreen
import com.skycoin.skywire.ui.wallet.WalletViewModel

/**
 * One of the four labelled bottom-bar slots. The fifth — the raised Skycoin
 * cloud in the middle, which opens the apps hub — is not a slot: it is its
 * own shape, drawn over the bar in [SkyNavBar].
 */
private data class BarSlot(
    val route: String,
    val label: String,
    /** Outlined at rest, its Rounded (filled) form when the tab is current. */
    val idleIcon: ImageVector,
    val activeIcon: ImageVector,
)

@Composable
fun SkywireApp() {
    val navController = rememberNavController()

    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentRoute = backStackEntry?.destination?.route

    // A skychat link opened from elsewhere belongs on the Chat tab. Only the
    // navigation happens here — the link stays pending until the chat surface
    // is up and has shown it, which can be a while after the tab is on screen
    // (the core may still be connecting).
    val chatLink by DeepLinks.pendingChatLink.collectAsState()
    LaunchedEffect(chatLink) {
        if (chatLink != null && currentRoute != Routes.CHAT) {
            navController.navigateToTab(Routes.CHAT)
        }
    }

    // A connected call needs the microphone, and a call can be answered from
    // the notification with no screen in front of the user — so the ask
    // happens here, app-wide, the moment there IS one. Until it is granted the
    // call runs receive-only rather than failing, and the capture loop picks
    // the grant up whenever it lands.
    val context = LocalContext.current
    // One ViewModel for the whole wallet flow, scoped to the Activity — the
    // freshly generated seed and the send draft must survive route changes
    // inside the flow without ever riding in navigation arguments.
    val walletViewModel: WalletViewModel = viewModel()
    val inCall by VoiceCalls.state.collectAsState()
    val micPermission = rememberLauncherForActivityResult(
        ActivityResultContracts.RequestPermission(),
    ) { /* Granted or not, the call carries on; the engine re-checks. */ }
    LaunchedEffect(inCall.inCall) {
        val granted = ContextCompat.checkSelfPermission(
            context,
            Manifest.permission.RECORD_AUDIO,
        ) == PackageManager.PERMISSION_GRANTED
        if (inCall.inCall && !granted) micPermission.launch(Manifest.permission.RECORD_AUDIO)
    }

    // A call owns the whole screen, in either direction and on every tab —
    // the Chat tab included. The embedded page draws its own banner and panel
    // underneath, but a call is not a thing to notice inside a list: it is
    // what the phone is doing.
    if (inCall.busy) {
        CallScreen()
        return
    }

    Scaffold(
        bottomBar = {
            SkyNavBar(
                currentRoute = currentRoute,
                onSelect = { navController.navigateToTab(it) },
            )
        },
    ) { innerPadding ->
        NavHost(
            navController = navController,
            startDestination = Routes.HOME,
            // consumeWindowInsets so a destination can still ask for the
            // insets it needs — the Chat WebView's imePadding would otherwise
            // count the navigation bar twice, once here and once in the
            // keyboard inset it is measured from.
            modifier = Modifier
                .padding(innerPadding)
                .consumeWindowInsets(innerPadding),
        ) {
            composable(Routes.HOME) {
                HomeScreen(
                    onOpenLogs = { source ->
                        // singleTop: a double tap must not stack two viewers
                        // (the hidden one would keep polling the API).
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }
            composable(Routes.CHAT) {
                ChatScreen(onBack = { navController.navigateToTab(Routes.HOME) })
            }
            composable(Routes.HUB) {
                HubScreen(
                    onBack = { navController.navigateToTab(Routes.HOME) },
                    onOpenRoute = { navController.navigate(it) },
                    onOpenTab = { navController.navigateToTab(it) },
                )
            }
            // The wallet flow shares one ViewModel across its routes — the
            // freshly generated seed and the send draft live in it, never in
            // navigation arguments.
            composable(Routes.WALLET) {
                WalletScreen(
                    viewModel = walletViewModel,
                    onBack = { navController.navigateToTab(Routes.HOME) },
                    onCreate = { navController.navigate(Routes.WALLET_CREATE) { launchSingleTop = true } },
                    onRestore = { navController.navigate(Routes.WALLET_RESTORE) { launchSingleTop = true } },
                    onReceive = { navController.navigate(Routes.WALLET_RECEIVE) { launchSingleTop = true } },
                    onSend = { navController.navigate(Routes.WALLET_SEND) { launchSingleTop = true } },
                    onHistory = { navController.navigate(Routes.WALLET_HISTORY) { launchSingleTop = true } },
                    onTx = { txid -> navController.navigate(Routes.walletTx(txid)) { launchSingleTop = true } },
                    onWallets = { navController.navigate(Routes.WALLET_WALLETS) { launchSingleTop = true } },
                    onAddCoin = { navController.navigate(Routes.WALLET_ADD_COIN) { launchSingleTop = true } },
                )
            }
            composable(Routes.WALLET_CREATE) {
                WalletSeedScreen(
                    viewModel = walletViewModel,
                    onContinue = { navController.navigate(Routes.WALLET_VERIFY) { launchSingleTop = true } },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_VERIFY) {
                WalletVerifyScreen(
                    viewModel = walletViewModel,
                    onActivated = { navController.popBackStack(Routes.WALLET, inclusive = false) },
                    onShowSeed = { navController.popBackStack() },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_RESTORE) {
                WalletRestoreScreen(
                    viewModel = walletViewModel,
                    onRestored = { navController.popBackStack(Routes.WALLET, inclusive = false) },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_RECEIVE) {
                WalletReceiveScreen(
                    viewModel = walletViewModel,
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_SEND) {
                WalletSendScreen(
                    viewModel = walletViewModel,
                    onSent = {
                        navController.navigate(Routes.WALLET_RESULT) {
                            launchSingleTop = true
                            popUpTo(Routes.WALLET) { inclusive = false }
                        }
                    },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_RESULT) {
                WalletResultScreen(
                    viewModel = walletViewModel,
                    onDone = { navController.popBackStack(Routes.WALLET, inclusive = false) },
                    onHistory = {
                        navController.navigate(Routes.WALLET_HISTORY) {
                            launchSingleTop = true
                            popUpTo(Routes.WALLET) { inclusive = false }
                        }
                    },
                )
            }
            composable(Routes.WALLET_HISTORY) {
                WalletHistoryScreen(
                    viewModel = walletViewModel,
                    onTx = { txid -> navController.navigate(Routes.walletTx(txid)) { launchSingleTop = true } },
                    onReceive = { navController.navigate(Routes.WALLET_RECEIVE) { launchSingleTop = true } },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(
                Routes.WALLET_TX,
                arguments = listOf(navArgument("txid") { type = NavType.StringType }),
            ) { entry ->
                WalletTxScreen(
                    viewModel = walletViewModel,
                    txid = entry.arguments?.getString("txid") ?: "",
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_WALLETS) {
                WalletManageScreen(
                    viewModel = walletViewModel,
                    onAddWallet = { navController.navigate(Routes.WALLET_CREATE) { launchSingleTop = true } },
                    onRestoreWallet = { navController.navigate(Routes.WALLET_RESTORE) { launchSingleTop = true } },
                    onReveal = { id -> navController.navigate(Routes.walletReveal(id)) { launchSingleTop = true } },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(
                Routes.WALLET_REVEAL,
                arguments = listOf(navArgument("walletId") { type = NavType.StringType }),
            ) { entry ->
                WalletRevealScreen(
                    viewModel = walletViewModel,
                    walletId = entry.arguments?.getString("walletId") ?: "",
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.WALLET_ADD_COIN) {
                WalletAddCoinScreen(
                    viewModel = walletViewModel,
                    onAdded = { navController.popBackStack(Routes.WALLET, inclusive = false) },
                    onBack = { navController.popBackStack() },
                )
            }
            composable(Routes.SETTINGS) {
                SettingsScreen(
                    onBack = { navController.navigateToTab(Routes.HOME) },
                    onOpenDiagnostics = {
                        navController.navigate(Routes.DIAGNOSTICS) { launchSingleTop = true }
                    },
                )
            }
            composable(Routes.DIAGNOSTICS) {
                DiagnosticsScreen(
                    onBack = { navController.popBackStack() },
                    onOpenLogs = { source ->
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }

            // Full-screen routes pushed from the hub — back returns to the hub.
            composable(Routes.SOCKS) {
                SocksScreen(onBack = { navController.popBackStack() })
            }
            composable(Routes.VPN) {
                VpnScreen(onBack = { navController.popBackStack() })
            }
            composable(Routes.DEX) {
                DexScreen(onBack = { navController.popBackStack() })
            }
            composable(Routes.FLEET) {
                FleetScreen(
                    onBack = { navController.popBackStack() },
                    onOpenLogs = { source ->
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }

            // One log viewer, reached from Home and (later) every app screen.
            composable(
                Routes.LOGS,
                arguments = listOf(navArgument("source") { type = NavType.StringType }),
            ) { entry ->
                LogViewerScreen(
                    source = entry.arguments?.getString("source") ?: LogSources.CORE,
                    onBack = { navController.popBackStack() },
                )
            }
        }
    }
}

/**
 * Standard bottom-bar navigation: single top, state saved/restored per tab.
 * Also used by the hub's SkyChat/Wallet tiles so they land on the *same*
 * destinations as the Chat/Wallet tabs (one screen, two entry points).
 */
private fun NavHostController.navigateToTab(route: String) {
    navigate(route) {
        popUpTo(graph.findStartDestination().id) { saveState = true }
        launchSingleTop = true
        restoreState = true
    }
}

/**
 * The floating bottom bar: a rounded shell with two labelled slots on each
 * side, and the Skycoin cloud on a raised circular button in the middle —
 * the one slot people aim for by shape rather than by reading, so it gets a
 * shape instead of a label. It opens the apps hub.
 *
 * The cloud button rides [LIFT] above the shell via [offset], which moves
 * drawing but not measurement — the jut lands on top of the screen's content,
 * which is what a raised button is. Scaffold places the bottom bar last, so
 * it wins the overlap.
 */
@Composable
private fun SkyNavBar(currentRoute: String?, onSelect: (String) -> Unit) {
    val slots = listOf(
        BarSlot(Routes.HOME, stringResource(R.string.tab_home), Icons.Outlined.Home, Icons.Rounded.Home),
        BarSlot(Routes.CHAT, stringResource(R.string.tab_chat), Icons.AutoMirrored.Outlined.Chat, Icons.AutoMirrored.Rounded.Chat),
        BarSlot(Routes.WALLET, stringResource(R.string.tab_wallet), Icons.Outlined.AccountBalanceWallet, Icons.Rounded.AccountBalanceWallet),
        BarSlot(Routes.SETTINGS, stringResource(R.string.tab_settings), Icons.Outlined.Settings, Icons.Rounded.Settings),
    )
    val selected = { slot: BarSlot ->
        currentRoute == slot.route ||
            (slot.route == Routes.SETTINGS && currentRoute in Routes.settingsPushed) ||
            (slot.route == Routes.WALLET && currentRoute in Routes.walletPushed)
    }
    val shellColor = MaterialTheme.colorScheme.surfaceContainerLowest

    Box(
        modifier = Modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(start = 12.dp, end = 12.dp, bottom = 8.dp),
    ) {
        Surface(
            shape = MaterialTheme.shapes.extraLarge,
            color = shellColor,
            border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
            shadowElevation = 8.dp,
            modifier = Modifier.fillMaxWidth(),
        ) {
            Row(Modifier.height(BAR_HEIGHT)) {
                NavSlot(slots[0], selected(slots[0]), onSelect, Modifier.weight(1f))
                NavSlot(slots[1], selected(slots[1]), onSelect, Modifier.weight(1f))
                Spacer(Modifier.weight(1f)) // the cloud's column
                NavSlot(slots[2], selected(slots[2]), onSelect, Modifier.weight(1f))
                NavSlot(slots[3], selected(slots[3]), onSelect, Modifier.weight(1f))
            }
        }
        CloudButton(
            ringColor = shellColor,
            onClick = { onSelect(Routes.HUB) },
            modifier = Modifier
                .align(Alignment.TopCenter)
                .offset(y = -LIFT),
        )
    }
}

@Composable
private fun NavSlot(
    slot: BarSlot,
    selected: Boolean,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
) {
    val tint = if (selected) {
        MaterialTheme.colorScheme.primary
    } else {
        MaterialTheme.colorScheme.outline
    }
    // Icon only — the label lives in the content description. Four glyphs
    // and the cloud read cleaner than four glyphs with four captions.
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.Center,
        modifier = modifier
            .fillMaxHeight()
            // No bounded ripple: the shell is one rounded surface and four
            // rectangular flashes inside it read as seams.
            .clickable(
                interactionSource = remember { MutableInteractionSource() },
                indication = null,
                onClick = { onSelect(slot.route) },
            ),
    ) {
        Icon(
            imageVector = if (selected) slot.activeIcon else slot.idleIcon,
            contentDescription = slot.label,
            tint = tint,
            modifier = Modifier.size(26.dp),
        )
    }
}

/**
 * The raised cloud: brand gradient under the Skycoin mark, a ring of the
 * shell's own color so it reads as punched *through* the bar, and a slow
 * pulse behind it — the bar's one piece of motion, on the one control that
 * is always worth a look.
 */
@Composable
private fun CloudButton(
    ringColor: Color,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    val hubLabel = stringResource(R.string.tab_hub_description)

    Box(contentAlignment = Alignment.Center, modifier = modifier) {
        PulseRing(size = CLOUD_SIZE, color = MaterialTheme.colorScheme.primary)
        Box(
            contentAlignment = Alignment.Center,
            modifier = Modifier
                .size(CLOUD_SIZE)
                .clip(CircleShape)
                .background(ringColor)
                .clickable(onClick = onClick)
                .padding(4.dp)
                .clip(CircleShape)
                .background(SkyButtonGradient),
        ) {
            Icon(
                painter = painterResource(R.drawable.skywire_logo),
                contentDescription = hubLabel,
                tint = Color.White,
                // The mark is a 4:3 cloud; 34×26 keeps it inside the 58dp
                // gradient disc with the ring's weight around it.
                modifier = Modifier.size(34.dp),
            )
        }
    }
}

private val BAR_HEIGHT = 64.dp
private val CLOUD_SIZE = 66.dp

/** How far the cloud rides above the shell — mock's 0.32 × its size. */
private val LIFT = 21.dp

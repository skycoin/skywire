package com.skycoin.skywire.ui

import android.Manifest
import android.content.pm.PackageManager
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.outlined.Chat
import androidx.compose.material.icons.outlined.AccountBalanceWallet
import androidx.compose.material.icons.outlined.Home
import androidx.compose.material.icons.outlined.Settings
import androidx.compose.material3.Icon
import androidx.compose.material3.LocalContentColor
import androidx.compose.material3.NavigationBar
import androidx.compose.material3.NavigationBarItem
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.painter.Painter
import androidx.compose.ui.graphics.vector.rememberVectorPainter
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
import com.skycoin.skywire.ui.settings.SettingsScreen
import com.skycoin.skywire.ui.socks.SocksScreen
import com.skycoin.skywire.ui.vpn.VpnScreen
import com.skycoin.skywire.ui.wallet.WalletScreen

/**
 * Main scaffold: bottom NavigationBar with 5 slots (left→right):
 * Home · Chat · Skycoin logo (apps hub, icon-only) · Wallet · Settings.
 * Bar items and hub tiles take a [Painter] so the designed logos drop in
 * later with zero layout change.
 */
private data class BarSlot(
    val route: String,
    val label: String?, // null = icon-only (the center logo slot)
    val icon: @Composable () -> Painter,
    val isLogo: Boolean = false,
)

@Composable
fun SkywireApp() {
    val navController = rememberNavController()
    val slots = listOf(
        BarSlot(Routes.HOME, stringResource(R.string.tab_home), { rememberVectorPainter(Icons.Outlined.Home) }),
        BarSlot(Routes.CHAT, stringResource(R.string.tab_chat), { rememberVectorPainter(Icons.AutoMirrored.Outlined.Chat) }),
        // Center slot: the Skycoin cloud only — no label — opens the apps hub.
        BarSlot(Routes.HUB, null, { painterResource(R.drawable.skywire_logo) }, isLogo = true),
        BarSlot(Routes.WALLET, stringResource(R.string.tab_wallet), { rememberVectorPainter(Icons.Outlined.AccountBalanceWallet) }),
        BarSlot(Routes.SETTINGS, stringResource(R.string.tab_settings), { rememberVectorPainter(Icons.Outlined.Settings) }),
    )

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
            NavigationBar {
                slots.forEach { slot ->
                    NavigationBarItem(
                        selected = currentRoute == slot.route ||
                            (slot.route == Routes.HUB && currentRoute in Routes.hubPushed),
                        onClick = { navController.navigateToTab(slot.route) },
                        icon = {
                            Icon(
                                painter = slot.icon(),
                                contentDescription = slot.label
                                    ?: stringResource(R.string.tab_hub_description),
                                // Logo slot: slightly enlarged and UNtinted — the
                                // brand cloud keeps its color; the selection pill
                                // alone signals the active state. Other slots get
                                // the bar's default selected/unselected tint.
                                modifier = Modifier.size(if (slot.isLogo) 44.dp else 24.dp),
                                tint = if (slot.isLogo) Color.Unspecified
                                else LocalContentColor.current,
                            )
                        },
                        label = slot.label?.let { { Text(it) } },
                    )
                }
            }
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
                ChatScreen(
                    onOpenLogs = { source ->
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }
            composable(Routes.HUB) {
                HubScreen(
                    onOpenRoute = { navController.navigate(it) },
                    onOpenTab = { navController.navigateToTab(it) },
                )
            }
            composable(Routes.WALLET) { WalletScreen() }
            composable(Routes.SETTINGS) { SettingsScreen() }

            // Full-screen routes pushed from the hub — back returns to the hub.
            composable(Routes.SOCKS) {
                SocksScreen(
                    onBack = { navController.popBackStack() },
                    onOpenLogs = { source ->
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }
            composable(Routes.VPN) {
                VpnScreen(
                    onBack = { navController.popBackStack() },
                    onOpenLogs = { source ->
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }
            composable(Routes.DEX) {
                DexScreen(
                    onBack = { navController.popBackStack() },
                    onOpenLogs = { source ->
                        navController.navigate(Routes.logs(source)) { launchSingleTop = true }
                    },
                )
            }
            composable(Routes.FLEET) { FleetScreen(onBack = { navController.popBackStack() }) }

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

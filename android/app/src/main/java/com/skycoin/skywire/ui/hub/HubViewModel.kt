package com.skycoin.skywire.ui.hub

import android.app.Application
import android.net.VpnService
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.AppConnection
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.Overview
import com.skycoin.skywire.api.SkychatApi
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.Fleet
import com.skycoin.skywire.core.SkyVpnService
import com.skycoin.skywire.core.SkychatProfile
import com.skycoin.skywire.ui.components.RateSampler
import com.skycoin.skywire.ui.components.SavedServer
import com.skycoin.skywire.ui.vpn.VpnArgs
import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.skywire.wallet.WalletRepository
import com.skycoin.wallet.Amounts
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.json.Json

/** Everything the apps hub renders that is alive rather than declared. */
data class HubUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The local API answered — before that every status below is unknown. */
    val apiUp: Boolean = false,
    /** The visor's client apps by process name, from the local summary. */
    val apps: Map<String, AppState> = emptyMap(),
    /** SkyVPN's live connection while it runs — the byte totals ride on it. */
    val vpnConnection: AppConnection? = null,
    /**
     * Measured here from the connection's byte counters rather than read off
     * the visor's own speed fields, which never leave zero — see [RateSampler].
     * Null until two samples exist.
     */
    val vpnRates: RateSampler.Rates? = null,
    /** The exit SkyVPN points at (or last pointed at), if any. */
    val vpnExitPk: String? = null,
    /** [vpnExitPk]'s country code, when the phone knows it. */
    val vpnCountry: String? = null,
    /** Last summary overview — the device's own address comes off this. */
    val overview: Overview? = null,
    /** The phone's killswitch preference — armed or not. */
    val killswitch: Boolean = false,
    /** Minimum route hops the visor is dialling with; 1 allows direct. */
    val minHops: Int = 0,
    /** The hero card's toggle is mid-flight. */
    val vpnBusy: Boolean = false,
    /** Messages waiting in SkyChat, as skychat itself counts them. */
    val unreadMessages: Int = 0,
    /** The active SKY wallet's cached balance, formatted; null = no wallet. */
    val skyBalance: String? = null,
    /** The Fleet opt-in, so the tile says nothing extra while it is off. */
    val fleetEnabled: Boolean = false,
    /** Remote visors currently connected in; null until the first count. */
    val fleetOnline: Int? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    val runningCount: Int get() = apps.values.count { it.running }

    val vpn: AppState? get() = apps[VpnArgs.APP]

    /** The hero's switch state: the tunnel is up or on its way up. */
    val vpnOn: Boolean
        get() = vpn?.let { it.running || it.status == AppState.STATUS_STARTING } == true
}

/**
 * Feeds the apps hub: one local-summary poll for every app's status (the
 * subtitle's "· N running" and the tiles' dots), plus SkyVPN's connection
 * while it runs, for the hero card's rates. All local API — nothing here
 * crosses dmsg, so the poll is cheap.
 */
class HubViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val prefs = AppPreferences(app)
    private val skychat = SkychatApi.get(app)
    private val wallet = WalletRepository.get(app)
    private val json = Json { ignoreUnknownKeys = true }

    private val mutable = MutableStateFlow(HubUiState())
    val uiState: StateFlow<HubUiState> = mutable.asStateFlow()

    /** Last exit the SkyVPN screen saved — country label and re-dial ride on it. */
    private var lastServer: SavedServer? = null

    private val rates = RateSampler()

    init {
        // The killswitch is the phone's preference, so it reads the same with
        // the core down — which is exactly when someone checks whether they
        // are still protected.
        viewModelScope.launch {
            prefs.boolean(VpnArgs.PREF_KILLSWITCH).collect { on ->
                mutable.update { it.copy(killswitch = on) }
            }
        }
        viewModelScope.launch {
            prefs.boolean(Fleet.PREF_KEY, Fleet.DEFAULT).collect { on ->
                mutable.update {
                    it.copy(fleetEnabled = on, fleetOnline = if (on) it.fleetOnline else null)
                }
            }
        }
        viewModelScope.launch {
            lastServer = prefs.string(VpnArgs.PREF_LAST_SERVER).first()?.let { stored ->
                runCatching { json.decodeFromString(SavedServer.serializer(), stored) }.getOrNull()
            }
            mutable.update { withExit(it) }
            CoreServiceState.state.collectLatest { core ->
                rates.reset()
                mutable.update {
                    withExit(
                        it.copy(
                            coreState = core,
                            apiUp = false,
                            apps = emptyMap(),
                            vpnConnection = null,
                            vpnRates = null,
                        ),
                    )
                }
                if (core is CoreState.Running) {
                    while (!api.ping()) delay(PING_INTERVAL_MS)
                    mutable.update { it.copy(apiUp = true) }
                    // Visor-wide and rarely changed, so it is read once per
                    // core lifetime rather than on every poll.
                    runCatching { api.routerSettings().minHops }.getOrNull()?.let { hops ->
                        mutable.update { it.copy(minHops = hops) }
                    }
                    poll()
                }
            }
        }
    }

    /**
     * Turn SkyVPN on from the hero card — but only when nothing about it
     * needs a screen: the system consent already granted, an exit already
     * chosen. Returns false otherwise, and the caller opens the SkyVPN
     * screen, which owns both of those conversations.
     */
    fun startVpn(): Boolean {
        val state = mutable.value
        if (!state.coreReady || state.vpnBusy) return false
        val pk = state.vpnExitPk ?: return false
        if (VpnService.prepare(getApplication()) != null) return false

        viewModelScope.launch {
            mutable.update { it.copy(vpnBusy = true) }
            runCatching {
                val killswitch = prefs.boolean(VpnArgs.PREF_KILLSWITCH).first()
                // The service first: vpn-client asks it for a descriptor
                // mid-handshake and must find it listening. Then the
                // unconditional stop — a stale proc would race the new one.
                SkyVpnService.start(getApplication(), killswitch)
                runCatching { api.updateApp(VpnArgs.APP, status = VisorApi.APP_STOP) }
                val updated = api.updateApp(
                    VpnArgs.APP,
                    pk = pk,
                    killswitch = killswitch,
                    status = VisorApi.APP_START,
                )
                mutable.update { withExit(it.copy(apps = it.apps + (VpnArgs.APP to updated))) }
            }
            mutable.update { it.copy(vpnBusy = false) }
        }
        return true
    }

    /**
     * Turn SkyVPN off from the hero card. The mirror of the SkyVPN screen's
     * own disconnect: stop the app, then take the interface away — deliberate,
     * so the killswitch does not trap the phone afterwards.
     */
    fun stopVpn() {
        viewModelScope.launch {
            mutable.update { it.copy(vpnBusy = true) }
            val stopped = runCatching {
                api.updateApp(VpnArgs.APP, status = VisorApi.APP_STOP)
            }.getOrNull()
            SkyVpnService.stop(getApplication())
            mutable.update { state ->
                withExit(
                    state.copy(
                        vpnBusy = false,
                        vpnConnection = null,
                        apps = stopped?.let { state.apps + (VpnArgs.APP to it) } ?: state.apps,
                    ),
                )
            }
        }
    }

    /** Runs until the core-state collector cancels it. */
    private suspend fun poll() {
        // Counting the fleet fires a Summary RPC to every remote over dmsg,
        // which routinely outlasts this loop's own cadence — so it runs on
        // FleetViewModel's slower one, folded in as every Nth pass.
        var fleetCountdown = 0
        while (true) {
            runCatching {
                val overview = api.summary().overview
                val apps = overview.apps.associateBy { it.name }
                val vpn = apps[VpnArgs.APP]
                val connection = if (vpn?.running == true) {
                    runCatching { api.appConnections(VpnArgs.APP) }
                        .getOrDefault(emptyList())
                        .firstOrNull()
                } else {
                    null
                }
                // A gone connection ends the series: the next one starts its
                // counters from zero and a delta across that is nonsense.
                val sampled = if (connection == null) {
                    rates.reset()
                    null
                } else {
                    rates.sample(connection.bandwidthSent, connection.bandwidthReceived)
                }
                mutable.update {
                    withExit(
                        it.copy(
                            overview = overview,
                            apps = apps,
                            vpnConnection = connection,
                            // Keep the last good reading between samples
                            // rather than blinking back to "—".
                            vpnRates = sampled ?: it.vpnRates.takeIf { connection != null },
                        ),
                    )
                }
            }
            // The tiles' own numbers, each behind its own failure boundary —
            // a chat probe failing must not cost the hero card its rates.
            runCatching { pollBadges() }
            if (mutable.value.fleetEnabled && fleetCountdown-- <= 0) {
                fleetCountdown = FLEET_EVERY_N_POLLS - 1
                runCatching { pollFleetCount() }
            }
            delay(POLL_INTERVAL_MS)
        }
    }

    /**
     * The SkyChat badge and the Wallet tile's balance. The unread number is
     * skychat's own — the page reports what it shows, the server carries it
     * while no page is open — and the balance is the wallet cache's, because
     * the wallet tab owns talking to the node.
     */
    private suspend fun pollBadges() {
        val chat = mutable.value.apps[SkychatProfile.APP]
        val unread = if (chat?.running == true) {
            skychat.unread(SkychatProfile.baseUrl(SkychatProfile.listenPort(chat.args)))
                ?: mutable.value.unreadMessages
        } else {
            0
        }
        val balance = withContext(Dispatchers.IO) {
            wallet.activeWalletId(CoinSpec.SKY.id).first()
                ?.let { wallet.cachedSnapshot(it) }
                ?.let { Amounts.format(it.confirmed, CoinSpec.SKY.exponent, 0) }
        }
        mutable.update { it.copy(unreadMessages = unread, skyBalance = balance) }
    }

    /** Remote visors currently connected in, the Fleet screen's own way. */
    private suspend fun pollFleetCount() {
        val local = api.localPk()
        val online = api.visorsSummary()
            .filterNot { it.isHypervisor || it.overview.localPk == local }
            .count { it.online }
        mutable.update { it.copy(fleetOnline = online) }
    }

    /**
     * Recompute the exit line from wherever the truth currently is: the
     * app's own args once the visor has them, the saved server before that.
     * The country only ever belongs to the same key it was saved with — the
     * flag must never belong to a different exit than the one shown.
     */
    private fun withExit(state: HubUiState): HubUiState {
        val pk = state.vpn?.let { VpnArgs.serverPk(it.args) } ?: lastServer?.pk
        val country = lastServer?.takeIf { it.pk == pk }?.country?.takeIf { it.isNotEmpty() }
        return state.copy(vpnExitPk = pk, vpnCountry = country)
    }

    private companion object {
        const val PING_INTERVAL_MS = 700L
        const val POLL_INTERVAL_MS = 5_000L

        /**
         * Fleet count cadence, in polls: 3 × 5 s matches FleetViewModel's
         * 15 s, which is what the Summary-RPC-per-remote fan-out tolerates.
         */
        const val FLEET_EVERY_N_POLLS = 3
    }
}

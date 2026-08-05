package com.skycoin.skywire.ui.dex

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.MarketStatus
import com.skycoin.skywire.api.SkydexApi
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SkydexProfile
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.IOException

/** Everything the SkyDEX screen renders. */
data class DexUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The visor's local API answered — nothing starts before it does. */
    val apiUp: Boolean = false,
    val app: AppState? = null,
    /** The trading UI answered here; null while it hasn't. */
    val uiUrl: String? = null,
    /** Secret the WebView answers the gate's challenge with — see [SkydexProfile]. */
    val password: String? = null,
    /** skydex-client's own view of its market — the page can change it too. */
    val market: MarketStatus? = null,
    val recents: List<SavedMarket> = emptyList(),
    /** Text in the market-key field, kept here so it survives a rotation. */
    val entry: String = "",
    /** A configure/start/connect round trip is in flight. */
    val busy: Boolean = false,
    val error: String? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    /**
     * The WebView is worth showing exactly when there is a market behind it.
     * Not merely "the app is up": the page's own screen at that point is a
     * second connect form, which is the one thing the native header exists to
     * replace.
     */
    val connected: Boolean get() = uiUrl != null && market?.connected == true

    val entryValid: Boolean get() = isMarketPk(entry.trim())
}

/**
 * Drives the SkyDEX flow: point skydex-client at a market, start it, dial the
 * market, and hand the screen a URL for the trading UI.
 *
 * Two servers are involved and they answer different questions. The visor
 * (`:8000`) owns the app — its argv, and whether it runs. skydex-client's own
 * control API (`:8051`) owns the market connection, because the engine dials
 * only when asked: `--market-pk` alone would leave the user on the page's
 * connect form with the key already filled in, which is two steps for what the
 * desktop flow does in one.
 *
 * The market state is polled rather than remembered — the embedded page keeps
 * a Disconnect button of its own, and a header claiming "connected" over a
 * page that isn't would be worse than no header at all.
 */
class DexViewModel(app: Application) : AndroidViewModel(app) {

    private val visor = VisorApi.get(app)
    private val skydex = SkydexApi.get(app)
    private val prefs = AppPreferences(app)
    private val json = Json { ignoreUnknownKeys = true }

    private val mutable = MutableStateFlow(DexUiState())
    val uiState: StateFlow<DexUiState> = mutable.asStateFlow()

    private var actionJob: Job? = null

    /**
     * The field is filled in from what the phone already knows — the last
     * market, or the one in the argv — but only until the user has seen it
     * once. Refilling on a later poll would fight whoever is typing.
     */
    private var prefilled = false

    init {
        viewModelScope.launch {
            val secret = skydex.password()
            mutable.update { it.copy(password = secret) }
        }
        viewModelScope.launch {
            val saved = readRecents()
            mutable.update { state ->
                state.copy(
                    recents = saved,
                    entry = state.entry.ifEmpty { saved.firstOrNull()?.pk.orEmpty() },
                )
            }
            if (saved.isNotEmpty()) prefilled = true
        }
        viewModelScope.launch {
            CoreServiceState.state.collectLatest { core ->
                // Everything below is derived from a live visor: a restart
                // takes the app's listener with it, so the WebView must be
                // torn down rather than left pointed at a dead port.
                mutable.update {
                    it.copy(coreState = core, apiUp = false, app = null, uiUrl = null, market = null)
                }
                if (core !is CoreState.Running) return@collectLatest
                while (!visor.ping()) delay(PING_INTERVAL_MS)
                mutable.update { it.copy(apiUp = true) }
                poll()
            }
        }
    }

    fun setEntry(value: String) {
        prefilled = true
        mutable.update { it.copy(entry = value, error = null) }
    }

    fun pickRecent(market: SavedMarket) {
        prefilled = true
        mutable.update { it.copy(entry = market.pk, error = null) }
    }

    /**
     * Point skydex-client at the key in the field, make sure it runs, and dial
     * the market.
     *
     * The stop is unconditional and its failure ignored, for the reason the
     * SkySOCKS screen found the hard way: `status: 1` on an app the visor
     * already considers started is a 500, and the polled snapshot can be a
     * poll behind. With the app reliably stopped the single PUT below is valid
     * either way — the args just rewrite the argv (the server-side restart it
     * triggers is a no-op with no proc running), then the status starts it
     * with the new key in place.
     */
    fun connect() = action {
        val pk = mutable.value.entry.trim()
        if (!isMarketPk(pk)) throw IOException("That is not a market public key.")

        val current = visor.app(SkydexProfile.APP)
        runCatching { visor.updateApp(SkydexProfile.APP, status = VisorApi.APP_STOP) }
        val started = visor.updateApp(
            SkydexProfile.APP,
            args = DexArgs.withMarketPk(current.args, pk),
            status = VisorApi.APP_START,
        )
        mutable.update { it.copy(app = started) }

        val url = SkydexProfile.baseUrl(SkydexProfile.listenPort(started.args))
        if (!awaitUi(url)) throw IOException("SkyDEX did not answer on $url")

        val market = skydex.connect(url, pk)
        saveRecent(SavedMarket(pk, market.marketName))
        mutable.update { it.copy(uiUrl = url, market = market) }
    }

    /**
     * Drop the market and stop the app. Stopping is deliberate: SkyDEX has no
     * background duty — the engine holds no connection while idle — so leaving
     * a trading UI listening on the phone would be a surface kept open for
     * nothing.
     */
    fun disconnect() = action {
        mutable.value.uiUrl?.let { skydex.disconnect(it) }
        val stopped = runCatching {
            visor.updateApp(SkydexProfile.APP, status = VisorApi.APP_STOP)
        }.getOrNull()
        mutable.update { it.copy(app = stopped ?: it.app, uiUrl = null, market = null) }
    }

    // --- internals ---

    /**
     * One user action at a time, with its failure surfaced on the screen.
     * Launched on the view-model scope rather than inside the state collector,
     * which is cancelled the moment the core state changes.
     */
    private fun action(block: suspend () -> Unit) {
        actionJob?.cancel()
        actionJob = viewModelScope.launch {
            mutable.update { it.copy(busy = true, error = null) }
            try {
                block()
            } catch (e: Exception) {
                mutable.update { it.copy(error = e.message) }
            } finally {
                mutable.update { it.copy(busy = false) }
            }
        }
    }

    /** Runs until the core-state collector cancels it. */
    private suspend fun poll() {
        while (true) {
            try {
                val app = visor.app(SkydexProfile.APP)
                // One call answers both questions the screen has: an app that
                // reports its market is by definition an app whose UI is up.
                val url = SkydexProfile.baseUrl(SkydexProfile.listenPort(app.args))
                val market = if (app.running) skydex.status(url) else null
                mutable.update {
                    it.copy(app = app, uiUrl = if (market != null) url else null, market = market)
                }
                val configured = DexArgs.marketPk(app.args)
                if (!prefilled && configured != null) {
                    prefilled = true
                    mutable.update { it.copy(entry = configured) }
                }
            } catch (e: Exception) {
                mutable.update { it.copy(error = e.message) }
            }
            delay(POLL_INTERVAL_MS)
        }
    }

    /** Wait for the trading UI's listener; ~15 s, as skychat's screen does. */
    private suspend fun awaitUi(url: String): Boolean {
        repeat(READY_ATTEMPTS) {
            if (skydex.probe(url)) return true
            delay(READY_INTERVAL_MS)
        }
        return false
    }

    private suspend fun readRecents(): List<SavedMarket> =
        prefs.string(KEY_RECENTS).first()?.let { stored ->
            runCatching {
                json.decodeFromString(ListSerializer(SavedMarket.serializer()), stored)
            }.getOrNull()
        }.orEmpty()

    /** Most recent first, deduplicated by key, oldest beyond [MAX_RECENTS] dropped. */
    private suspend fun saveRecent(market: SavedMarket) {
        val updated = (listOf(market) + mutable.value.recents.filterNot { it.pk == market.pk })
            .take(MAX_RECENTS)
        prefs.putString(
            KEY_RECENTS,
            json.encodeToString(ListSerializer(SavedMarket.serializer()), updated),
        )
        mutable.update { it.copy(recents = updated) }
    }

    private companion object {
        const val KEY_RECENTS = "dex_recent_markets"
        const val MAX_RECENTS = 5
        const val PING_INTERVAL_MS = 700L
        const val POLL_INTERVAL_MS = 2_000L
        const val READY_INTERVAL_MS = 500L

        /** ~15 s: an in-proc app binds its listener in well under a second. */
        const val READY_ATTEMPTS = 30
    }
}

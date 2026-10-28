package com.skycoin.skywire.ui.dex

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.api.AppState
import com.skycoin.skywire.api.DexCreatedListing
import com.skycoin.skywire.api.DexListing
import com.skycoin.skywire.api.DexOrder
import com.skycoin.skywire.api.DexOrderStatus
import com.skycoin.skywire.api.DexProduct
import com.skycoin.skywire.api.DexPurchase
import com.skycoin.skywire.api.MarketStatus
import com.skycoin.skywire.api.SkydexApi
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.core.CoreServiceState
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.SkydexProfile
import com.skycoin.skywire.dex.DexLegacyImport
import com.skycoin.skywire.dex.DexLocal
import com.skycoin.skywire.dex.DexStore
import com.skycoin.skywire.dex.SKY
import com.skycoin.skywire.dex.mergeHistory
import kotlinx.coroutines.Job
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.distinctUntilChanged
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.IOException

enum class DexTab { MARKET, ORDERS, LISTINGS, HISTORY, SETTINGS }

/** The outcome under the Settings form: saved, or what went wrong. */
data class WalletsMessage(val ok: Boolean, val text: String)

/** Everything the SkyDEX screens render. */
data class DexUiState(
    val coreState: CoreState = CoreState.Stopped,
    /** The visor's local API answered — nothing starts before it does. */
    val apiUp: Boolean = false,
    val app: AppState? = null,
    /** skydex-client's control API answered here; null while it hasn't. */
    val uiUrl: String? = null,
    /** skydex-client's own view of its market. */
    val market: MarketStatus? = null,
    val recents: List<SavedMarket> = emptyList(),
    /** Text in the market-key field, kept here so it survives a rotation. */
    val entry: String = "",
    /** A configure/start/connect round trip is in flight. */
    val busy: Boolean = false,
    val error: String? = null,

    val tab: DexTab = DexTab.MARKET,
    val currencies: List<String> = emptyList(),
    val sellCoins: List<String> = emptyList(),
    /** Whether the market has a sell coin enabled; null until it has answered. */
    val ready: Boolean? = null,
    /** Null until the first answer, so "nothing yet" is not "nothing at all". */
    val products: List<DexProduct>? = null,
    val orders: List<DexOrder>? = null,
    /** Live get_order_status answers for in-flight buys, by order id. */
    val statuses: Map<String, DexOrderStatus> = emptyMap(),
    val listings: List<DexListing>? = null,
    /** The last poll of the open tab that failed; the next good one clears it. */
    val tradeError: String? = null,
    /** Why the last buy, listing or cancel was refused; kept until the next action or tab. */
    val actionError: String? = null,
    /** Deposit instructions from the last listing created, kept until dismissed. */
    val deposit: DexCreatedListing? = null,
    /** Payment instructions from the last buy, kept until dismissed. */
    val payment: DexPurchase? = null,
    /** A buy or a new listing is in flight. */
    val working: Boolean = false,
    /** The order or listing whose cancel is in flight. */
    val busyId: String? = null,
    val savingWallets: Boolean = false,
    val walletsMessage: WalletsMessage? = null,
) {
    val coreReady: Boolean get() = coreState is CoreState.Running && apiUp

    /** The trading screens show exactly when there is a market behind them. */
    val connected: Boolean get() = uiUrl != null && market?.connected == true

    val entryValid: Boolean get() = isMarketPk(entry.trim())
}

/**
 * Drives SkyDEX: point skydex-client at a market, start it, dial the market,
 * then trade through skydex-client's control API.
 *
 * Two servers are involved. The visor (`:8000`) owns the app — its argv and
 * whether it runs. skydex-client's own API (`:8051`) owns the market
 * connection and proxies every trading call to the market over dmsg; the
 * market stays the source of truth for orders and identity. What the web page
 * kept in localStorage lives in [DexStore].
 */
class DexViewModel(app: Application) : AndroidViewModel(app) {

    private val visor = VisorApi.get(app)
    private val skydex = SkydexApi.get(app)
    private val prefs = AppPreferences(app)
    private val store = DexStore.get(app)
    private val json = Json { ignoreUnknownKeys = true }

    private val mutable = MutableStateFlow(DexUiState())
    val uiState: StateFlow<DexUiState> = mutable.asStateFlow()

    /** Wallet addresses and trade history kept on this phone. */
    val local: StateFlow<DexLocal> = store.state

    private val noticeFlow = MutableSharedFlow<String>(extraBufferCapacity = 4)

    /** One-off messages for the snackbar: a cancel went through, a copy was made. */
    val notices: SharedFlow<String> = noticeFlow

    /** Polling runs only while the screen is on, as the page's timers did. */
    private val visible = MutableStateFlow(false)

    private var actionJob: Job? = null

    /**
     * The field is filled in from what the phone already knows — the last
     * market, or the one in the argv — but only until the user has seen it
     * once. Refilling on a later poll would fight whoever is typing.
     */
    private var prefilled = false

    init {
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
                // takes the app's listener with it.
                mutable.update {
                    it.copy(coreState = core, apiUp = false, app = null, uiUrl = null, market = null)
                }
                if (core !is CoreState.Running) return@collectLatest
                while (!visor.ping()) delay(PING_INTERVAL_MS)
                mutable.update { it.copy(apiUp = true) }
                poll()
            }
        }
        viewModelScope.launch {
            // A new market, or the same one after a reconnect, starts from a clean slate.
            uiState.map { if (it.connected) it.uiUrl to it.market?.marketPk else null }
                .distinctUntilChanged()
                .combine(visible) { target, on -> if (on) target else null }
                .collectLatest { target ->
                    if (target == null) return@collectLatest
                    tradeLoop(target.first ?: return@collectLatest)
                }
        }
    }

    fun setVisible(on: Boolean) {
        visible.value = on
    }

    fun setEntry(value: String) {
        prefilled = true
        mutable.update { it.copy(entry = value, error = null) }
    }

    fun selectTab(tab: DexTab) {
        mutable.update { it.copy(tab = tab, tradeError = null, actionError = null, walletsMessage = null) }
    }

    /** Connect straight to a market from the recent list. */
    fun connectTo(market: SavedMarket) {
        prefilled = true
        mutable.update { it.copy(entry = market.pk, error = null) }
        connect()
    }

    fun removeRecent(market: SavedMarket) {
        viewModelScope.launch { writeRecents(mutable.value.recents.filterNot { it.pk == market.pk }) }
    }

    fun clearRecents() {
        viewModelScope.launch { writeRecents(emptyList()) }
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
        val context = getApplication<Application>()
        val pk = mutable.value.entry.trim()
        if (!isMarketPk(pk)) {
            throw IOException(context.getString(R.string.dex_error_invalid_key))
        }

        val current = visor.app(SkydexProfile.APP)
        runCatching { visor.updateApp(SkydexProfile.APP, status = VisorApi.APP_STOP) }
        val started = visor.updateApp(
            SkydexProfile.APP,
            args = DexArgs.withMarketPk(current.args, pk),
            status = VisorApi.APP_START,
        )
        mutable.update { it.copy(app = started) }

        val url = SkydexProfile.baseUrl(SkydexProfile.listenPort(started.args))
        if (!awaitUi(url)) {
            throw IOException(context.getString(R.string.dex_error_no_answer, url))
        }
        importLegacy(url)

        val market = skydex.connect(url, pk)
        saveRecent(SavedMarket(pk, market.marketName, System.currentTimeMillis()))
        mutable.update { it.copy(uiUrl = url, market = market, tab = DexTab.MARKET) }
    }

    /**
     * Drop the market and stop the app. Stopping is deliberate: SkyDEX has no
     * background duty — the engine holds no connection while idle — so leaving
     * a trading API listening on the phone would be a surface kept open for
     * nothing.
     */
    fun disconnect() = action {
        mutable.value.uiUrl?.let { skydex.disconnect(it) }
        val stopped = runCatching {
            visor.updateApp(SkydexProfile.APP, status = VisorApi.APP_STOP)
        }.getOrNull()
        mutable.update { it.copy(app = stopped ?: it.app, uiUrl = null, market = null) }
    }

    // --- trading ---

    fun buy(product: DexProduct) = trade {
        if (!store.state.value.registered) {
            needRegister()
            return@trade
        }
        val bought = skydex.buy(base(), product.id)
        mutable.update { it.copy(payment = bought) }
        refreshProducts()
    }

    fun createListing(sellCoin: String, amount: Double, price: Double, paymentCurrency: String, onDone: () -> Unit) = trade {
        if (!store.state.value.registered) {
            needRegister()
            return@trade
        }
        val created = skydex.createListing(base(), sellCoin, amount, price, paymentCurrency)
        mutable.update { it.copy(deposit = created) }
        onDone()
    }

    fun cancelOrder(order: DexOrder) = cancel(order.id) {
        val reply = skydex.cancelOrder(base(), order.id)
        notice(reply.message.ifEmpty { text(R.string.dex_order_canceled) })
        refreshOrders()
    }

    fun cancelListing(listing: DexListing) = cancel(listing.id) {
        val reply = skydex.cancelListing(base(), listing.id)
        notice(reply.message.ifEmpty { text(R.string.dex_offer_canceled) })
        val fresh = skydex.listings(base())
        mutable.update { it.copy(listings = fresh) }
    }

    fun dismissDeposit() = mutable.update { it.copy(deposit = null) }

    fun dismissPayment() = mutable.update { it.copy(payment = null) }

    /** Register the addresses with the market, then remember them here (Settings.jsx). */
    fun saveWallets(sky: String, payouts: Map<String, String>) {
        val skyAddr = sky.trim()
        if (skyAddr.isEmpty()) {
            mutable.update { it.copy(walletsMessage = WalletsMessage(false, text(R.string.dex_sky_required))) }
            return
        }
        val wallets = payouts.mapValues { it.value.trim() }.filterValues { it.isNotEmpty() }
        viewModelScope.launch {
            mutable.update { it.copy(savingWallets = true, walletsMessage = null) }
            try {
                skydex.register(base(), skyAddr, wallets)
                store.update { it.copy(wallets = mapOf(SKY to skyAddr) + wallets) }
                mutable.update { it.copy(walletsMessage = WalletsMessage(true, text(R.string.dex_wallets_saved))) }
            } catch (e: Exception) {
                mutable.update { it.copy(walletsMessage = WalletsMessage(false, e.message.orEmpty())) }
            } finally {
                mutable.update { it.copy(savingWallets = false) }
            }
        }
    }

    fun removeHistory(order: DexOrder) = store.update { s -> s.copy(history = s.history.filterNot { it.id == order.id && it.type == order.type }) }

    fun clearHistory() = store.update { it.copy(history = emptyList()) }

    fun notice(message: String) {
        noticeFlow.tryEmit(message)
    }

    // --- internals ---

    /** Polls what the open tab shows, plus the market's readiness, until the target changes. */
    private suspend fun tradeLoop(url: String) {
        mutable.update {
            it.copy(products = null, orders = null, listings = null, statuses = emptyMap(), ready = null, tradeError = null)
        }
        coroutineScope {
            launch {
                while (true) {
                    refreshReadiness(url)
                    delay(READINESS_INTERVAL_MS)
                }
            }
            launch {
                uiState.map { it.tab }.distinctUntilChanged().collectLatest { tab ->
                    while (true) {
                        try {
                            when (tab) {
                                DexTab.MARKET -> refreshProducts()
                                DexTab.ORDERS, DexTab.HISTORY -> refreshOrders()
                                DexTab.LISTINGS -> mutable.update { it.copy(listings = skydex.listings(url), tradeError = null) }
                                DexTab.SETTINGS -> Unit
                            }
                        } catch (e: Exception) {
                            if (e is kotlinx.coroutines.CancellationException) throw e
                            mutable.update { it.copy(tradeError = if (redial(e)) null else e.message) }
                        }
                        delay(if (tab == DexTab.HISTORY) HISTORY_INTERVAL_MS else TRADE_INTERVAL_MS)
                    }
                }
            }
        }
    }

    private suspend fun refreshReadiness(url: String) {
        runCatching { skydex.currencies(url) }.onSuccess { c ->
            val sell = c.sellCoins.orEmpty()
            mutable.update { it.copy(sellCoins = sell, currencies = c.currencies.orEmpty(), ready = sell.isNotEmpty()) }
            val pk = mutable.value.market?.marketPk.orEmpty()
            if (c.marketName.isNotEmpty() && pk.isNotEmpty()) {
                mutable.update { s -> s.copy(market = s.market?.copy(marketName = c.marketName)) }
                val known = mutable.value.recents.firstOrNull { it.pk == pk }
                if (known?.name != c.marketName) saveRecent(SavedMarket(pk, c.marketName, known?.ts ?: System.currentTimeMillis()))
            }
        }
    }

    private suspend fun refreshProducts() {
        mutable.update { it.copy(products = skydex.products(base()), tradeError = null) }
    }

    /** Orders, the live status of in-flight buys, and finished ones folded into History. */
    private suspend fun refreshOrders() {
        val url = base()
        val list = skydex.orders(url).distinctBy { it.type to it.id }
        val tracked = list.filter { it.type == "buy" && it.status in IN_FLIGHT_STATUSES }
        val live = coroutineScope {
            tracked.map { o -> async { runCatching { skydex.orderStatus(url, o.id) }.getOrNull() } }.awaitAll()
        }.filterNotNull().associateBy { it.orderId }
        // Keep a live status only while its order is in flight; after that the list is the truth.
        val trackedIds = tracked.map { it.id }.toSet()
        mutable.update { it.copy(orders = list, statuses = it.statuses.filterKeys { id -> id in trackedIds } + live, tradeError = null) }
        val market = mutable.value.market
        store.update { s ->
            s.copy(history = mergeHistory(s.history, list, market?.marketPk.orEmpty(), market?.marketName.orEmpty(), System.currentTimeMillis()))
        }
    }

    /**
     * skydex-client keeps a market connection whose dmsg stream has closed, and
     * every call then fails. Dial the same market again, at most every 20 s.
     */
    private suspend fun redial(e: Exception): Boolean {
        if (!DROPPED.containsMatchIn(e.message.orEmpty())) return false
        val url = mutable.value.uiUrl ?: return false
        val pk = mutable.value.market?.marketPk?.takeIf { it.isNotEmpty() } ?: return false
        val now = System.currentTimeMillis()
        if (now - lastRedial < REDIAL_GAP_MS) return false
        lastRedial = now
        return runCatching { skydex.connect(url, pk) }
            .onSuccess { m -> mutable.update { s -> s.copy(market = m.copy(marketName = m.marketName.ifEmpty { s.market?.marketName.orEmpty() })) } }
            .isSuccess
    }

    private var lastRedial = 0L

    private fun base(): String = mutable.value.uiUrl ?: throw IOException(text(R.string.dex_error_not_connected))

    private fun needRegister() {
        mutable.update { it.copy(tab = DexTab.SETTINGS, walletsMessage = WalletsMessage(false, text(R.string.dex_banner_register_title))) }
    }

    /** A buy or a listing: one at a time, its failure shown on the tab. "Register first" opens Settings. */
    private fun trade(block: suspend () -> Unit) {
        viewModelScope.launch {
            mutable.update { it.copy(working = true, actionError = null) }
            try {
                block()
            } catch (e: Exception) {
                val message = e.message.orEmpty()
                mutable.update { it.copy(actionError = message) }
                if (REGISTER.containsMatchIn(message)) mutable.update { it.copy(tab = DexTab.SETTINGS) }
                if (redial(e)) notice(text(R.string.dex_reconnected))
            } finally {
                mutable.update { it.copy(working = false) }
            }
        }
    }

    private fun cancel(id: String, block: suspend () -> Unit) {
        viewModelScope.launch {
            mutable.update { it.copy(busyId = id, actionError = null) }
            try {
                block()
            } catch (e: Exception) {
                mutable.update { it.copy(actionError = e.message) }
                if (redial(e)) notice(text(R.string.dex_reconnected))
            } finally {
                mutable.update { it.copy(busyId = null) }
            }
        }
    }

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
                // reports its market is by definition an app whose API is up.
                val url = SkydexProfile.baseUrl(SkydexProfile.listenPort(app.args))
                val market = if (app.running) skydex.status(url) else null
                mutable.update { s ->
                    // Keep a name learned from get_currencies over an empty one.
                    val merged = market?.let { m -> if (m.marketName.isEmpty() && m.marketPk == s.market?.marketPk) m.copy(marketName = s.market.marketName) else m }
                    s.copy(app = app, uiUrl = if (market != null) url else null, market = merged)
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

    /** Wait for skydex-client's listener; ~15 s, as skychat's screen does. */
    private suspend fun awaitUi(url: String): Boolean {
        repeat(READY_ATTEMPTS) {
            if (skydex.probe(url)) return true
            delay(READY_INTERVAL_MS)
        }
        return false
    }

    /** Once, the first time skydex-client is up: what the old WebView page kept. */
    private suspend fun importLegacy(url: String) {
        if (store.state.value.legacyImported) return
        val app = getApplication<Application>()
        if (!DexLegacyImport.hasWebViewData(app)) {
            store.update { it.copy(legacyImported = true) }
            return
        }
        val storage = DexLegacyImport.read(app, url, skydex.password()) ?: return
        val legacy = DexLegacyImport.parse(storage)
        store.update { s ->
            s.copy(
                wallets = if (s.wallets.isEmpty()) legacy.wallets else s.wallets,
                history = (s.history + legacy.history.filter { old -> s.history.none { it.id == old.id && it.type == old.type } }),
                legacyImported = true,
            )
        }
        // Markets already listed here take the page's name and last-used time when they lack them.
        val known = mutable.value.recents.map { k ->
            val old = legacy.markets.firstOrNull { it.pk == k.pk } ?: return@map k
            k.copy(name = k.name.ifEmpty { old.name }, ts = if (k.ts > 0) k.ts else old.ts)
        }
        val imported = legacy.markets.filter { m -> known.none { it.pk == m.pk } }.map { SavedMarket(it.pk, it.name, it.ts) }
        if (imported.isNotEmpty() || known != mutable.value.recents) writeRecents((known + imported).sortedByDescending { it.ts })
    }

    private suspend fun readRecents(): List<SavedMarket> =
        prefs.string(KEY_RECENTS).first()?.let { stored ->
            runCatching {
                json.decodeFromString(ListSerializer(SavedMarket.serializer()), stored)
            }.getOrNull()
        }.orEmpty()

    /** Most recent first, deduplicated by key, a previously learned name kept. */
    private suspend fun saveRecent(market: SavedMarket) {
        val prev = mutable.value.recents.firstOrNull { it.pk == market.pk }
        val entry = market.copy(name = market.name.ifEmpty { prev?.name.orEmpty() })
        writeRecents(listOf(entry) + mutable.value.recents.filterNot { it.pk == market.pk })
    }

    private suspend fun writeRecents(list: List<SavedMarket>) {
        val trimmed = list.take(MAX_RECENTS)
        prefs.putString(KEY_RECENTS, json.encodeToString(ListSerializer(SavedMarket.serializer()), trimmed))
        mutable.update { it.copy(recents = trimmed) }
    }

    private fun text(id: Int): String = getApplication<Application>().getString(id)

    private companion object {
        const val KEY_RECENTS = "dex_recent_markets"
        const val MAX_RECENTS = 12
        const val PING_INTERVAL_MS = 700L
        const val POLL_INTERVAL_MS = 2_000L
        const val READY_INTERVAL_MS = 500L
        const val TRADE_INTERVAL_MS = 5_000L
        const val HISTORY_INTERVAL_MS = 8_000L
        const val READINESS_INTERVAL_MS = 8_000L

        /** ~15 s: an in-proc app binds its listener in well under a second. */
        const val READY_ATTEMPTS = 30

        /** The market's "register first" refusal, which the page turned into a jump to Settings. */
        val REGISTER = Regex("register", RegexOption.IGNORE_CASE)

        /** A market connection whose stream is gone, as skydex-client words it. */
        val DROPPED = Regex("stream closed|session shutdown|closed pipe|broken pipe|connection reset|\\bEOF\\b", RegexOption.IGNORE_CASE)
        const val REDIAL_GAP_MS = 20_000L
    }
}

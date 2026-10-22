package com.skycoin.skywire.ui.wallet

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.core.AppPreferences
import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.skywire.wallet.WalletMeta
import com.skycoin.skywire.wallet.WalletRepository
import com.skycoin.skywire.wallet.WalletPrices
import com.skycoin.skywire.wallet.WalletSnapshot
import com.skycoin.skywire.wallet.coinTextFromUsd
import com.skycoin.wallet.Amounts
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.Derivation
import com.skycoin.wallet.ExtendedPublicKeys
import com.skycoin.wallet.FeeLevel
import com.skycoin.wallet.FeePresets
import com.skycoin.wallet.NodeInfo
import com.skycoin.wallet.Recipient
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.UtxoRecord
import com.skycoin.wallet.WalletException
import com.skycoin.wallet.skycoin.ChainInfo
import com.skycoin.wallet.skycoin.PeerInfo
import com.skycoin.wallet.skycoin.PendingInfo
import kotlinx.coroutines.CancellationException
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.collectLatest
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.IOException
import java.math.BigDecimal
import java.security.SecureRandom

/** Where the send flow stands; the review sheet and result screen read this. */
data class SendState(
    val to: String = "",
    val amountText: String = "",
    val sendMax: Boolean = false,
    /** The one address the send spends from; null = the whole wallet. */
    val fromAddress: String? = null,
    /** Unspent outputs picked to fund the send; empty = automatic. */
    val inputs: Set<String> = emptySet(),
    /** The unspent outputs as last loaded for the picker. */
    val utxos: List<UtxoRecord> = emptyList(),
    /** Hours for the recipient, digits only; blank = automatic split. */
    val hoursText: String = "",
    /** Destinations after the first. Fiber only. */
    val extra: List<DestDraft> = emptyList(),
    /** Where change goes; blank keeps the automatic choice. Fiber only. */
    val changeAddress: String = "",
    /** Percent of post-burn hours automatic hours send onward, 10 to 100. Fiber only. */
    val sharePercent: Int = 50,
    /** The tip offered. Ethereum family only. */
    val ethLevel: FeeLevel = FeeLevel.NORMAL,
    /** The amount field holds US dollars, converted at the current price. */
    val usdMode: Boolean = false,
    val feeRate: Int = 12,
    val presets: FeePresets? = null,
    val planning: Boolean = false,
    val plan: TxPlan? = null,
    val planError: String? = null,
    val sending: Boolean = false,
    val sentTxid: String? = null,
    val sentAmount: ULong = 0u,
    val sentTo: String = "",
    val sentCount: Int = 1,
)

/** The create/restore flow in progress — the phrase lives only here, in memory. */
data class DraftState(
    val coin: CoinSpec = CoinSpec.SKY,
    val seed: String? = null,
    val quizPositions: List<Int> = emptyList(),
    /** The kind of wallet being made. Fiber coins offer BIP 44 besides their classic wallet. */
    val derivation: Derivation = Derivation.STANDARD,
    /** The optional BIP 39 passphrase; held in memory only until the wallet is saved. */
    val passphrase: String = "",
)

/** Four flows, one carrier — Kotlin ships Triple and stops. */
private data class Quad<A, B, C, D>(val a: A, val b: B, val c: C, val d: D)

data class WalletUiState(
    val ready: Boolean = false,
    val coins: List<CoinSpec> = emptyList(),
    val coin: CoinSpec = CoinSpec.SKY,
    val allWallets: List<WalletMeta> = emptyList(),
    /** Wallets of the selected coin. */
    val coinWallets: List<WalletMeta> = emptyList(),
    val active: WalletMeta? = null,
    val snapshot: WalletSnapshot? = null,
    /** True when the freshest fetch failed and the screen shows old numbers. */
    val stale: Boolean = false,
    val refreshing: Boolean = false,
    val restoring: Boolean = false,
    /** Index into the active wallet's receive addresses shown on Receive. */
    val receiveIndex: Int = 0,
    val send: SendState = SendState(),
    val draft: DraftState = DraftState(),
    /**
     * The node address [coin] would use with nothing set — what the Node
     * screen offers to go back to, and what it compares against to know
     * whether the address on screen is the user's or the shipped one.
     */
    val defaultNodeUrl: String = "",
    /** The user's node addresses for this coin, in priority order. */
    val nodeUrls: List<String> = emptyList(),
    /** The same for the Ethereum family's history indexer; null for coins without one. */
    val defaultIndexerUrl: String? = null,
    val message: String? = null,
    /** History shows only transactions this address of the wallet took part in; null shows all. */
    val historyAddress: String? = null,
    /** The coin's node as it last answered; null before it has, or when it did not. */
    val node: NodeInfo? = null,
    /** False when the last node check got no answer. */
    val nodeReachable: Boolean = true,
    /** The user turned USD prices on; they are fetched only then. */
    val pricesEnabled: Boolean = false,
    /** One [coin] in US dollars, when prices are on and the coin has one. */
    val usdPrice: Double? = null,
)

class WalletViewModel(app: Application) : AndroidViewModel(app) {

    private val repo = WalletRepository.get(app)
    private val prefs = AppPreferences(app)
    private val mutable = MutableStateFlow(WalletUiState())
    val uiState: StateFlow<WalletUiState> = mutable.asStateFlow()

    private var refreshJob: Job? = null
    private var actionJob: Job? = null

    init {
        viewModelScope.launch {
            prefs.boolean(WalletPrices.PREF_KEY).collectLatest { on ->
                mutable.update { it.copy(pricesEnabled = on, usdPrice = it.usdPrice.takeIf { _ -> on }) }
                if (on) refreshPrice()
            }
        }
        viewModelScope.launch {
            // All four flows read the same DataStore, so any wallet-store edit
            // re-runs this block with a consistent view.
            combine(
                repo.coins(),
                repo.selectedCoinId(),
                repo.wallets(),
                repo.defaultCoins(),
            ) { coins, selectedId, wallets, defaults ->
                Quad(coins, selectedId, wallets, defaults)
            }.collectLatest { (coins, selectedId, wallets, defaults) ->
                val coin = coins.firstOrNull { it.id == selectedId } ?: CoinSpec.SKY
                val coinWallets = wallets.filter { it.coinId == coin.id }
                val activeId = repo.activeWalletId(coin.id).first()
                val active = coinWallets.firstOrNull { it.id == activeId } ?: coinWallets.firstOrNull()
                val previous = mutable.value
                val sameWallet = previous.active?.id == active?.id
                mutable.update {
                    it.copy(
                        ready = true,
                        coins = coins,
                        coin = coin,
                        defaultNodeUrl = defaults[coin.id]?.nodeUrl ?: coin.nodeUrl,
                        nodeUrls = coin.nodeUrls.filterNot { it == defaults[coin.id]?.nodeUrl },
                        defaultIndexerUrl = defaults[coin.id]?.indexerUrl ?: coin.indexerUrl,
                        allWallets = wallets,
                        coinWallets = coinWallets,
                        active = active,
                        snapshot = active?.let { w -> repo.cachedSnapshot(w.id) } ?: it.snapshot.takeIf { _ -> sameWallet },
                        stale = if (sameWallet) it.stale else false,
                        historyAddress = it.historyAddress.takeIf { _ -> sameWallet },
                        receiveIndex = if (sameWallet) {
                            it.receiveIndex.coerceAtMost((active?.receiveAddresses?.size ?: 1) - 1).coerceAtLeast(0)
                        } else 0,
                    )
                }
                if (!sameWallet || refreshJob?.isActive != true) startRefreshing(active)
            }
        }
    }

    private fun startRefreshing(active: WalletMeta?) {
        refreshJob?.cancel()
        if (active == null) return
        refreshJob = viewModelScope.launch {
            while (true) {
                mutable.update { it.copy(refreshing = true) }
                try {
                    val snapshot = repo.refresh(active.id)
                    mutable.update {
                        if (it.active?.id == active.id) it.copy(snapshot = snapshot, stale = false, refreshing = false)
                        else it.copy(refreshing = false)
                    }
                    checkNode()
                    refreshPrice()
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // The screen renders the cached numbers with the stale
                    // banner; with no cache at all it says "not synced yet".
                    mutable.update {
                        if (it.active?.id == active.id) it.copy(stale = true, refreshing = false)
                        else it.copy(refreshing = false)
                    }
                    checkNode()
                    refreshPrice()
                }
                delay(REFRESH_INTERVAL_MS)
            }
        }
    }

    /** The node status line: asked with each refresh, as skycoin-web polls its node's health. */
    private suspend fun checkNode() {
        val coin = mutable.value.coin
        val info = try {
            repo.nodeStatus(coin)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            null
        }
        mutable.update { if (it.coin.id == coin.id) it.copy(node = info, nodeReachable = info != null) else it }
    }

    /** The coin's USD price, when the user turned prices on; a failed fetch keeps the last one. */
    private suspend fun refreshPrice() {
        val st = mutable.value
        val id = st.coin.priceId
        if (!st.pricesEnabled || id == null) {
            mutable.update { it.copy(usdPrice = null) }
            return
        }
        val price = try {
            WalletPrices.usd(id)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            null
        }
        mutable.update { if (it.coin.priceId == id && it.pricesEnabled) it.copy(usdPrice = price ?: it.usdPrice) else it }
    }

    fun refreshNow() {
        startRefreshing(mutable.value.active)
    }

    fun messageShown() = mutable.update { it.copy(message = null) }

    fun filterHistoryBy(address: String?) = mutable.update { it.copy(historyAddress = address) }

    private fun report(e: Exception): String {
        val app: Application = getApplication()
        // The exception's own message is kept verbatim — it comes from the
        // node or the core and is what gets pasted into an issue. Only the
        // fallbacks, written here, are translated. The last one falls back to
        // the class name, which stays a class name.
        return when (e) {
            is WalletException -> e.message ?: app.getString(R.string.wallet_error_rejected)
            is IOException -> e.message?.take(200) ?: app.getString(R.string.wallet_error_no_route)
            else -> e.message?.take(200) ?: e::class.java.simpleName
        }
    }

    private fun action(block: suspend () -> Unit) {
        actionJob?.cancel()
        actionJob = viewModelScope.launch {
            try {
                block()
            } catch (e: CancellationException) {
                throw e
            } catch (e: Exception) {
                mutable.update { it.copy(message = report(e), restoring = false) }
            }
        }
    }

    /**
     * Save the selected coin's node list — every add, remove and move lands
     * here at once, so the order on screen is the order the wallet asks in.
     * An empty list hands the coin back to its shipped address;
     * [indexerUrl] moves the Ethereum family's history indexer, null leaving
     * it as it is. The wallet re-reads immediately: the coin flow carries the
     * new addresses, which rebuilds the client under them.
     */
    /** Checks a node before it joins the selected coin's list: what it reports, or why it will not do. */
    suspend fun checkNode(url: String): Result<NodeInfo> = try {
        Result.success(repo.checkNode(mutable.value.coin, url))
    } catch (e: CancellationException) {
        throw e
    } catch (e: IllegalArgumentException) {
        Result.failure(e)
    } catch (e: Exception) {
        val app: Application = getApplication()
        Result.failure(IllegalStateException(app.getString(R.string.wallet_node_unreachable)))
    }

    fun saveNodeUrls(urls: List<String>, indexerUrl: String? = null) = action {
        repo.setNodeUrls(mutable.value.coin.id, urls, indexerUrl)
        mutable.update { it.copy(stale = false) }
        refreshNow()
    }

    // --- coin switching / Fibercoins ---

    fun selectCoin(coinId: String) = action {
        repo.setSelectedCoin(coinId)
        mutable.update { it.copy(send = SendState(), receiveIndex = 0, node = null, nodeReachable = true, usdPrice = null) }
    }

    /**
     * Copy a picked image into app storage for use as a coin badge and hand
     * back the stored name. Off the main thread — the source can be a large
     * photo that needs decoding and cropping.
     */
    fun importCoinIcon(uri: Uri, onSaved: (String) -> Unit) = action {
        val name = withContext(Dispatchers.IO) {
            importCoinIconFrom(getApplication(), uri)
        }
        onSaved(name)
    }

    /**
     * Remove a user-added coin. Refused for a built-in or while wallets still
     * hold it — the repository decides, and its message is what the sheet
     * shows, so the reason is the same wherever this is called from.
     */
    fun removeCoin(coinId: String) = action {
        val gone = repo.removeUserCoin(coinId) ?: return@action
        // The badge file was written by this layer (importCoinIcon), so it is
        // this layer that clears it up. Best-effort: a coin already out of the
        // list must not come back because a file would not delete.
        gone.icon?.let { name ->
            withContext(Dispatchers.IO) {
                runCatching { coinIconFile(getApplication(), name).delete() }
            }
        }
        val app: Application = getApplication()
        mutable.update {
            it.copy(message = app.getString(R.string.wallet_coin_removed, gone.name))
        }
    }

    fun addFiberCoin(name: String, ticker: String, nodeUrl: String, icon: String?, onDone: () -> Unit) = action {
        // A blank name or ticker comes from the node; the repository says when it cannot.
        val spec = repo.addFiberCoin(name, ticker, nodeUrl, icon)
        repo.setSelectedCoin(spec.id)
        onDone()
    }

    fun addErc20Token(
        name: String,
        ticker: String,
        contract: String,
        decimals: String,
        icon: String?,
        onDone: () -> Unit,
    ) = action {
        val app: Application = getApplication()
        require(name.isNotBlank()) { app.getString(R.string.wallet_add_token_name_required) }
        require(ticker.isNotBlank()) { app.getString(R.string.wallet_add_token_ticker_required) }
        val parsed = decimals.trim().toIntOrNull()
        requireNotNull(parsed) { app.getString(R.string.wallet_add_token_decimals_invalid) }
        val spec = repo.addErc20Token(name, ticker, contract, parsed, icon)
        repo.setSelectedCoin(spec.id)
        onDone()
    }

    // --- create / restore ---

    /** Begin the create flow for the selected coin: fresh phrase of [words] words + quiz picks. */
    fun startCreate(words: Int = 12, derivation: Derivation = Derivation.STANDARD, passphrase: String = "") {
        val coin = mutable.value.coin
        val seed = Bip39.newMnemonic(if (words == 24) 256 else 128)
        val rnd = SecureRandom()
        val positions = (1..seed.split(" ").size).toMutableList().let { pool ->
            List(3) { pool.removeAt(rnd.nextInt(pool.size)) }.sorted()
        }
        mutable.update { it.copy(draft = DraftState(coin, seed, positions, derivation, passphrase)) }
    }

    fun setDraftDerivation(derivation: Derivation) = mutable.update { it.copy(draft = it.draft.copy(derivation = derivation)) }

    fun setDraftPassphrase(passphrase: String) = mutable.update { it.copy(draft = it.draft.copy(passphrase = passphrase)) }

    fun startRestore() {
        mutable.update { it.copy(draft = DraftState(it.coin, null, emptyList())) }
    }

    /** Quiz answers checked; wrong positions returned (empty = activated). */
    fun submitQuiz(answers: Map<Int, String>, onActivated: () -> Unit): Map<Int, Boolean> {
        val draft = mutable.value.draft
        val seed = draft.seed ?: return emptyMap()
        val words = seed.split(" ")
        val wrong = draft.quizPositions.associateWith { pos ->
            answers[pos]?.trim()?.lowercase() != words[pos - 1]
        }.filterValues { it }
        if (wrong.isEmpty()) {
            action {
                val name = defaultWalletName(draft.coin)
                repo.addWallet(draft.coin, name, seed, restored = false, derivation = draft.derivation, passphrase = draft.passphrase)
                mutable.update { it.copy(draft = DraftState(), message = null) }
                onActivated()
            }
        }
        return wrong
    }

    /**
     * The phrase is validated by the restore screen before this is called.
     * [legacy] is a checksum-less phrase the user chose to restore anyway.
     */
    fun restoreWallet(seedText: String, legacy: Boolean = false, exact: Boolean = false, onDone: () -> Unit) {
        val draft = mutable.value.draft
        action {
            mutable.update { it.copy(restoring = true) }
            val name = defaultWalletName(draft.coin)
            repo.addWallet(
                draft.coin, name, seedText, restored = true, legacy = legacy,
                derivation = draft.derivation, passphrase = draft.passphrase, exact = exact,
            )
            mutable.update { it.copy(draft = DraftState(), restoring = false) }
            onDone()
        }
    }

    private suspend fun defaultWalletName(coin: CoinSpec): String {
        val count = repo.wallets().first().count { it.coinId == coin.id }
        return if (count == 0) coin.ticker else "${coin.ticker} ${count + 1}"
    }

    // --- receive ---

    fun pickReceiveIndex(index: Int) = mutable.update { it.copy(receiveIndex = index) }

    fun generateNewAddress(onDone: (String) -> Unit) = action {
        val active = mutable.value.active ?: return@action
        val addr = repo.newReceiveAddress(active.id)
        mutable.update { st ->
            st.copy(receiveIndex = (st.active?.receiveAddresses?.size ?: 1))
        }
        onDone(addr)
    }

    // --- send ---

    fun updateSend(transform: (SendState) -> SendState) =
        mutable.update { it.copy(send = transform(it.send)) }

    fun resetSend() = mutable.update { it.copy(send = SendState()) }

    // --- more destinations (fiber) ---

    fun addDestination() = updateSend { it.copy(extra = it.extra + DestDraft(), sendMax = false, plan = null) }

    fun updateDestination(index: Int, transform: (DestDraft) -> DestDraft) = updateSend { s ->
        s.copy(extra = s.extra.mapIndexed { i, d -> if (i == index) transform(d) else d }, plan = null, planError = null)
    }

    fun removeDestination(index: Int) = updateSend { s ->
        s.copy(extra = s.extra.filterIndexed { i, _ -> i != index }, plan = null, planError = null)
    }

    /** Bulk send replaces every destination, the first one included. */
    fun applyBulk(destinations: List<DestDraft>) = updateSend { s ->
        val first = destinations.first()
        s.copy(
            to = first.address,
            amountText = first.amountText,
            hoursText = first.hoursText,
            extra = destinations.drop(1),
            sendMax = false,
            plan = null,
            planError = null,
        )
    }

    // --- coin control (fiber) ---

    /** Loads the unspent outputs for the picker; keeps the last list on failure. */
    fun loadUtxos() {
        val active = mutable.value.active ?: return
        if (mutable.value.coin.kind != CoinKind.SKY_FIBER) return
        action {
            val list = runCatching { repo.utxos(active.id) }
                .getOrDefault(mutable.value.send.utxos)
            mutable.update { it.copy(send = it.send.copy(utxos = list)) }
        }
    }

    fun toggleInput(uxid: String) = updateSend { s ->
        s.copy(
            inputs = if (uxid in s.inputs) s.inputs - uxid else s.inputs + uxid,
            plan = null,
        )
    }

    fun clearInputs() = updateSend { it.copy(inputs = emptySet(), plan = null) }

    fun loadFeePresets() {
        val coin = mutable.value.coin
        if (coin.kind != CoinKind.BTC) return
        viewModelScope.launch {
            runCatching { repo.coreFor(coin).feePresets() }.getOrNull()?.let { presets ->
                mutable.update { st ->
                    st.copy(send = st.send.copy(presets = presets, feeRate = st.send.feeRate.coerceAtLeast(1)))
                }
            }
        }
    }

    /** The Max amount, fee-adjusted on Bitcoin, full balance elsewhere. */
    fun sendMaxAmount(): ULong {
        val st = mutable.value
        val snapshot = st.snapshot ?: return 0uL
        // Picked outputs cap the send at what they hold; a fiber fee burns
        // hours, not coins.
        if (st.send.inputs.isNotEmpty()) {
            return st.send.utxos.filter { it.uxid in st.send.inputs }.sumOf { it.coins }
        }
        // With a from-address chosen, Max is what that address holds. The
        // output count is an estimate on Bitcoin — the plan recomputes the
        // true fee from the real inputs either way.
        val from = st.send.fromAddress
        val balance = com.skycoin.wallet.WalletBalance(
            confirmed = from?.let { snapshot.perAddress[it] } ?: snapshot.confirmed,
            predicted = from?.let { snapshot.perAddress[it] } ?: snapshot.predicted,
            hours = snapshot.hours,
            spendableOutputs = if (from == null) snapshot.spendableOutputs else 1,
        )
        return repo.coreFor(st.coin).estimateMax(balance, st.send.feeRate)
    }

    /** Build the plan and open the review sheet on success. */
    fun buildPlan(onReady: () -> Unit) {
        val st = mutable.value
        val active = st.active ?: return
        val coin = st.coin
        val send = st.send
        val app: Application = getApplication()
        // In USD mode the field holds dollars; the plan is always in the coin.
        val coinText = if (send.usdMode) st.usdPrice?.let { coin.coinTextFromUsd(send.amountText, it) }.orEmpty() else send.amountText
        val amount = if (send.sendMax) 0uL
        else Amounts.parse(coinText, coin.exponent) ?: run {
            mutable.update {
                it.copy(send = send.copy(planError = app.getString(R.string.wallet_send_invalid_amount)))
            }
            return
        }
        val hoursTo = send.hoursText.trim().takeIf { it.isNotEmpty() }?.let { text ->
            text.toULongOrNull() ?: run {
                mutable.update {
                    it.copy(send = send.copy(planError = app.getString(R.string.wallet_send_invalid_hours)))
                }
                return
            }
        }
        val more = ArrayList<Recipient>()
        for ((i, d) in send.extra.withIndex()) {
            val coins = Amounts.parse(d.amountText, coin.exponent)?.takeIf { it > 0uL }
            val typedHours = d.hoursText.trim().takeIf { it.isNotEmpty() }
            val hours = typedHours?.toULongOrNull()
            val problem = when {
                coins == null -> app.getString(R.string.wallet_send_dest_invalid_amount, i + 2)
                typedHours != null && hours == null -> app.getString(R.string.wallet_send_invalid_hours)
                (hours == null) != (hoursTo == null) -> app.getString(R.string.wallet_send_hours_mixed)
                else -> null
            }
            if (problem != null || coins == null) {
                mutable.update { it.copy(send = send.copy(planError = problem)) }
                return
            }
            more += Recipient(d.address.trim(), coins, hours)
        }
        mutable.update { it.copy(send = send.copy(planning = true, planError = null)) }
        action {
            try {
                val plan = repo.plan(
                    walletId = active.id,
                    toAddress = send.to,
                    amount = amount,
                    feeRate = if (coin.kind == CoinKind.BTC) send.feeRate else null,
                    sendMax = send.sendMax,
                    fromAddress = send.fromAddress,
                    inputs = send.inputs,
                    hoursTo = hoursTo,
                    more = more,
                    changeAddress = send.changeAddress.takeIf { coin.kind == CoinKind.SKY_FIBER },
                    shareFactor = BigDecimal(send.sharePercent).movePointLeft(2)
                        .takeIf { coin.kind == CoinKind.SKY_FIBER },
                    feeLevel = send.ethLevel.takeIf { coin.isEthFamily },
                )
                mutable.update { it.copy(send = it.send.copy(planning = false, plan = plan)) }
                onReady()
            } catch (e: WalletException) {
                mutable.update { it.copy(send = it.send.copy(planning = false, planError = e.message)) }
            } catch (e: IOException) {
                mutable.update {
                    it.copy(
                        send = it.send.copy(
                            planning = false,
                            planError = e.message?.take(200)
                                ?: app.getString(R.string.wallet_error_no_route),
                        ),
                    )
                }
            }
        }
    }

    /** After the biometric confirm: sign locally, broadcast, land on the result. */
    fun signAndSend(onSent: () -> Unit) {
        val st = mutable.value
        val active = st.active ?: return
        val plan = st.send.plan ?: return
        mutable.update { it.copy(send = it.send.copy(sending = true)) }
        action {
            try {
                val txid = repo.signAndBroadcast(active.id, plan)
                mutable.update {
                    it.copy(
                        send = it.send.copy(
                            sending = false,
                            sentTxid = txid,
                            sentAmount = plan.amount,
                            sentTo = plan.toAddress,
                            sentCount = plan.recipients.size,
                        ),
                    )
                }
                refreshNow()
                onSent()
            } catch (e: Exception) {
                if (e is CancellationException) throw e
                mutable.update {
                    it.copy(send = it.send.copy(sending = false), message = report(e))
                }
            }
        }
    }

    // --- node details (fiber) ---

    suspend fun chainInfo(): Result<ChainInfo> = attempt { repo.chainInfo(mutable.value.coin) }

    suspend fun peers(): Result<List<PeerInfo>> = attempt { repo.peers(mutable.value.coin) }

    suspend fun pending(): Result<List<PendingInfo>> = attempt {
        repo.pending(mutable.value.active?.id ?: error("no wallet"))
    }

    suspend fun deletePending(txid: String): Result<Unit> = attempt { repo.deletePending(mutable.value.coin, txid) }

    /** The call's result, or its failure in the words the screens show. */
    private suspend fun <T> attempt(block: suspend () -> T): Result<T> = try {
        Result.success(block())
    } catch (e: CancellationException) {
        throw e
    } catch (e: Exception) {
        Result.failure(IllegalStateException(report(e)))
    }

    // --- offline transactions (fiber) ---

    /** The planned send unsigned, or null when there is no plan. */
    suspend fun unsignedHex(): String? {
        val st = mutable.value
        val plan = st.send.plan ?: return null
        return repo.unsignedHex(st.active?.id ?: return null, plan)
    }

    /** Signs a pasted transaction; the result or the reason it failed, in the user's words. */
    suspend fun signRaw(rawHex: String): Result<String> = offline { repo.signRaw(it, rawHex).rawHex }

    /** Broadcasts a pasted signed transaction; the txid or the reason it failed. */
    suspend fun broadcastRaw(rawHex: String): Result<String> = offline { walletId ->
        repo.broadcastRaw(walletId, rawHex).also { refreshNow() }
    }

    private suspend fun offline(block: suspend (String) -> String): Result<String> {
        val active = mutable.value.active ?: return Result.failure(IllegalStateException())
        return try {
            Result.success(block(active.id))
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Result.failure(IllegalStateException(report(e)))
        }
    }

    // --- wallet management ---

    fun useWallet(walletId: String) = action {
        val coin = mutable.value.coin
        repo.setActiveWallet(coin.id, walletId)
    }

    fun renameWallet(walletId: String, name: String) = action {
        repo.renameWallet(walletId, name)
    }

    fun removeWallet(walletId: String, onRemoved: (String) -> Unit) = action {
        val name = mutable.value.allWallets.firstOrNull { it.id == walletId }?.name ?: ""
        repo.removeWallet(walletId)
        onRemoved(name)
    }

    suspend fun revealSeed(walletId: String): String? = repo.revealSeed(walletId)

    suspend fun revealPassphrase(walletId: String): String = repo.revealPassphrase(walletId)

    suspend fun extendedPublicKeys(walletId: String): ExtendedPublicKeys? = repo.extendedPublicKeys(walletId)

    override fun onCleared() {
        refreshJob?.cancel()
        super.onCleared()
    }

    companion object {
        private const val REFRESH_INTERVAL_MS = 30_000L
    }
}

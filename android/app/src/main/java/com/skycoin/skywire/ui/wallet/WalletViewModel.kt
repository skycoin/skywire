package com.skycoin.skywire.ui.wallet

import android.app.Application
import android.net.Uri
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.R
import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.skywire.wallet.WalletMeta
import com.skycoin.skywire.wallet.WalletRepository
import com.skycoin.skywire.wallet.WalletSnapshot
import com.skycoin.wallet.Amounts
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.FeePresets
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.WalletException
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
import java.security.SecureRandom

/** Where the send flow stands; the review sheet and result screen read this. */
data class SendState(
    val to: String = "",
    val amountText: String = "",
    val sendMax: Boolean = false,
    val feeRate: Int = 12,
    val presets: FeePresets? = null,
    val planning: Boolean = false,
    val plan: TxPlan? = null,
    val planError: String? = null,
    val sending: Boolean = false,
    val sentTxid: String? = null,
    val sentAmount: ULong = 0u,
    val sentTo: String = "",
)

/** The create/restore flow in progress — the phrase lives only here, in memory. */
data class DraftState(
    val coin: CoinSpec = CoinSpec.SKY,
    val seed: String? = null,
    val quizPositions: List<Int> = emptyList(),
)

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
    val message: String? = null,
)

class WalletViewModel(app: Application) : AndroidViewModel(app) {

    private val repo = WalletRepository.get(app)
    private val mutable = MutableStateFlow(WalletUiState())
    val uiState: StateFlow<WalletUiState> = mutable.asStateFlow()

    private var refreshJob: Job? = null
    private var actionJob: Job? = null

    init {
        viewModelScope.launch {
            // All four flows read the same DataStore, so any wallet-store edit
            // re-runs this block with a consistent view.
            combine(repo.coins(), repo.selectedCoinId(), repo.wallets()) { coins, selectedId, wallets ->
                Triple(coins, selectedId, wallets)
            }.collectLatest { (coins, selectedId, wallets) ->
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
                        allWallets = wallets,
                        coinWallets = coinWallets,
                        active = active,
                        snapshot = active?.let { w -> repo.cachedSnapshot(w.id) } ?: it.snapshot.takeIf { _ -> sameWallet },
                        stale = if (sameWallet) it.stale else false,
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
                } catch (e: CancellationException) {
                    throw e
                } catch (e: Exception) {
                    // The screen renders the cached numbers with the stale
                    // banner; with no cache at all it says "not synced yet".
                    mutable.update {
                        if (it.active?.id == active.id) it.copy(stale = true, refreshing = false)
                        else it.copy(refreshing = false)
                    }
                }
                delay(REFRESH_INTERVAL_MS)
            }
        }
    }

    fun refreshNow() {
        startRefreshing(mutable.value.active)
    }

    fun messageShown() = mutable.update { it.copy(message = null) }

    private fun report(e: Exception): String = when (e) {
        is WalletException -> e.message ?: "rejected"
        is IOException -> e.message?.take(200) ?: "no route to the node"
        else -> e.message?.take(200) ?: e::class.java.simpleName
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

    // --- coin switching / Fibercoins ---

    fun selectCoin(coinId: String) = action {
        repo.setSelectedCoin(coinId)
        mutable.update { it.copy(send = SendState(), receiveIndex = 0) }
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
        require(name.isNotBlank()) { "give the coin a name" }
        require(ticker.isNotBlank()) { "give the coin a ticker" }
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
        require(name.isNotBlank()) { "give the token a name" }
        require(ticker.isNotBlank()) { "give the token a ticker" }
        val parsed = decimals.trim().toIntOrNull()
        requireNotNull(parsed) { "decimals must be a number — 6 for USDT-like tokens, 18 for most" }
        val spec = repo.addErc20Token(name, ticker, contract, parsed, icon)
        repo.setSelectedCoin(spec.id)
        onDone()
    }

    // --- create / restore ---

    /** Begin the create flow for the selected coin: fresh phrase + quiz picks. */
    fun startCreate() {
        val coin = mutable.value.coin
        val seed = Bip39.newMnemonic(128)
        val rnd = SecureRandom()
        val positions = (1..12).toMutableList().let { pool ->
            List(3) { pool.removeAt(rnd.nextInt(pool.size)) }.sorted()
        }
        mutable.update { it.copy(draft = DraftState(coin, seed, positions)) }
    }

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
                repo.addWallet(draft.coin, name, seed, restored = false)
                mutable.update { it.copy(draft = DraftState(), message = null) }
                onActivated()
            }
        }
        return wrong
    }

    /** The phrase is validated by the restore screen before this is called. */
    fun restoreWallet(seedText: String, onDone: () -> Unit) {
        val draft = mutable.value.draft
        action {
            mutable.update { it.copy(restoring = true) }
            val name = defaultWalletName(draft.coin)
            repo.addWallet(draft.coin, name, seedText, restored = true)
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
        val balance = com.skycoin.wallet.WalletBalance(
            confirmed = snapshot.confirmed,
            predicted = snapshot.predicted,
            hours = snapshot.hours,
            spendableOutputs = snapshot.spendableOutputs,
        )
        return repo.coreFor(st.coin).estimateMax(balance, st.send.feeRate)
    }

    /** Build the plan and open the review sheet on success. */
    fun buildPlan(onReady: () -> Unit) {
        val st = mutable.value
        val active = st.active ?: return
        val coin = st.coin
        val send = st.send
        val amount = if (send.sendMax) 0uL
        else Amounts.parse(send.amountText, coin.exponent) ?: run {
            mutable.update { it.copy(send = send.copy(planError = "enter a valid amount")) }
            return
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
                )
                mutable.update { it.copy(send = it.send.copy(planning = false, plan = plan)) }
                onReady()
            } catch (e: WalletException) {
                mutable.update { it.copy(send = it.send.copy(planning = false, planError = e.message)) }
            } catch (e: IOException) {
                mutable.update {
                    it.copy(send = it.send.copy(planning = false, planError = e.message?.take(200) ?: "no route to the node"))
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

    override fun onCleared() {
        refreshJob?.cancel()
        super.onCleared()
    }

    companion object {
        private const val REFRESH_INTERVAL_MS = 30_000L
    }
}

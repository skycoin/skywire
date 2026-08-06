package com.skycoin.skywire.wallet

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.SignedTx
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.WalletCore
import com.skycoin.wallet.btc.BtcWalletCore
import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.withContext
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import okhttp3.HttpUrl.Companion.toHttpUrlOrNull
import okhttp3.OkHttpClient
import java.io.File
import java.util.UUID
import java.util.concurrent.TimeUnit

/**
 * Everything the wallet screens need, behind one door: the coin list, the
 * wallets and their sealed seeds, cached chain views, and the send path.
 * Network work delegates to [WalletCore] implementations; nothing above this
 * class ever touches key material or node URLs.
 */
class WalletRepository private constructor(private val context: Context) {

    private val seeds = WalletSeedStore(context)
    private val json = Json { ignoreUnknownKeys = true }

    private val client = OkHttpClient.Builder()
        .connectTimeout(10, TimeUnit.SECONDS)
        .readTimeout(20, TimeUnit.SECONDS)
        .callTimeout(30, TimeUnit.SECONDS)
        .build()

    // --- coins ---

    fun coins(): Flow<List<CoinSpec>> = seeds.store.data.map { prefs ->
        val fiber = prefs[KEY_FIBER_COINS]?.let {
            runCatching { json.decodeFromString(ListSerializer(CoinSpec.serializer()), it) }.getOrNull()
        } ?: emptyList()
        // SKY first, user fiber coins in the order added, BTC last.
        listOf(CoinSpec.SKY) + fiber + listOf(CoinSpec.BTC)
    }

    suspend fun coin(coinId: String): CoinSpec? = coins().first().firstOrNull { it.id == coinId }

    suspend fun addFiberCoin(name: String, ticker: String, nodeUrl: String): CoinSpec {
        val url = nodeUrl.trim().removeSuffix("/")
        require(url.toHttpUrlOrNull() != null) {
            "the node address must be a full URL, like http://node.example.com:6420"
        }
        val spec = CoinSpec(
            id = "fiber-${UUID.randomUUID().toString().take(8)}",
            name = name.trim(),
            ticker = ticker.trim().uppercase(),
            kind = CoinKind.SKY_FIBER,
            nodeUrl = url,
        )
        seeds.store.edit { prefs ->
            val current = prefs[KEY_FIBER_COINS]?.let {
                runCatching { json.decodeFromString(ListSerializer(CoinSpec.serializer()), it) }.getOrNull()
            } ?: emptyList()
            prefs[KEY_FIBER_COINS] = json.encodeToString(
                ListSerializer(CoinSpec.serializer()), current + spec,
            )
        }
        return spec
    }

    fun coreFor(spec: CoinSpec): WalletCore = when (spec.kind) {
        CoinKind.SKY_FIBER -> SkyFiberWalletCore(spec.nodeUrl, client)
        CoinKind.BTC -> BtcWalletCore(spec.nodeUrl, client)
    }

    // --- selection ---

    fun selectedCoinId(): Flow<String> =
        seeds.store.data.map { it[KEY_SELECTED_COIN] ?: CoinSpec.SKY.id }

    suspend fun setSelectedCoin(coinId: String) {
        seeds.store.edit { it[KEY_SELECTED_COIN] = coinId }
    }

    fun activeWalletId(coinId: String): Flow<String?> =
        seeds.store.data.map { it[stringPreferencesKey("active_$coinId")] }

    suspend fun setActiveWallet(coinId: String, walletId: String) {
        seeds.store.edit { it[stringPreferencesKey("active_$coinId")] = walletId }
    }

    // --- wallets ---

    fun wallets(): Flow<List<WalletMeta>> = seeds.store.data.map { prefs ->
        prefs[KEY_WALLETS]?.let {
            runCatching { json.decodeFromString(ListSerializer(WalletMeta.serializer()), it) }.getOrNull()
        } ?: emptyList()
    }

    suspend fun wallet(id: String): WalletMeta? = wallets().first().firstOrNull { it.id == id }

    private suspend fun putWallets(all: List<WalletMeta>) {
        seeds.store.edit {
            it[KEY_WALLETS] = json.encodeToString(ListSerializer(WalletMeta.serializer()), all)
        }
    }

    /**
     * Create (fresh phrase, already quiz-verified) or restore. Restores probe
     * the network for used addresses; a dead node degrades to one address
     * rather than failing the restore.
     */
    suspend fun addWallet(spec: CoinSpec, name: String, mnemonic: String, restored: Boolean): WalletMeta =
        withContext(Dispatchers.IO) {
            val core = coreFor(spec)
            val seed = normalizeSeed(mnemonic)
            require(core.validateSeed(seed)) { "invalid recovery phrase" }

            val (receiveCount, changeCount) = if (restored) {
                runCatching { core.scanUsed(seed) }.getOrDefault(1 to 0)
            } else {
                1 to 0
            }
            val book = core.deriveAddresses(seed, receiveCount, changeCount)

            val meta = WalletMeta(
                id = "w-${UUID.randomUUID().toString().take(12)}",
                coinId = spec.id,
                name = name.trim().ifEmpty { spec.ticker },
                createdAtMs = System.currentTimeMillis(),
                receiveAddresses = book.receive,
                changeAddresses = book.change,
            )
            seeds.putSeed(meta.id, seed)
            putWallets(wallets().first() + meta)
            setActiveWallet(spec.id, meta.id)
            meta
        }

    private fun normalizeSeed(mnemonic: String): String =
        mnemonic.trim().lowercase().split(Regex("\\s+")).joinToString(" ")

    suspend fun renameWallet(id: String, name: String) {
        putWallets(wallets().first().map { if (it.id == id) it.copy(name = name.trim()) else it })
    }

    /** Deletes the sealed seed, the metadata and the cache. Irreversible. */
    suspend fun removeWallet(id: String) {
        val all = wallets().first()
        val gone = all.firstOrNull { it.id == id } ?: return
        putWallets(all.filter { it.id != id })
        seeds.deleteSeed(id)
        cacheFile(id).delete()
        val remaining = all.filter { it.id != id && it.coinId == gone.coinId }
        seeds.store.edit { prefs ->
            val key = stringPreferencesKey("active_${gone.coinId}")
            if (prefs[key] == id) {
                val next = remaining.firstOrNull()?.id
                if (next == null) prefs.remove(key) else prefs[key] = next
            }
        }
    }

    /** The phrase in the clear — callers gate this behind the biometric confirm. */
    suspend fun revealSeed(id: String): String? = seeds.seed(id)

    suspend fun newReceiveAddress(walletId: String): String {
        val meta = wallet(walletId) ?: error("unknown wallet")
        val spec = coin(meta.coinId) ?: error("unknown coin")
        val seed = seeds.seed(walletId) ?: error("seed unavailable")
        val book = coreFor(spec).deriveAddresses(
            seed, meta.receiveAddresses.size + 1, meta.changeAddresses.size,
        )
        putWallets(wallets().first().map {
            if (it.id == walletId) it.copy(receiveAddresses = book.receive) else it
        })
        return book.receive.last()
    }

    // --- chain view & cache ---

    private fun cacheDir(): File = File(context.filesDir, "wallet-cache").apply { mkdirs() }
    private fun cacheFile(walletId: String): File = File(cacheDir(), "$walletId.json")

    fun cachedSnapshot(walletId: String): WalletSnapshot? = runCatching {
        val f = cacheFile(walletId)
        if (!f.exists()) return null
        json.decodeFromString(WalletSnapshot.serializer(), f.readText())
    }.getOrNull()

    /** Fetch balance and history; persist and return the fresh snapshot. */
    suspend fun refresh(walletId: String): WalletSnapshot = withContext(Dispatchers.IO) {
        val meta = wallet(walletId) ?: error("unknown wallet")
        val spec = coin(meta.coinId) ?: error("unknown coin")
        val core = coreFor(spec)
        val book = AddressBook(meta.receiveAddresses, meta.changeAddresses)
        val balance = core.balance(book)
        val history = core.history(book)
        val snapshot = WalletSnapshot(
            confirmed = balance.confirmed,
            predicted = balance.predicted,
            hours = balance.hours,
            spendableOutputs = balance.spendableOutputs,
            txs = history.map {
                CachedTx(
                    txid = it.txid,
                    incoming = it.incoming,
                    amount = it.amount,
                    party = it.party,
                    timestamp = it.timestamp,
                    confirmed = it.confirmed,
                    confirmations = it.confirmations,
                    fee = it.fee,
                )
            },
            fetchedAtMs = System.currentTimeMillis(),
        )
        cacheFile(walletId).writeText(json.encodeToString(WalletSnapshot.serializer(), snapshot))
        snapshot
    }

    // --- send ---

    suspend fun plan(
        walletId: String,
        toAddress: String,
        amount: ULong,
        feeRate: Int?,
        sendMax: Boolean,
    ): TxPlan = withContext(Dispatchers.IO) {
        val meta = wallet(walletId) ?: error("unknown wallet")
        val spec = coin(meta.coinId) ?: error("unknown coin")
        val seed = seeds.seed(walletId) ?: error("seed unavailable")
        coreFor(spec).buildTx(
            seed = seed,
            book = AddressBook(meta.receiveAddresses, meta.changeAddresses),
            toAddress = toAddress.trim(),
            amount = amount,
            feeRate = feeRate,
            sendMax = sendMax,
        )
    }

    /** Sign locally and broadcast; returns the network's txid. */
    suspend fun signAndBroadcast(walletId: String, plan: TxPlan): String = withContext(Dispatchers.IO) {
        val meta = wallet(walletId) ?: error("unknown wallet")
        val spec = coin(meta.coinId) ?: error("unknown coin")
        val core = coreFor(spec)
        val seed = seeds.seed(walletId) ?: error("seed unavailable")
        val signed: SignedTx = core.signTx(seed, plan)
        val txid = core.broadcast(signed)

        // A Bitcoin plan that paid change to a fresh chain address makes that
        // address part of the wallet the moment the transaction exists.
        if (core is BtcWalletCore) {
            val idx = core.consumedChangeIndex(signed)
            if (idx >= 0 && idx == meta.changeAddresses.size) {
                val book = core.deriveAddresses(seed, meta.receiveAddresses.size, idx + 1)
                putWallets(wallets().first().map {
                    if (it.id == walletId) it.copy(changeAddresses = book.change) else it
                })
            }
        }
        txid
    }

    companion object {
        private val KEY_WALLETS = stringPreferencesKey("wallets")
        private val KEY_FIBER_COINS = stringPreferencesKey("fiber_coins")
        private val KEY_SELECTED_COIN = stringPreferencesKey("selected_coin")

        @Volatile private var instance: WalletRepository? = null
        fun get(context: Context): WalletRepository =
            instance ?: synchronized(this) {
                instance ?: WalletRepository(context.applicationContext).also { instance = it }
            }
    }
}

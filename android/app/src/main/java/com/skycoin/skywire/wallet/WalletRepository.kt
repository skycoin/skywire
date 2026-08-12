package com.skycoin.skywire.wallet

import android.content.Context
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import com.skycoin.skywire.R
import com.skycoin.wallet.AddressBook
import com.skycoin.wallet.SignedTx
import com.skycoin.wallet.TxPlan
import com.skycoin.wallet.WalletCore
import com.skycoin.wallet.btc.BtcWalletCore
import com.skycoin.wallet.eth.EthCrypto
import com.skycoin.wallet.eth.EthWalletCore
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
        // One stored list for everything user-added; the key predates
        // tokens and is not worth a migration to rename.
        val user = prefs[KEY_FIBER_COINS]?.let {
            runCatching { json.decodeFromString(ListSerializer(CoinSpec.serializer()), it) }.getOrNull()
        } ?: emptyList()
        // SKY first, user Fibercoins in the order added, then the other
        // built-ins, then user tokens in the order added.
        listOf(CoinSpec.SKY) +
            user.filter { it.kind == CoinKind.SKY_FIBER } +
            listOf(CoinSpec.BTC, CoinSpec.ETH, CoinSpec.USDT) +
            user.filter { it.kind == CoinKind.ERC20 }
    }

    suspend fun coin(coinId: String): CoinSpec? = coins().first().firstOrNull { it.id == coinId }

    suspend fun addFiberCoin(name: String, ticker: String, nodeUrl: String, icon: String? = null): CoinSpec {
        val url = nodeUrl.trim().removeSuffix("/")
        require(url.toHttpUrlOrNull() != null) {
            context.getString(R.string.wallet_add_coin_node_invalid)
        }
        val spec = CoinSpec(
            id = "fiber-${UUID.randomUUID().toString().take(8)}",
            name = name.trim(),
            ticker = ticker.trim().uppercase(),
            kind = CoinKind.SKY_FIBER,
            nodeUrl = url,
            icon = icon,
        )
        storeUserCoin(spec)
        return spec
    }

    /**
     * Add an ERC-20 token on Ethereum mainnet: same chain plumbing as USDT,
     * different contract and decimals. The contract must be checksum-valid;
     * decimals must match the contract's own or amounts will be off by
     * powers of ten.
     */
    suspend fun addErc20Token(name: String, ticker: String, contract: String, decimals: Int, icon: String? = null): CoinSpec {
        require(EthCrypto.isValidAddress(contract.trim())) {
            context.getString(R.string.wallet_add_token_contract_invalid)
        }
        require(decimals in 0..36) { context.getString(R.string.wallet_add_token_decimals_range) }
        val spec = CoinSpec(
            id = "erc20-${UUID.randomUUID().toString().take(8)}",
            name = name.trim(),
            ticker = ticker.trim().uppercase(),
            kind = CoinKind.ERC20,
            nodeUrl = CoinSpec.ETH_NODE,
            explorerTxUrl = "${CoinSpec.ETH_INDEXER}/tx/%s",
            contract = contract.trim(),
            tokenDecimals = decimals,
            indexerUrl = CoinSpec.ETH_INDEXER,
            icon = icon,
        )
        storeUserCoin(spec)
        // The account this token lives in may already be set up under ETH or
        // another token; if so it arrives holding it, rather than asking for
        // a phrase the user has already given.
        adoptEthFamilyWallets(spec)
        return spec
    }

    /**
     * Drop a user-added coin from the list, returning the spec that went so a
     * caller can name it and clear up its icon file — icon storage belongs to
     * the UI layer that put the file there.
     *
     * Refuses while any wallet still holds the coin, rather than cascading
     * into [removeWallet]. That is not caution for its own sake: removeWallet
     * erases a sealed seed that exists nowhere else, and a phrase written down
     * nowhere else is gone with it. Tidying a list is not a moment where a
     * user is braced for that, so the two stay separate acts — delete the
     * wallets, having read what deleting a wallet means, and then the coin.
     *
     * Built-ins are not removable. They are the wallet's floor: SKY is what
     * the selection falls back TO, and the other three have no add path to
     * restore them from.
     */
    suspend fun removeUserCoin(coinId: String): CoinSpec? {
        val spec = coin(coinId) ?: return null
        require(!spec.builtIn) { context.getString(R.string.wallet_coin_remove_builtin, spec.name) }
        val held = wallets().first().count { it.coinId == coinId }
        require(held == 0) {
            context.resources.getQuantityString(
                R.plurals.wallet_coin_remove_blocked_count, held, held,
            )
        }
        seeds.store.edit { prefs ->
            val current = prefs[KEY_FIBER_COINS]?.let {
                runCatching { json.decodeFromString(ListSerializer(CoinSpec.serializer()), it) }.getOrNull()
            } ?: emptyList()
            prefs[KEY_FIBER_COINS] = json.encodeToString(
                ListSerializer(CoinSpec.serializer()), current.filter { it.id != coinId },
            )
            // The active-wallet pointer for a coin with no wallets is already
            // meaningless, but leaving it would resurrect a stale id if a coin
            // with the same generated id ever came back.
            prefs.remove(stringPreferencesKey("active_$coinId"))
            // Selection has to move or the wallet tab opens on a coin that is
            // no longer in its own list.
            if (prefs[KEY_SELECTED_COIN] == coinId) prefs[KEY_SELECTED_COIN] = CoinSpec.SKY.id
        }
        return spec
    }

    private suspend fun storeUserCoin(spec: CoinSpec) {
        seeds.store.edit { prefs ->
            val current = prefs[KEY_FIBER_COINS]?.let {
                runCatching { json.decodeFromString(ListSerializer(CoinSpec.serializer()), it) }.getOrNull()
            } ?: emptyList()
            prefs[KEY_FIBER_COINS] = json.encodeToString(
                ListSerializer(CoinSpec.serializer()), current + spec,
            )
        }
    }

    fun coreFor(spec: CoinSpec): WalletCore = when (spec.kind) {
        CoinKind.SKY_FIBER -> SkyFiberWalletCore(spec.nodeUrl, client)
        CoinKind.BTC -> BtcWalletCore(spec.nodeUrl, client)
        CoinKind.ETH -> EthWalletCore(spec.nodeUrl, spec.indexerUrl, client)
        CoinKind.ERC20 -> EthWalletCore(
            spec.nodeUrl,
            spec.indexerUrl,
            client,
            EthWalletCore.Erc20Token(
                contract = spec.contract ?: error("token ${spec.id} has no contract address"),
                decimals = spec.tokenDecimals ?: CoinSpec.DEFAULT_TOKEN_DECIMALS,
            ),
        )
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
            require(core.validateSeed(seed)) { context.getString(R.string.wallet_seed_invalid) }

            val (receiveCount, changeCount) = if (restored) {
                runCatching { core.scanUsed(seed) }.getOrDefault(1 to 0)
            } else {
                1 to 0
            }
            val book = core.deriveAddresses(seed, receiveCount, changeCount)

            // This coin may already hold this exact account — most likely
            // because mirroring put it there, and the user restored the phrase
            // under the sibling anyway out of habit, which is the very habit
            // this is meant to retire. Two entries for one address would show
            // the same balance twice and let the same coins be spent from
            // either, so the existing one is adopted instead of duplicated.
            val head = book.receive.firstOrNull()
            val already = wallets().first().firstOrNull {
                it.coinId == spec.id && head != null && it.receiveAddresses.firstOrNull() == head
            }
            if (already != null) {
                // A restore that scanned further than the mirror knew about is
                // the one thing worth carrying over.
                val grown = if (book.receive.size > already.receiveAddresses.size) {
                    already.copy(receiveAddresses = book.receive, changeAddresses = book.change)
                        .also { putWallets(wallets().first().map { w -> if (w.id == it.id) it else w }) }
                } else {
                    already
                }
                setActiveWallet(spec.id, grown.id)
                return@withContext grown
            }

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
            if (spec.isEthFamily) mirrorIntoEthFamily(meta, seed, book, exceptCoin = spec.id)
            meta
        }

    /**
     * Give every other Ethereum-family coin the same wallet.
     *
     * ETH and every ERC-20 are one account on one chain — the same key, the
     * same address, differing only in which asset you are looking at. The
     * store keys a wallet by coin, so without this a user who has entered
     * their phrase for ETH is asked for the very same phrase again to see the
     * USDT sitting at the address they just added, and again for each token
     * after that.
     *
     * [book] is passed rather than re-derived: EthWalletCore.deriveAddresses
     * reads only the seed, so this is the same computation, and reusing it
     * makes "the mirror has identical addresses" true by construction rather
     * than by agreement between two call sites.
     */
    private suspend fun mirrorIntoEthFamily(
        source: WalletMeta,
        seed: String,
        book: AddressBook,
        exceptCoin: String,
    ) {
        coins().first()
            .filter { it.isEthFamily && it.id != exceptCoin }
            .forEach { sibling -> mirrorWallet(sibling, source.name, source.createdAtMs, seed, book) }
    }

    /**
     * One mirrored wallet, or nothing if [coin] already has this account.
     *
     * Matching on the first receive address rather than on a stored link:
     * an address IS the account here, so this stays right for wallets that
     * predate mirroring — including the pair someone made by entering the
     * same phrase twice, which is what this feature exists to stop.
     */
    private suspend fun mirrorWallet(
        coin: CoinSpec,
        name: String,
        createdAtMs: Long,
        seed: String,
        book: AddressBook,
    ): WalletMeta? {
        val head = book.receive.firstOrNull() ?: return null
        val existing = wallets().first().filter { it.coinId == coin.id }
        if (existing.any { it.receiveAddresses.firstOrNull() == head }) return null

        val meta = WalletMeta(
            id = "w-${UUID.randomUUID().toString().take(12)}",
            coinId = coin.id,
            name = name,
            createdAtMs = createdAtMs,
            receiveAddresses = book.receive,
            changeAddresses = book.change,
        )
        // A second sealed copy of a phrase already on this device, under the
        // same keystore key — no new exposure, and the alternative (one seed
        // shared by reference) makes deleting any one wallet able to strand
        // the others.
        seeds.putSeed(meta.id, seed)
        putWallets(wallets().first() + meta)
        // Only if that coin has nothing selected yet: arriving at a coin the
        // user was already using must not move them off their own choice.
        if (activeWalletId(coin.id).first() == null) setActiveWallet(coin.id, meta.id)
        return meta
    }

    /**
     * Give a newly added ERC-20 the Ethereum wallets that already exist, so a
     * token added today reaches the account the user set up months ago
     * without them typing the phrase again — the same rule as
     * [mirrorIntoEthFamily], applied when the coin arrives after the wallet
     * instead of before it.
     *
     * One wallet per distinct account: several ETH-family coins already hold
     * a copy each, and they must not become several copies here.
     */
    private suspend fun adoptEthFamilyWallets(token: CoinSpec) {
        val ethCoinIds = coins().first().filter { it.isEthFamily && it.id != token.id }.map { it.id }.toSet()
        val seen = mutableSetOf<String>()
        wallets().first()
            .filter { it.coinId in ethCoinIds }
            .forEach { existing ->
                val head = existing.receiveAddresses.firstOrNull() ?: return@forEach
                if (!seen.add(head)) return@forEach
                // Unreadable seed: keystore rotated under the store, or the
                // wallet predates sealing. Nothing to copy, and the user can
                // still restore the token's wallet by hand.
                val seed = seeds.seed(existing.id) ?: return@forEach
                mirrorWallet(
                    token,
                    existing.name,
                    existing.createdAtMs,
                    seed,
                    AddressBook(existing.receiveAddresses, existing.changeAddresses),
                )
            }
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
        val seed = seeds.seed(walletId) ?: error(context.getString(R.string.wallet_seed_unavailable))
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
        val seed = seeds.seed(walletId) ?: error(context.getString(R.string.wallet_seed_unavailable))
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
        val seed = seeds.seed(walletId) ?: error(context.getString(R.string.wallet_seed_unavailable))
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

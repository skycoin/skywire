package com.skycoin.skywire.dex

import android.content.Context
import com.skycoin.skywire.api.DexOrder
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.File

/**
 * What SkyDEX keeps on this phone. The market is the source of truth for
 * trades; this is what the web page kept in localStorage: the addresses to
 * pre-fill, and finished trades the market deletes after its cleanup window.
 */
@Serializable
data class DexLocal(
    /** "SKY" plus one payout address per payment coin, as last registered. */
    val wallets: Map<String, String> = emptyMap(),
    val history: List<DexOrder> = emptyList(),
    val legacyImported: Boolean = false,
) {
    val registered: Boolean get() = !wallets[SKY].isNullOrBlank()
}

class DexStore private constructor(context: Context) {

    private val file = File(context.filesDir, "skydex/state.json")
    private val mutable = MutableStateFlow(load())
    val state: StateFlow<DexLocal> = mutable.asStateFlow()

    fun update(block: (DexLocal) -> DexLocal) {
        synchronized(this) {
            val next = block(mutable.value)
            if (next == mutable.value) return
            mutable.value = next
            write(next)
        }
    }

    private fun load(): DexLocal =
        runCatching { json.decodeFromString(DexLocal.serializer(), file.readText()) }.getOrDefault(DexLocal())

    private fun write(state: DexLocal) {
        runCatching {
            file.parentFile?.mkdirs()
            val tmp = File(file.parentFile, file.name + ".tmp")
            tmp.writeText(json.encodeToString(DexLocal.serializer(), state))
            tmp.renameTo(file)
        }
    }

    companion object {
        private val json = Json { ignoreUnknownKeys = true; encodeDefaults = false }

        @Volatile private var instance: DexStore? = null

        fun get(context: Context): DexStore =
            instance ?: synchronized(this) {
                instance ?: DexStore(context.applicationContext).also { instance = it }
            }
    }
}

const val SKY = "SKY"

/** Order states that are over: settled, or ended without settling. */
val TERMINAL_STATUSES = setOf("completed", "canceled", "cancelled", "expired")

/**
 * Folds the market's finished orders into the saved history, tagging each with
 * the market it came from. Orders the market has since purged stay (api.js
 * mergeTradeHistory). Keyed by side and id: a self-trade is one order seen
 * as both a buy and a sell.
 */
fun mergeHistory(saved: List<DexOrder>, orders: List<DexOrder>, marketPk: String, marketName: String, now: Long): List<DexOrder> {
    val byId = LinkedHashMap<String, DexOrder>()
    saved.forEach { byId[it.type + ":" + it.id] = it }
    for (o in orders) {
        if (o.id.isEmpty() || o.status !in TERMINAL_STATUSES) continue
        val key = o.type + ":" + o.id
        val prev = byId[key]
        byId[key] = o.copy(
            marketPk = marketPk.ifEmpty { prev?.marketPk.orEmpty() },
            marketName = marketName.ifEmpty { prev?.marketName.orEmpty() },
            savedAt = prev?.savedAt?.takeIf { it > 0 } ?: now,
        )
    }
    return byId.values.toList()
}

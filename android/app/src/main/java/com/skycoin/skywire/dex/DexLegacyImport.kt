package com.skycoin.skywire.dex

import android.annotation.SuppressLint
import android.content.Context
import android.webkit.HttpAuthHandler
import android.webkit.WebView
import android.webkit.WebViewClient
import com.skycoin.skywire.api.DexOrder
import com.skycoin.skywire.core.SkydexProfile
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import kotlinx.coroutines.withTimeoutOrNull
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import java.io.File

/** A market the old page remembered (`exchange:markets`). */
data class LegacyMarket(val pk: String, val name: String, val ts: Long)

data class LegacyDex(
    val wallets: Map<String, String> = emptyMap(),
    val markets: List<LegacyMarket> = emptyList(),
    val history: List<DexOrder> = emptyList(),
)

/**
 * One-time move of what the old embedded trading page kept in the WebView's
 * localStorage. Trade history matters most: the market deletes finished
 * orders, so the page's copy was the only one. Like the chat's import, a
 * hidden WebView loads a page from the old origin and hands the storage back.
 */
object DexLegacyImport {

    private const val TIMEOUT_MS = 20_000L
    private val json = Json { ignoreUnknownKeys = true; isLenient = true }

    fun hasWebViewData(context: Context): Boolean =
        File(context.applicationInfo.dataDir, "app_webview").isDirectory

    /** The page's localStorage, or null when it could not be read. Runs on the main thread. */
    @SuppressLint("SetJavaScriptEnabled")
    suspend fun read(context: Context, baseUrl: String, password: String): Map<String, String>? =
        withContext(Dispatchers.Main) {
            val done = CompletableDeferred<String?>()
            val view = WebView(context.applicationContext)
            view.settings.javaScriptEnabled = true
            view.settings.domStorageEnabled = true
            view.webViewClient = object : WebViewClient() {
                override fun onReceivedHttpAuthRequest(v: WebView, handler: HttpAuthHandler, host: String, realm: String) {
                    handler.proceed(SkydexProfile.USER, password)
                }

                override fun onPageFinished(v: WebView, url: String) {
                    v.evaluateJavascript(DUMP) { done.complete(it) }
                }
            }
            view.loadUrl(baseUrl + "api/config")
            val raw = withTimeoutOrNull(TIMEOUT_MS) { done.await() }
            view.stopLoading()
            view.destroy()
            val text = raw?.takeIf { it != "null" } ?: return@withContext null
            runCatching {
                val inner = (json.parseToJsonElement(text) as JsonPrimitive).content
                (json.parseToJsonElement(inner) as JsonObject).mapValues { (it.value as JsonPrimitive).content }
            }.getOrNull()
        }

    /** api.js keys: exchange:wallets, exchange:markets, exchange:history. */
    fun parse(storage: Map<String, String>): LegacyDex {
        fun element(key: String) = storage[key]?.let { runCatching { json.parseToJsonElement(it) }.getOrNull() }

        val wallets = (element("exchange:wallets") as? JsonObject)
            ?.mapNotNull { (coin, v) -> (v as? JsonPrimitive)?.contentOrNull?.trim()?.takeIf { it.isNotEmpty() }?.let { coin to it } }
            ?.toMap()
            .orEmpty()
        val markets = (element("exchange:markets") as? JsonArray).orEmpty().mapNotNull { e ->
            val o = e as? JsonObject ?: return@mapNotNull null
            val pk = o["pk"]?.jsonPrimitive?.contentOrNull?.trim().orEmpty()
            if (pk.isEmpty()) return@mapNotNull null
            LegacyMarket(pk, o["name"]?.jsonPrimitive?.contentOrNull.orEmpty(), o["ts"]?.jsonPrimitive?.longOrNull ?: 0L)
        }
        val history = (element("exchange:history") as? JsonArray).orEmpty().mapNotNull { e ->
            runCatching { json.decodeFromJsonElement(DexOrder.serializer(), e) }.getOrNull()?.takeIf { it.id.isNotEmpty() }
        }
        return LegacyDex(wallets, markets, history)
    }

    private const val DUMP =
        "(function(){var o={};for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);o[k]=localStorage.getItem(k);}return JSON.stringify(o);})()"
}

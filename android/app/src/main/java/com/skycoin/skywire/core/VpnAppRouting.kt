package com.skycoin.skywire.core

import android.Manifest
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.combine
import kotlinx.coroutines.flow.first
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.builtins.serializer
import kotlinx.serialization.json.Json

/**
 * Which apps SkyVPN carries.
 *
 * A whole-phone tunnel sends everything through the mesh, including the apps
 * that gain nothing from it and only get slower: a banking app, a local
 * streaming app, a game. So the tunnel can take every app, only the apps
 * chosen for it, or every app but the ones chosen to stay out.
 */
enum class VpnAppMode(val key: String) {
    /** Every app (the phone-wide VPN, and the default). */
    ALL("all"),

    /** Only the chosen apps; everything else keeps the normal connection. */
    ONLY("only"),

    /** Every app except the chosen ones. */
    EXCEPT("except"),
    ;

    companion object {
        fun of(key: String?): VpnAppMode = entries.firstOrNull { it.key == key } ?: ALL
    }
}

/**
 * The app routing choice. Each mode keeps its own list, so switching between
 * "only these" and "all but these" does not lose either.
 */
data class VpnAppRouting(
    val mode: VpnAppMode = VpnAppMode.ALL,
    val only: Set<String> = emptySet(),
    val except: Set<String> = emptySet(),
) {
    /** The chosen apps of the current mode. */
    val selected: Set<String>
        get() = when (mode) {
            VpnAppMode.ALL -> emptySet()
            VpnAppMode.ONLY -> only
            VpnAppMode.EXCEPT -> except
        }

    /**
     * Whether a tunnel can be built from this. "Only these apps" with none
     * chosen cannot, and must not be tried: Android reads an empty allow-list
     * as no list at all, which puts *every* app in the tunnel — this app and
     * the visor it runs among them, whose traffic is what carries the tunnel.
     */
    val usable: Boolean get() = mode != VpnAppMode.ONLY || only.isNotEmpty()
}

/**
 * What the VPN builder is told. Android takes an allow-list or a deny-list for
 * one interface, never both.
 */
data class TunAppRules(val allowed: List<String>, val disallowed: List<String>)

/**
 * The builder's lists for [routing]. This app ([self]) is always outside the
 * tunnel: the visor runs as its child and shares its UID, and its dmsg traffic
 * is the traffic carrying the tunnel. With a deny-list it is denied first; with
 * an allow-list it is simply never allowed.
 */
fun tunAppRules(routing: VpnAppRouting, self: String): TunAppRules = when (routing.mode) {
    VpnAppMode.ALL -> TunAppRules(allowed = emptyList(), disallowed = listOf(self))
    VpnAppMode.ONLY -> TunAppRules(allowed = routing.only.filter { it != self }.sorted(), disallowed = emptyList())
    VpnAppMode.EXCEPT -> TunAppRules(
        allowed = emptyList(),
        disallowed = listOf(self) + routing.except.filter { it != self }.sorted(),
    )
}

/**
 * The app routing choice, kept in [AppPreferences]. Read by the VPN screen
 * and, at every interface build, by [SkyVpnService], so whichever path starts
 * the tunnel builds it the way the user chose.
 */
class VpnAppRoutingStore(private val prefs: AppPreferences) {

    private val json = Json { ignoreUnknownKeys = true }
    private val list = ListSerializer(String.serializer())

    fun flow(): Flow<VpnAppRouting> = combine(
        prefs.string(KEY_MODE),
        prefs.string(listKey(VpnAppMode.ONLY)),
        prefs.string(listKey(VpnAppMode.EXCEPT)),
    ) { mode, only, except ->
        VpnAppRouting(VpnAppMode.of(mode), decode(only), decode(except))
    }

    suspend fun read(): VpnAppRouting = flow().first()

    suspend fun setMode(mode: VpnAppMode) {
        prefs.putString(KEY_MODE, mode.key)
    }

    /** The chosen apps for [mode] — ONLY or EXCEPT; ALL has none. */
    suspend fun setApps(mode: VpnAppMode, apps: Set<String>) {
        if (mode == VpnAppMode.ALL) return
        prefs.putString(listKey(mode), json.encodeToString(list, apps.sorted()))
    }

    private fun decode(stored: String?): Set<String> =
        stored?.let { runCatching { json.decodeFromString(list, it) }.getOrNull() }.orEmpty().toSet()

    private fun listKey(mode: VpnAppMode) = "vpn_apps_${mode.key}"

    private companion object {
        const val KEY_MODE = "vpn_app_mode"
    }
}

/** An app that can be chosen for SkyVPN. */
data class InstalledApp(val packageName: String, val label: String)

/** [apps] with the [chosen] ones first, each group keeping its order. */
fun chosenFirst(apps: List<InstalledApp>, chosen: Set<String>): List<InstalledApp> =
    apps.sortedBy { it.packageName !in chosen }

/**
 * The apps worth offering: the ones on the launcher (the apps a person opens
 * and would think to choose) that can use the network at all, without this
 * one, sorted by name.
 *
 * The launcher query is what the manifest's `<queries>` entry makes visible
 * from Android 11. Listing every package would need QUERY_ALL_PACKAGES, a
 * permission to see everything installed, for a list whose background
 * services nobody would recognise anyway.
 */
object InstalledApps {

    fun load(context: Context): List<InstalledApp> {
        val pm = context.packageManager
        val launcher = Intent(Intent.ACTION_MAIN).addCategory(Intent.CATEGORY_LAUNCHER)
        val found = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            pm.queryIntentActivities(launcher, PackageManager.ResolveInfoFlags.of(0))
        } else {
            @Suppress("DEPRECATION")
            pm.queryIntentActivities(launcher, 0)
        }
        return found
            .map { it.activityInfo.applicationInfo }
            .distinctBy { it.packageName }
            .filter { it.packageName != context.packageName }
            .filter { pm.checkPermission(Manifest.permission.INTERNET, it.packageName) == PackageManager.PERMISSION_GRANTED }
            .map { InstalledApp(it.packageName, it.loadLabel(pm).toString()) }
            .sortedBy { it.label.lowercase() }
    }
}

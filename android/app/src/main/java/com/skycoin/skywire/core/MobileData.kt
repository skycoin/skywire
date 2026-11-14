package com.skycoin.skywire.core

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import android.net.NetworkRequest
import android.os.Build
import android.os.Handler
import android.os.Looper
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.distinctUntilChanged

/**
 * Whether the visor may use mobile data, on by default. Off, the core goes
 * offline on a metered network, and one not yet running waits to start.
 */
object MobileData {

    /** [AppPreferences] key holding the user's choice. */
    const val PREF_KEY = "use_mobile_data"

    const val DEFAULT = true

    /** Field inside the config's `transport.network` object. */
    const val CONFIG_KEY = "metered"

    /** The core's metered mode that dials nothing, `visorapi.ModeOffline`. */
    const val METERED_OFFLINE = "offline"

    /** A VPN counts as metered whatever it runs over, so its flag is no answer. */
    internal fun meteredOf(vpn: Boolean, notMetered: Boolean): Boolean? = if (vpn) null else !notMetered

    /**
     * Whether the network under any VPN is metered, null while there is none.
     * It is the same network [NetworkWatcher] reports to the core.
     */
    fun metered(context: Context): Flow<Boolean?> = callbackFlow {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        trySend(null)
        var current: Network? = null
        fun callback() = object : ConnectivityManager.NetworkCallback() {
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                val metered = meteredOf(
                    vpn = caps.hasTransport(NetworkCapabilities.TRANSPORT_VPN),
                    notMetered = caps.hasCapability(NetworkCapabilities.NET_CAPABILITY_NOT_METERED),
                ) ?: return
                current = network
                trySend(metered)
            }

            override fun onLost(network: Network) {
                if (network != current) return
                current = null
                trySend(null)
            }
        }
        // One thread for both, so [current] needs no lock.
        val handler = Handler(Looper.getMainLooper())
        val registered = mutableListOf(callback().also { cm.registerDefaultNetworkCallback(it, handler) })
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            val underVpn = NetworkRequest.Builder()
                .addCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET)
                .addCapability(NetworkCapabilities.NET_CAPABILITY_NOT_VPN)
                .build()
            registered += callback().also { cm.registerBestMatchingNetworkCallback(underVpn, it, handler) }
        }
        awaitClose { registered.forEach(cm::unregisterNetworkCallback) }
    }.distinctUntilChanged()
}

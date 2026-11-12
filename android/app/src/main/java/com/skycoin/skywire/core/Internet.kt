package com.skycoin.skywire.core

import android.content.Context
import android.net.ConnectivityManager
import android.net.Network
import android.net.NetworkCapabilities
import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.coroutines.flow.distinctUntilChanged

/**
 * Whether the phone has a network with internet access, as the system sees it.
 * Without one the core cannot reach the Skywire network and waits.
 */
object Internet {
    fun online(context: Context): Flow<Boolean> = callbackFlow {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        fun has(caps: NetworkCapabilities?) =
            caps?.hasCapability(NetworkCapabilities.NET_CAPABILITY_INTERNET) == true
        // The callback says nothing while there is no default network at all.
        trySend(has(cm.getNetworkCapabilities(cm.activeNetwork)))
        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onCapabilitiesChanged(network: Network, caps: NetworkCapabilities) {
                trySend(has(caps))
            }

            // A default network callback hears onLost only when no network is
            // left, and activeNetwork can still name the lost one at that moment.
            override fun onLost(network: Network) {
                trySend(false)
            }
        }
        cm.registerDefaultNetworkCallback(callback)
        awaitClose { cm.unregisterNetworkCallback(callback) }
    }.distinctUntilChanged()
}

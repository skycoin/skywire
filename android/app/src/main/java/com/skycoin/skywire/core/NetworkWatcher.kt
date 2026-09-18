package com.skycoin.skywire.core

import android.content.Context
import android.net.ConnectivityManager
import android.net.LinkProperties
import android.net.Network
import android.util.Log
import com.skycoin.skywire.api.VisorApi
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.NonCancellable
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.net.InetAddress
import kotlin.coroutines.coroutineContext

/**
 * What makes one attachment to the network different from another: which
 * network the system routes through, and the addresses we hold on it. A TCP
 * socket survives neither of those changing.
 */
internal data class Attachment(val networkId: String, val addresses: String) {
    override fun toString(): String =
        if (addresses.isEmpty()) networkId else "$networkId[$addresses]"

    companion object {
        /**
         * Routable addresses only, in a stable order. Link-local is
         * per-interface and constant across the moves that matter, and no
         * dmsg session is bound to one; ordering is the framework's, not a
         * fact about the network.
         */
        fun of(networkId: String, addresses: List<InetAddress>): Attachment =
            Attachment(
                networkId = networkId,
                addresses = addresses
                    .filterNot { it.isLinkLocalAddress || it.isLoopbackAddress || it.isAnyLocalAddress }
                    .mapNotNull { it.hostAddress }
                    .sorted()
                    .joinToString(","),
            )
    }
}

/**
 * Decides which network events are worth a re-dial.
 *
 * Deliberately narrow: the default network's identity, or the addresses on
 * it. Not its capabilities, not signal strength, not metered-ness — none of
 * those invalidate a socket, and re-dialling on them would churn sessions for
 * nothing. The network the visor started on is the baseline and is never
 * itself a move.
 *
 * Not synchronized: the framework serializes `NetworkCallback` delivery, and
 * this is only ever driven from there.
 */
internal class NetworkMoves {

    private var current: Attachment? = null
    private var baselineTaken = false

    /** The attachment last observed, or null while there is no default network. */
    fun current(): Attachment? = current

    /**
     * Records where we are now. Returns true when that is a move — i.e. the
     * sockets the core holds are no longer on the network it holds them on.
     */
    fun observe(next: Attachment): Boolean {
        if (next == current) return false
        current = next
        if (!baselineTaken) {
            // The network the visor came up on. Its sockets are the ones it
            // just opened; there is nothing to re-dial.
            baselineTaken = true
            return false
        }
        return true
    }

    /**
     * The default network went away. Not a move on its own — there is nothing
     * to re-dial into — but it forgets where we were, so coming back up
     * counts as one however long the gap was.
     */
    fun lost(networkId: String) {
        if (current?.networkId == networkId) current = null
    }
}

/**
 * Tells the core the ground moved.
 *
 * A phone changes network constantly: Wi-Fi to cellular when you walk out the
 * door, cellular to Wi-Fi when you come back, and on mobile data a new IP
 * whenever the carrier feels like it — a handover between cells, an idle PDP
 * context torn down and rebuilt, a CGNAT mapping expiring. Every one of those
 * silently kills every TCP connection the visor holds. Nothing is sent on a
 * dead socket, so neither end learns it is dead: the visor's dmsg sessions
 * sit in ESTABLISHED against an address that is no longer the phone's.
 *
 * Left alone the visor finds out when a yamux keepalive fails to write — a
 * 30 s interval plus a 45 s write timeout, so up to ~75 s of being off the
 * network while believing it is on it. For that window the hypervisor sees
 * the phone stop answering and dmsg peers cannot reach its listeners, and on
 * mobile data the next move often lands before it has finished recovering
 * from the last. That is the flapping an operator watching from a desk sees.
 *
 * The system knows the instant it happens, so this listens and hands the core
 * the one fact it cannot observe for itself. The core does the rest:
 * `POST /api/dmsg/reconnect` closes the dead sessions, the dmsg client
 * re-dials and republishes its discovery entry, and the hypervisor RPC conn —
 * a stream on one of those sessions — is redialled behind it.
 *
 * Runs off the core service for the lifetime of one visor process, like the
 * other watchers there, so the baseline is re-taken every time the core
 * starts.
 */
internal class NetworkWatcher(context: Context) {

    private val app = context.applicationContext
    private val api = VisorApi.get(app)

    fun watch(scope: CoroutineScope): Job = scope.launch(Dispatchers.IO) {
        val cm = app.getSystemService(ConnectivityManager::class.java)
        if (cm == null) {
            Log.w(TAG, "no ConnectivityManager; network moves will not be noticed")
            return@launch
        }

        // Conflated: a handover fires several callbacks in a row and they all
        // mean the same single thing — re-dial once, after they settle.
        val moves = Channel<Unit>(Channel.CONFLATED)
        val state = NetworkMoves()

        val callback = object : ConnectivityManager.NetworkCallback() {
            override fun onAvailable(network: Network) {
                note(network, cm.getLinkProperties(network))
            }

            override fun onLinkPropertiesChanged(network: Network, props: LinkProperties) {
                note(network, props)
            }

            override fun onLost(network: Network) {
                state.lost(network.toString())
                Log.d(TAG, "default network lost: $network")
            }

            private fun note(network: Network, props: LinkProperties?) {
                val was = state.current()
                val next = Attachment.of(
                    network.toString(),
                    props?.linkAddresses.orEmpty().map { it.address },
                )
                if (!state.observe(next)) {
                    // The first attachment is worth a line of its own: it is
                    // the one every later "moved" line is read against, and
                    // without it a log that never says "moved" is ambiguous
                    // between "nothing moved" and "this never started".
                    // Repeats of an attachment we already hold are not — the
                    // framework re-reports freely and they would be noise.
                    if (was == null) Log.i(TAG, "network baseline: $next")
                    return
                }
                Log.i(TAG, "network moved: $was -> $next")
                moves.trySend(Unit)
            }
        }

        try {
            cm.registerDefaultNetworkCallback(callback)
        } catch (e: SecurityException) {
            // ACCESS_NETWORK_STATE is in the manifest, but a hardened ROM can
            // still refuse. The visor's own keepalives remain the fallback —
            // slower, not absent.
            Log.w(TAG, "cannot watch the default network: ${e.message}")
            return@launch
        }

        try {
            while (coroutineContext.isActive) {
                moves.receive()
                // Let the burst finish before acting: a handover reports the
                // new network, then its addresses, sometimes twice.
                delay(SETTLE_MS)
                while (moves.tryReceive().isSuccess) { /* coalesce */ }
                reconnect()
            }
        } finally {
            // The framework holds this registration past our scope; unregister
            // even when the scope is already cancelled.
            withContext(NonCancellable) {
                runCatching { cm.unregisterNetworkCallback(callback) }
            }
        }
    }

    /**
     * One attempt, then one retry. The core is normally right there on
     * loopback, but a move can land while it is still coming up or is itself
     * busy failing over — and a miss here costs the full keepalive wait this
     * class exists to avoid, so it is worth asking twice.
     */
    private suspend fun reconnect() {
        repeat(ATTEMPTS) { attempt ->
            val closed = runCatching { api.dmsgReconnect() }
                .onFailure { Log.d(TAG, "dmsg re-dial request failed: ${it.message}") }
                .getOrNull()
            if (closed != null) {
                Log.i(TAG, "core re-dialling dmsg; $closed session(s) dropped")
                return
            }
            if (attempt < ATTEMPTS - 1) delay(RETRY_MS)
        }
    }

    private companion object {
        const val TAG = "NetworkWatcher"

        /** Long enough for a handover's callbacks to land, short enough to beat a user noticing. */
        const val SETTLE_MS = 1_500L

        const val ATTEMPTS = 2
        const val RETRY_MS = 2_000L
    }
}

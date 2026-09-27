package com.skycoin.skywire.core

import android.content.Context
import android.net.ConnectivityManager
import android.net.LocalServerSocket
import android.net.LocalSocket
import android.os.ParcelFileDescriptor
import android.os.Process
import android.os.SystemClock
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.IOException
import java.net.Inet4Address
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.NetworkInterface
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.ConcurrentHashMap

/**
 * SkyVPN for the devices on this phone's hotspot.
 *
 * Android forwards tethered traffic below VpnService, straight to the uplink,
 * and no app without root can pull it into its tunnel. What a device on the
 * hotspot CAN do is use a proxy, so that is what this is: a port on the
 * hotspot's address that answers HTTP and SOCKS5 proxy requests, and carries
 * each connection through the tunnel.
 *
 * This side only decides who gets in. It listens on [PORT], lets through only
 * connections that arrived on a hotspot interface — the café Wi-Fi the phone is
 * joined to must not get its tunnel — and hands each one to the visor as a file
 * descriptor. vpn-client does the proxying (pkg/vpn/share.go), dialling from a
 * netstack that shares the tunnel's address; that is the only way in, because
 * this app, and so the visor, is excluded from its own tunnel.
 */
object VpnHotspot {
    /** The proxy port on the hotspot address — HTTP and SOCKS5 alike. */
    const val PORT = 8118

    /** Whether the user wants it on; applied whenever SkyVPN runs. */
    const val PREF_KEY = "vpn_hotspot"

    /**
     * Abstract socket the visor collects the connections from. Kept in step
     * with the core's `SKYWIRE_ANDROID_VPN_SHARE_SOCKET` (see [coreEnv]).
     */
    const val SOCKET_NAME = "com.skycoin.skywire.vpn.share"

    internal val mutableState = MutableStateFlow(VpnHotspotState())
    val state = mutableState.asStateFlow()
}

/** What [HotspotShare] is doing, for the SkyVPN screen. */
data class VpnHotspotState(
    /** The proxy port is open. */
    val serving: Boolean = false,
    /** This phone's hotspot addresses; empty while no hotspot is on. */
    val addresses: List<String> = emptyList(),
    /** Devices that used the proxy in the last few minutes. */
    val devices: Int = 0,
    /** Why the port could not be opened. */
    val error: String? = null,
)

/**
 * The hotspot half of [VpnHotspot], owned by [SkyVpnService]: it cannot
 * outlive the VPN it shares.
 */
internal class HotspotShare(private val context: Context, private val scope: CoroutineScope) {

    private val sinkLock = Any()

    /** The visor's end of the handoff channel; null while it is not connected. */
    @Volatile private var sink: LocalSocket? = null
    @Volatile private var sinkServer: LocalServerSocket? = null
    @Volatile private var listener: ServerSocket? = null
    private var sinkJob: Job? = null
    private var serveJob: Job? = null
    private var watchJob: Job? = null

    @Volatile private var addresses: Set<String> = emptySet()

    /** Device address → when it was last handed over. */
    private val recent = ConcurrentHashMap<String, Long>()

    /**
     * Opens the channel the visor collects connections from. Always, not only
     * when sharing is on: vpn-client dials it once per run, and a channel that
     * is already there is one it never has to retry for.
     */
    fun start() {
        if (sinkJob?.isActive == true) return
        sinkJob = scope.launch {
            val server = bindSink() ?: return@launch
            try {
                while (isActive) {
                    val peer = server.accept()
                    // The abstract namespace has no permissions of its own:
                    // only our own process — the visor — may take connections.
                    val uid = runCatching { peer.peerCredentials.uid }.getOrNull()
                    if (uid != Process.myUid()) {
                        runCatching { peer.close() }
                        continue
                    }
                    adopt(peer)
                }
            } catch (e: IOException) {
                // accept() throwing is how closing the socket stops this loop.
            } finally {
                runCatching { server.close() }
                if (boundSink === server) boundSink = null
                if (sinkServer === server) sinkServer = null
            }
        }
    }

    /** Opens or closes the proxy port. */
    fun setEnabled(on: Boolean) {
        if (on) serve() else stopServing()
    }

    fun stop() {
        stopServing()
        sinkJob?.cancel()
        sinkJob = null
        runCatching { sinkServer?.close() }
        sinkServer = null
        synchronized(sinkLock) {
            runCatching { sink?.close() }
            sink = null
        }
    }

    // --- the visor's channel ---

    /** Same reclaim-and-retry as the VPN socket: see SkyVpnService.bind. */
    private suspend fun bindSink(): LocalServerSocket? {
        repeat(BIND_ATTEMPTS) { attempt ->
            runCatching { boundSink?.close() }
            boundSink = null
            try {
                return LocalServerSocket(VpnHotspot.SOCKET_NAME).also {
                    boundSink = it
                    sinkServer = it
                }
            } catch (e: IOException) {
                if (attempt < BIND_ATTEMPTS - 1) delay(BIND_RETRY_MS)
            }
        }
        return null
    }

    /**
     * The newest connection is the visor that is running now; an older one
     * is a vpn-client that has already stopped.
     */
    private fun adopt(peer: LocalSocket) {
        synchronized(sinkLock) {
            runCatching { sink?.close() }
            sink = peer
        }
        // Nothing is ever read from the visor: this only notices it leaving.
        scope.launch {
            runCatching { while (peer.inputStream.read() >= 0) Unit }
            synchronized(sinkLock) {
                if (sink === peer) sink = null
            }
            runCatching { peer.close() }
        }
    }

    // --- the proxy port ---

    private fun serve() {
        if (serveJob?.isActive == true) return
        serveJob = scope.launch {
            val server = try {
                ServerSocket().apply {
                    reuseAddress = true
                    // IPv4 only, like the tunnel. A wildcard bind is what lets
                    // the hotspot come and go (and change address) without
                    // reopening; who may use it is checked per connection.
                    bind(InetSocketAddress(InetAddress.getByName("0.0.0.0"), VpnHotspot.PORT), BACKLOG)
                }
            } catch (e: IOException) {
                VpnHotspot.mutableState.update { it.copy(serving = false, error = e.message ?: e.toString()) }
                return@launch
            }
            listener = server
            VpnHotspot.mutableState.update { it.copy(serving = true, error = null) }
            try {
                while (isActive) hand(server.accept())
            } catch (e: IOException) {
                // Closed by stopServing.
            } finally {
                runCatching { server.close() }
                if (listener === server) listener = null
                VpnHotspot.mutableState.update { it.copy(serving = false) }
            }
        }
        watchJob = scope.launch {
            while (isActive) {
                refreshAddresses()
                delay(REFRESH_MS)
            }
        }
    }

    private fun stopServing() {
        serveJob?.cancel()
        serveJob = null
        watchJob?.cancel()
        watchJob = null
        runCatching { listener?.close() }
        listener = null
        recent.clear()
        VpnHotspot.mutableState.value = VpnHotspotState()
    }

    /**
     * Passes [client] to the visor, or turns it away. Either way this side's
     * copy is closed: the visor holds its own duplicate from here on.
     */
    private fun hand(client: Socket) {
        client.use {
            val local = client.localAddress?.hostAddress ?: return
            if (local !in addresses) {
                // The hotspot may have come up since the last look.
                refreshAddresses()
                if (local !in addresses) return
            }
            val device = client.inetAddress?.hostAddress
            val handed = synchronized(sinkLock) {
                val out = sink ?: return
                val pfd = ParcelFileDescriptor.fromSocket(client) ?: return
                pfd.use {
                    try {
                        out.setFileDescriptorsForSend(arrayOf(pfd.fileDescriptor))
                        out.outputStream.write(1)
                        true
                    } catch (e: IOException) {
                        runCatching { out.close() }
                        if (sink === out) sink = null
                        false
                    } finally {
                        out.setFileDescriptorsForSend(null)
                    }
                }
            }
            if (handed && device != null) {
                recent[device] = SystemClock.elapsedRealtime()
                publishDevices()
            }
        }
    }

    private fun refreshAddresses() {
        val found = HotspotInterfaces.addresses(context)
        addresses = found.toSet()
        VpnHotspot.mutableState.update { it.copy(addresses = found) }
        publishDevices()
    }

    private fun publishDevices() {
        val since = SystemClock.elapsedRealtime() - DEVICE_WINDOW_MS
        recent.entries.removeIf { it.value < since }
        VpnHotspot.mutableState.update { it.copy(devices = recent.size) }
    }

    private companion object {
        /** Process-wide, as SkyVpnService's own listener is, and for the same reason. */
        @Volatile var boundSink: LocalServerSocket? = null

        const val BIND_ATTEMPTS = 3
        const val BIND_RETRY_MS = 250L
        const val BACKLOG = 64
        const val REFRESH_MS = 3_000L

        /** "Using it now", for a device whose browser holds no connection open. */
        const val DEVICE_WINDOW_MS = 5 * 60_000L
    }
}

/** This phone's hotspot addresses — the ones the proxy answers on. */
object HotspotInterfaces {
    fun addresses(context: Context): List<String> {
        val cm = context.getSystemService(ConnectivityManager::class.java)
        // Every network the phone is a CLIENT of: Wi-Fi it joined, mobile
        // data, a VPN. Tethering interfaces are not networks to the system.
        @Suppress("DEPRECATION")
        val uplinks = runCatching {
            cm?.allNetworks.orEmpty().mapNotNull { cm?.getLinkProperties(it)?.interfaceName }.toSet()
        }.getOrDefault(emptySet())
        val interfaces = runCatching {
            NetworkInterface.getNetworkInterfaces()?.toList().orEmpty().mapNotNull { ni ->
                runCatching {
                    NetIface(
                        name = ni.name,
                        up = ni.isUp,
                        loopback = ni.isLoopback,
                        ipv4 = ni.inetAddresses.toList().filterIsInstance<Inet4Address>()
                            .mapNotNull { it.hostAddress },
                    )
                }.getOrNull()
            }
        }.getOrDefault(emptyList())
        return hotspotAddresses(interfaces, uplinks)
    }
}

/** One interface as [hotspotAddresses] needs to see it. */
internal data class NetIface(
    val name: String,
    val up: Boolean,
    val loopback: Boolean,
    val ipv4: List<String>,
)

/**
 * Interface names that are never a hotspot, whatever the system lists: the
 * VPN's own, mobile data (and its 464xlat and IMS siblings, which are not all
 * networks an app can see) and the kernel's placeholders.
 */
private val NOT_HOTSPOT = listOf("tun", "rmnet", "ccmni", "v4-", "clat", "dummy", "ppp", "seth", "ifb", "lo")

/**
 * The IPv4 addresses a hotspot device can reach this phone on: interfaces
 * that are up, not an uplink the phone is a client of ([uplinks]), not a
 * known non-hotspot kind, and carrying a private address — which every
 * Wi-Fi, USB and Bluetooth tethering subnet Android hands out is.
 */
internal fun hotspotAddresses(interfaces: List<NetIface>, uplinks: Set<String>): List<String> =
    interfaces.asSequence()
        .filter { it.up && !it.loopback && it.name !in uplinks }
        .filter { iface -> NOT_HOTSPOT.none { iface.name.startsWith(it) } }
        .flatMap { it.ipv4.asSequence() }
        .filter(::isPrivateIpv4)
        .distinct()
        .toList()

internal fun isPrivateIpv4(address: String): Boolean {
    val octets = address.split('.').map { it.toIntOrNull() ?: return false }
    if (octets.size != 4 || octets.any { it !in 0..255 }) return false
    return octets[0] == 10 ||
        (octets[0] == 172 && octets[1] in 16..31) ||
        (octets[0] == 192 && octets[1] == 168)
}

package com.skycoin.skywire.core

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageManager
import android.net.LocalServerSocket
import android.net.LocalSocket
import android.net.VpnService
import android.os.ParcelFileDescriptor
import android.os.Process
import com.skycoin.skywire.MainActivity
import com.skycoin.skywire.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.drop
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import java.io.IOException
import java.util.Collections

/**
 * Owner of the phone's VPN interface.
 *
 * Android hands out a TUN only through [VpnService], and only after the user
 * has granted the VPN consent — an app can never open `/dev/net/tun` itself.
 * So the interface is built here, from the parameters the VPN server assigned
 * to vpn-client, and its file descriptor is passed to the visor process over
 * an abstract unix socket as `SCM_RIGHTS` ancillary data. The Go end of that
 * handoff is `pkg/vpn/tun_device_android.go`, which documents the protocol.
 *
 * Two things about this service carry the design:
 *
 *  - **`addDisallowedApplication(self)`.** The visor runs as a child of this
 *    app, so it shares this UID; excluding the package excludes it. Without
 *    that the visor's own dmsg traffic would be routed into the tunnel it is
 *    carrying, and the VPN would deadlock on the first packet.
 *  - **The interface outlives the connection.** This service keeps its own
 *    descriptor open, so the interface stays up even after the core closes
 *    its copy. While the tunnel is down nothing drains the interface, and the
 *    packets go nowhere — that is what makes vpn-client's `--killswitch` real
 *    rather than advisory. With the killswitch off the core says `down`
 *    before it stops carrying traffic, and the interface goes away so the
 *    phone gets its normal networking back.
 *
 * Not a foreground service, deliberately: it shares a process with
 * [SkywireCoreService], which is one and runs whenever the VPN could — the
 * VPN cannot outlive the visor that carries it (see the core watcher below).
 * The system draws its own persistent VPN indicator on top of that.
 */
class SkyVpnService : VpnService() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.IO)
    private val json = Json { ignoreUnknownKeys = true }

    /** One interface change at a time, whichever connection asks. */
    private val mutex = Mutex()

    @Volatile private var server: LocalServerSocket? = null
    @Volatile private var acceptor: Job? = null
    @Volatile private var coreWatcher: Job? = null

    /**
     * Live control connections. Tracked only so shutdown can close them: a
     * reader parked in a blocking `readLine` does not notice its coroutine
     * being cancelled, and closing the socket is what wakes it.
     */
    private val clients = Collections.synchronizedSet(mutableSetOf<LocalSocket>())

    /** Our copy of the live interface — the app's claim on its lifetime. */
    private var tun: ParcelFileDescriptor? = null

    /** The connection that last established; only its EOF means anything. */
    @Volatile private var controller: LocalSocket? = null

    @Volatile private var killswitch = false

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> {
                shutdown()
                stopSelf()
                return START_NOT_STICKY
            }
            ACTION_KILLSWITCH ->
                killswitch = intent.getBooleanExtra(EXTRA_KILLSWITCH, killswitch)
            else -> {
                killswitch = intent?.getBooleanExtra(EXTRA_KILLSWITCH, killswitch) ?: killswitch
                listen()
                watchCore()
            }
        }
        // The interface dies with this process, so there is nothing for a
        // revival to resume — the screen re-drives the whole flow instead.
        return START_NOT_STICKY
    }

    /** The user turned the VPN off in Settings, or another VPN took over. */
    override fun onRevoke() {
        shutdown()
        stopSelf()
        super.onRevoke()
    }

    override fun onDestroy() {
        shutdown()
        scope.cancel()
        super.onDestroy()
    }

    // --- the control socket ---

    private fun listen() {
        if (acceptor?.isActive == true) return
        acceptor = scope.launch {
            val socket = bind() ?: return@launch
            try {
                // A shutdown that raced the bind lands here with the job
                // already cancelled; fall through to the close below rather
                // than publish a listener nobody will tear down.
                if (isActive) {
                    server = socket
                    VpnTunnel.mutableState.value = VpnTunnelState(serviceUp = true)
                    while (isActive) {
                        val client = socket.accept()
                        launch { serve(client) }
                    }
                }
            } catch (e: IOException) {
                // accept() throwing is how closing the socket stops this loop.
            } finally {
                // The binder owns the close. An abstract name stays bound
                // until its fd closes or the process dies, and this process
                // hosts the foreground core service, so it does not die —
                // any exit that skipped this close used to wedge the VPN
                // with EADDRINUSE until a force-stop.
                runCatching { socket.close() }
                if (boundServer === socket) boundServer = null
                if (server === socket) server = null
            }
        }
    }

    /**
     * Binds the abstract name, first reclaiming any listener a predecessor
     * service instance left bound: the name is process-wide but the field
     * holding it used to be per-instance, so an orphan was unreachable and
     * every rebind failed until the process died. The brief retry covers a
     * predecessor that is still mid-close.
     */
    private suspend fun bind(): LocalServerSocket? {
        var last: IOException? = null
        repeat(BIND_ATTEMPTS) { attempt ->
            runCatching { boundServer?.close() }
            boundServer = null
            try {
                return LocalServerSocket(SOCKET_NAME).also { boundServer = it }
            } catch (e: IOException) {
                last = e
            }
            if (attempt < BIND_ATTEMPTS - 1) delay(BIND_RETRY_MS)
        }
        VpnTunnel.mutableState.value = VpnTunnelState(
            error = getString(R.string.vpn_error_socket, last?.message.orEmpty()),
        )
        return null
    }

    /**
     * Reads control lines from one connection until it ends.
     *
     * The abstract namespace has no filesystem permissions to lean on, so the
     * peer's UID is the whole gate: only our own processes — which is to say
     * the visor we spawned — may ask for the phone's TUN.
     */
    private suspend fun serve(client: LocalSocket) {
        clients += client
        try {
            if (client.peerCredentials.uid != Process.myUid()) return
            val reader = client.inputStream.bufferedReader()
            while (true) {
                val line = reader.readLine() ?: break
                handle(client, line)
            }
        } catch (e: IOException) {
            // A broken control channel is the core going away; handled below.
        } finally {
            clients -= client
            runCatching { client.close() }
            if (controller === client) {
                controller = null
                onCoreGone()
            }
        }
    }

    private suspend fun handle(client: LocalSocket, line: String) = mutex.withLock {
        val request = runCatching {
            json.decodeFromString(TunRequest.serializer(), line)
        }.getOrNull()
        when (request?.op) {
            OP_ESTABLISH -> establish(client, request)
            OP_DOWN -> {
                closeTun()
                reply(client, ok = true)
            }
            else -> reply(client, ok = false, error = "unknown request")
        }
    }

    /**
     * Builds the interface the core asked for and hands its descriptor over.
     *
     * Establishing again while one is already up is the sanctioned reconnect:
     * the system swaps the interfaces without a gap, which is why the old
     * descriptor is only closed *after* the new one exists — closing first
     * would open a window with no VPN and leak traffic straight out.
     */
    private fun establish(client: LocalSocket, request: TunRequest) {
        val address = request.addr.substringBefore('/')
        val prefix = request.addr.substringAfter('/', "").toIntOrNull()
        if (address.isEmpty() || prefix == null) {
            reply(client, ok = false, error = "malformed address ${request.addr}")
            return
        }

        val established = try {
            val builder = Builder()
                .setSession(getString(R.string.app_skyvpn))
                .addAddress(address, prefix)
                // Everything. The client's own routing declares this as the
                // two half-spaces; a default route is the same statement.
                .addRoute("0.0.0.0", 0)
                .setMtu(request.mtu.takeIf { it > 0 } ?: DEFAULT_MTU)
                // A full tunnel with no resolver of its own would leave the
                // phone pointed at whatever DNS its Wi-Fi handed out — often
                // a LAN address that is unreachable from the other end of the
                // tunnel. `--dns` overrides this.
                .addDnsServer(request.dns.ifEmpty { DEFAULT_DNS })
                // The Go side polls this descriptor; blocking reads would
                // park in the kernel with no way to interrupt them.
                .setBlocking(false)
                .setConfigureIntent(configureIntent())
            // The visor is our own child process and shares this UID, so its
            // dmsg traffic — the traffic CARRYING the tunnel — must stay out
            // of it. Excluding the package is what keeps it out.
            builder.addDisallowedApplication(packageName)
            builder.establish()
        } catch (e: PackageManager.NameNotFoundException) {
            reply(client, ok = false, error = "cannot exclude self from the tunnel: ${e.message}")
            return
        } catch (e: IllegalArgumentException) {
            reply(client, ok = false, error = "rejected interface parameters: ${e.message}")
            return
        } catch (e: IllegalStateException) {
            reply(client, ok = false, error = "rejected interface parameters: ${e.message}")
            return
        }

        if (established == null) {
            // Consent was never granted, or was revoked while we ran.
            reply(client, ok = false, error = getString(R.string.vpn_error_no_consent))
            VpnTunnel.mutableState.value = VpnTunnel.mutableState.value.copy(
                established = false,
                error = getString(R.string.vpn_error_no_consent),
            )
            return
        }

        // The fd rides along with the reply as SCM_RIGHTS; the receiver gets
        // its own dup, which is why ours stays open and holds the interface.
        client.setFileDescriptorsForSend(arrayOf(established.fileDescriptor))
        val sent = reply(client, ok = true)
        client.setFileDescriptorsForSend(null)
        if (!sent) {
            runCatching { established.close() }
            return
        }

        runCatching { tun?.close() }
        tun = established
        controller = client
        VpnTunnel.mutableState.value = VpnTunnelState(
            serviceUp = true,
            established = true,
            address = request.addr,
        )
    }

    /** Drops the interface, handing the phone back its normal networking. */
    private fun closeTun() {
        val current = tun ?: return
        tun = null
        runCatching { current.close() }
        VpnTunnel.mutableState.value = VpnTunnel.mutableState.value.copy(
            established = false,
            address = "",
        )
    }

    /**
     * The control channel ended without a `down`: the visor was killed rather
     * than stopped. With the killswitch off that is the cue to release the
     * phone; with it on, keeping the block is precisely what was asked for —
     * the user disconnects to lift it.
     */
    private fun onCoreGone() {
        if (killswitch) return
        scope.launch { mutex.withLock { closeTun() } }
    }

    /**
     * The tunnel cannot outlive the visor that carries it. A crash-restart is
     * not that — [CoreState.Restarting] keeps the interface (and the block)
     * in place — but a user Disconnect or a terminal failure is, and leaving
     * the phone blocked with nothing left to reconnect would be a bug, not a
     * killswitch.
     */
    private fun watchCore() {
        if (coreWatcher?.isActive == true) return
        coreWatcher = scope.launch {
            // Drop the replayed snapshot: this watcher exists to notice the
            // core GOING away, not to veto a start that raced one. Acting on
            // the stale initial value used to tear down the listener the
            // sibling coroutine was busy binding.
            CoreServiceState.state.drop(1).collect { core ->
                if (core is CoreState.Stopped || core is CoreState.Failed) {
                    shutdown()
                    stopSelf()
                }
            }
        }
    }

    private fun shutdown() {
        acceptor?.cancel()
        acceptor = null
        coreWatcher?.cancel()
        coreWatcher = null
        runCatching { server?.close() }
        server = null
        controller = null
        synchronized(clients) { clients.toList() }.forEach { runCatching { it.close() } }
        clients.clear()
        runCatching { tun?.close() }
        tun = null
        VpnTunnel.mutableState.value = VpnTunnelState()
    }

    /** Tapping the system's VPN indicator lands on the app. */
    private fun configureIntent(): PendingIntent = PendingIntent.getActivity(
        this,
        0,
        Intent(this, MainActivity::class.java),
        PendingIntent.FLAG_IMMUTABLE,
    )

    /** True when the reply reached the peer. */
    private fun reply(client: LocalSocket, ok: Boolean, error: String? = null): Boolean =
        runCatching {
            val body = json.encodeToString(TunReply.serializer(), TunReply(ok, error.orEmpty()))
            client.outputStream.apply {
                write((body + "\n").toByteArray())
                flush()
            }
            true
        }.getOrDefault(false)

    companion object {
        /**
         * Abstract-namespace socket the visor dials. Kept in step with the
         * core's `SKYWIRE_ANDROID_VPN_SOCKET` (see [coreEnv]), which is how
         * the Go side learns the name.
         */
        const val SOCKET_NAME = "com.skycoin.skywire.vpn"

        /**
         * The one bound listener in this process, whichever service instance
         * bound it. Process-scoped so a fresh instance can close a socket a
         * dead predecessor orphaned — that is what turns "Address already in
         * use" from a permanent failure into a self-healing one.
         */
        @Volatile private var boundServer: LocalServerSocket? = null

        private const val BIND_ATTEMPTS = 3
        private const val BIND_RETRY_MS = 250L

        private const val ACTION_START = "com.skycoin.skywire.vpn.START"
        private const val ACTION_STOP = "com.skycoin.skywire.vpn.STOP"
        private const val ACTION_KILLSWITCH = "com.skycoin.skywire.vpn.KILLSWITCH"
        private const val EXTRA_KILLSWITCH = "killswitch"

        private const val OP_ESTABLISH = "establish"
        private const val OP_DOWN = "down"

        /** Matches pkg/vpn's TUNMTU; the core sends its own value anyway. */
        private const val DEFAULT_MTU = 1500

        /** Only used when vpn-client was given no `--dns`. */
        private const val DEFAULT_DNS = "1.1.1.1"

        fun start(context: Context, killswitch: Boolean) {
            context.startService(
                Intent(context, SkyVpnService::class.java)
                    .setAction(ACTION_START)
                    .putExtra(EXTRA_KILLSWITCH, killswitch),
            )
        }

        fun stop(context: Context) {
            context.startService(
                Intent(context, SkyVpnService::class.java).setAction(ACTION_STOP),
            )
        }

        /** Changes what happens to the interface when the core goes away. */
        fun setKillswitch(context: Context, killswitch: Boolean) {
            context.startService(
                Intent(context, SkyVpnService::class.java)
                    .setAction(ACTION_KILLSWITCH)
                    .putExtra(EXTRA_KILLSWITCH, killswitch),
            )
        }
    }
}

/** One control line from the core. See pkg/vpn/tun_device_android.go. */
@Serializable
private data class TunRequest(
    @SerialName("op") val op: String = "",
    @SerialName("addr") val addr: String = "",
    @SerialName("gateway") val gateway: String = "",
    @SerialName("mtu") val mtu: Int = 0,
    @SerialName("dns") val dns: String = "",
)

@Serializable
private data class TunReply(
    @SerialName("ok") val ok: Boolean,
    @SerialName("error") val error: String = "",
)

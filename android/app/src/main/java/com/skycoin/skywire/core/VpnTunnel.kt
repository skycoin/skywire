package com.skycoin.skywire.core

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.asStateFlow

/** What [SkyVpnService] has done with the phone's network interface. */
data class VpnTunnelState(
    /** The service is up and the visor can ask it for an interface. */
    val serviceUp: Boolean = false,
    /**
     * An interface is established: every other app's traffic is being routed
     * into it. That is not the same as connected — while the tunnel is down
     * and the killswitch holds this open, the traffic goes nowhere, which is
     * the whole point of the setting.
     */
    val established: Boolean = false,
    /** Address the core asked for, e.g. `192.168.255.6/29`. */
    val address: String = "",
    /** Last failure putting an interface up; cleared by the next success. */
    val error: String? = null,
)

/**
 * In-process bridge between [SkyVpnService] and the UI, the same shape as
 * [CoreServiceState] — service and Compose tree share a process, so a
 * StateFlow is the whole of it.
 */
object VpnTunnel {
    internal val mutableState = MutableStateFlow(VpnTunnelState())
    val state = mutableState.asStateFlow()
}

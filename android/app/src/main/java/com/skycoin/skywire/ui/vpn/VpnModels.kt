package com.skycoin.skywire.ui.vpn

import com.skycoin.skywire.core.argValue

/**
 * The two vpn-client flags this screen reads.
 *
 * Neither is ever written as raw argv: the visor owns both spellings — `--srv`
 * through the PUT's `pk` field and `--killswitch` through its `killswitch`
 * field, which adds the bare flag or strips it. This side only has to
 * recognise what came back, including the `--flag=value` form a hand-edited
 * config can carry.
 */
object VpnArgs {

    const val APP = "vpn-client"

    /**
     * Where the last-used exit lives in [com.skycoin.skywire.core.AppPreferences]
     * (a [com.skycoin.skywire.ui.components.SavedServer] as JSON). Owned by the
     * SkyVPN screen; the apps hub reads it for the exit's country and to turn
     * the tunnel back on from the hero card.
     */
    const val PREF_LAST_SERVER = "vpn_last_server"

    /** The phone's killswitch preference — same arrangement, same owners. */
    const val PREF_KILLSWITCH = "vpn_killswitch"

    private val SRV = listOf("--srv", "-srv")

    fun serverPk(args: List<String>): String? =
        argValue(args, SRV)?.takeIf { it.isNotEmpty() }

    fun killswitch(args: List<String>): Boolean = args.any { token ->
        val bare = token.trimStart('-')
        bare == KILLSWITCH || (
            bare.startsWith("$KILLSWITCH=") && bare.substringAfter('=') != "false"
            )
    }

    private const val KILLSWITCH = "killswitch"
}

/**
 * The visor's own wording for what vpn-client is doing, as it reports it in
 * `detailed_status`. Matched as strings because that is what the API sends —
 * the constants live in pkg/app/appserver/app_state.go.
 */
object VpnStatus {
    const val RUNNING = "Running"
    const val CONNECTING = "Connecting"
    const val RECONNECTING = "Connection failed, reconnecting"
}

package com.skycoin.skywire.core

/**
 * Automatic transports to public visors — one bool, off on this build.
 *
 * A visor with this on polls service discovery and opens an STCPR transport to
 * every public visor it finds, on a five-minute cycle, whether or not anything
 * is using them. That is how a desktop node makes itself useful to the network:
 * more transports mean more paths for everyone's routes, and the node earns
 * from carrying them.
 *
 * `config gen` for this phone passes `--autoconn`, which sets
 * `transport.public_autoconnect = false` — the phone opts out by default, and
 * the reasons are the phone's: a handset on mobile data pays for those
 * transports in battery and in a radio that never idles, and the transports
 * are mostly to the network's benefit rather than this device's. A visor
 * behind carrier NAT often cannot complete them anyway.
 *
 * It is worth having as a choice rather than a decision made for everyone: on
 * wifi and charging, a phone that carries transports is a phone contributing
 * to the network, and multi-hop routing needs transports to exist somewhere —
 * see the min-hops failure in DialRoutes, where a visor holding none cannot
 * route through anything.
 *
 * The visor reads it when it builds its transport manager, so changing it
 * means restarting the core — the same deal [Fleet] has.
 */
object PublicAutoconnect {

    /** [AppPreferences] key holding the user's choice. The app owns it. */
    const val PREF_KEY = "public_autoconnect"

    /** Off, matching what `config gen --autoconn` writes for this phone. */
    const val DEFAULT = false

    /** Field inside the config's `transport` object. */
    const val CONFIG_KEY = "public_autoconnect"
}

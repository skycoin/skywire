package com.skycoin.skywire.core

/**
 * The Fleet opt-in — one bool, and the only thing on this phone that opens a
 * listener the outside world can reach.
 *
 * The phone's core ships API-only: the visor serves its local HTTP API on
 * loopback and nothing else. The section that API lives under is historically
 * called `hypervisor`, but on this build it is just the API — no manager, no
 * remote visors. Turning Fleet on hands that hypervisor its dmsg client, which
 * starts the RPC listener other visors dial in on. From then on any visor
 * carrying this phone's PK in its own `hypervisors` list connects over dmsg and
 * reports its status here.
 *
 * Off by default, deliberately: a listener nobody asked for is battery spent on
 * connections nobody wants, and the feature is worth nothing until the user has
 * actually put this key into another visor's config.
 *
 * The bool is read exactly once, when the visor builds its module graph, so
 * changing it means restarting the core — see [SkywireCoreService.restart].
 */
object Fleet {

    /** [AppPreferences] key holding the user's choice. The app owns it. */
    const val PREF_KEY = "fleet_enabled"

    /** Off until the user turns it on. */
    const val DEFAULT = false

    /** Field inside the config's `hypervisor` object. */
    const val CONFIG_KEY = "dmsg_ingest"
}

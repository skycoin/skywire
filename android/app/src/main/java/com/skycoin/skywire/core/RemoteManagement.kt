package com.skycoin.skywire.core

/**
 * The remote-management grant — one public key, and the only thing that lets
 * a machine other than this phone drive this visor.
 *
 * The granted key goes into the config's top-level `hypervisors` list, which
 * is both halves of remote access at once: the visor dials OUT to that key as
 * its hypervisor (so the phone appears in `hv ls` / the hypervisor UI on that
 * machine), and the key is admitted INBOUND on the visor's dmsg RPC surfaces,
 * which lets `skywire cli --via dmsg://<this-phone>` run the full CLI against
 * it — start the VPN or proxy client, change routing, follow the logs. That
 * is what turns a tester's "it doesn't connect" into a trace someone at a
 * desk can read (docs/guides/remote-visor-cli.md).
 *
 * The grant is deliberately narrower than on a desktop visor: this phone
 * runs no dmsgpty and pins dmsgscp off, so the key gets the typed API only —
 * no remote shell, no file access.
 *
 * Off by default, and one key rather than a list: nothing on the phone is
 * remotely reachable until its owner pastes a key in, and "the machine on my
 * desk" is the use case — a fleet wants the desktop-managed direction, not
 * this. Read at config-rewrite time, so granting or revoking takes effect
 * when the core next starts — same arrangement as [Fleet] and
 * [PublicAutoconnect].
 */
object RemoteManagement {

    /** [AppPreferences] key holding the granted PK; absent = nothing granted. */
    const val PREF_KEY = "remote_management_pk"

    /** 33 bytes of compressed secp256k1 public key, as hex. */
    const val PK_HEX_LENGTH = 66

    /**
     * Normalize [raw] to the lowercase hex the config carries; null when it
     * is not shaped like a visor public key. Shape-only on purpose — the same
     * check [ConfigManager] applies to keys, with the same rationale: real
     * validation is key handling, and key handling stays in the core.
     */
    fun sanitize(raw: String?): String? {
        val pk = raw?.trim()?.lowercase() ?: return null
        if (pk.length != PK_HEX_LENGTH) return null
        if (!pk.all { it.isDigit() || it in 'a'..'f' }) return null
        return pk
    }
}

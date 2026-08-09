package com.skycoin.skywire.core

/**
 * Which transport type the visor reaches for FIRST — the primary — with
 * every other type left in place behind it as a fallback.
 *
 * One visor-wide order drives two decisions in the core: which type it
 * tries to *create* when a route needs a transport that isn't there yet,
 * and which existing transport a route *rides* when several reach the same
 * peer. So picking a primary here is exactly "use this one if you can,
 * otherwise the others" — never "only this one".
 *
 * It is a visor setting, not a per-app one: SkySOCKS is where the phone
 * exposes it first, and SkyVPN will show the same value.
 */
object TransportPreference {

    /** Relayed through a dmsg server. Always reachable — no NAT to beat. */
    const val DMSG = "dmsg"

    /** Direct TCP, peer address from the address resolver. */
    const val STCPR = "stcpr"

    /** Direct UDP with hole punching; needs a STUN-friendly NAT. */
    const val SUDPH = "sudph"

    /**
     * The types offered as a primary. Deliberately the three the visor's
     * transport-creation path can be asked to establish on demand — the
     * rest of the taxonomy (quic, ws, wt, webrtc, stcp) is listener- or
     * browser-side and never created for an outgoing app dial, so offering
     * it here would promise something the core would not honor.
     */
    val choices = listOf(DMSG, STCPR, SUDPH)

    /**
     * dmsg, on a phone. A mobile link sits behind carrier NAT with no
     * inbound reachability, so stcpr almost never establishes and sudph
     * needs a NAT type the carrier rarely gives — yet the core's built-in
     * order tries both first, spending up to ~20 s (the STUN wait plus the
     * dial retries) before it reaches dmsg anyway. Starting at dmsg is the
     * shortest path to a working route here; the others stay as fallbacks
     * for the Wi-Fi case where they can win.
     */
    const val DEFAULT = DMSG

    /** [AppPreferences] key holding the user's choice. */
    const val PREF_KEY = "transport_primary"

    /**
     * The core's own default order. Kept whole so hoisting a primary out of
     * it preserves the relative order of everything else, instead of
     * flattening the untouched types to "unranked" (which is what sending a
     * one-element order would do). A type the core adds later simply sorts
     * last until this list catches up.
     */
    private val CORE_ORDER = listOf(
        "stcpr", "squicr", "sudph", "stcp", "webrtc", "swsr", "swtr", "dmsg",
    )

    /** The full priority order to hand the visor, [primary] in front. */
    fun order(primary: String): List<String> =
        listOf(primary) + CORE_ORDER.filter { it != primary }

    /** A stored/unknown value mapped back onto a supported choice. */
    fun sanitize(value: String?): String = value?.takeIf { it in choices } ?: DEFAULT
}

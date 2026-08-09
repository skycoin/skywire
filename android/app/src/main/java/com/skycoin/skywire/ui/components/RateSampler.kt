package com.skycoin.skywire.ui.components

import android.os.SystemClock

/**
 * Turns a pair of monotonically-rising byte counters into a rate.
 *
 * The visor reports `upload_speed` / `download_speed` on an app connection,
 * but those are not derived from the byte counters: the route group exchanges
 * them inside its ping/pong keepalive — `handlePingPacket` stores whatever
 * throughput the far side announced, and the download figure is the remote
 * throughput echoed back. That needs an active route group, a cooperating
 * exit and a completed ping round; on a phone the pair sits at zero and never
 * moves, which is why the SkyVPN screen only ever showed its rate row behind
 * a `> 0` guard.
 *
 * `bandwidth_sent` / `bandwidth_received` do move, so the rate is measured
 * here instead: bytes gained since the previous sample over the time between
 * them. Deliberately local — nothing is asked of the visor that it is not
 * already being asked.
 */
class RateSampler {

    private var lastSent = 0L
    private var lastReceived = 0L
    private var lastAt = 0L

    /** Bytes per second since the previous [sample]; null until there are two. */
    data class Rates(val upBytesPerSec: Long, val downBytesPerSec: Long)

    /**
     * Feed the current counters. Returns null on the first call and whenever
     * the counters restart, since neither yields a rate anyone should read.
     *
     * elapsedRealtime, not wall clock: a clock correction mid-session would
     * otherwise divide by a negative or absurd interval and print a rate in
     * gigabytes.
     */
    fun sample(sent: Long, received: Long): Rates? {
        val now = SystemClock.elapsedRealtime()
        val previousAt = lastAt
        val previousSent = lastSent
        val previousReceived = lastReceived

        lastAt = now
        lastSent = sent
        lastReceived = received

        if (previousAt == 0L) return null
        val millis = now - previousAt
        if (millis <= 0) return null
        // A counter that went backwards means the app restarted its
        // connection; the delta across that boundary is meaningless.
        if (sent < previousSent || received < previousReceived) return null

        return Rates(
            upBytesPerSec = (sent - previousSent) * 1000 / millis,
            downBytesPerSec = (received - previousReceived) * 1000 / millis,
        )
    }

    /** Forget the history — the connection this was measuring is gone. */
    fun reset() {
        lastAt = 0
        lastSent = 0
        lastReceived = 0
    }
}

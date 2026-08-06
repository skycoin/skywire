package com.skycoin.skywire.core

import android.os.SystemClock
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/**
 * Whether the app is currently behind its biometric lock.
 *
 * A process-level object because the lock is a property of the app, not of any
 * one screen: the Activity is recreated on every rotation and theme change, and
 * a lock that reset with it would be no lock at all.
 *
 * The state starts *locked*. A fresh process is exactly the case that must ask
 * — the phone was rebooted, the app was killed, someone launched it cold — and
 * starting unlocked would mean every process death is a free pass. The gate
 * ignores this entirely while the preference is off, so the cost of that
 * default is nothing for the users who never turn the lock on.
 */
object AppLock {

    /** [AppPreferences] key holding the user's choice. */
    const val PREF_KEY = "app_lock_enabled"

    /** Off until asked for. */
    const val DEFAULT = false

    /**
     * How long the app may be away before it locks again.
     *
     * The point is the interruption, not the absence: answering a message,
     * copying a code out of another app and coming back is one task, and a
     * lock that fires in the middle of it is the reason people turn locks off.
     * Half a minute is long enough for that round trip and far short of
     * "someone else picked up the phone".
     */
    const val GRACE_MS = 30_000L

    private val locked = MutableStateFlow(true)
    val isLocked: StateFlow<Boolean> = locked.asStateFlow()

    /**
     * When the app last went to the background. Zero until it has — which is
     * what makes a cold start lock: `now - 0` is past any grace period.
     */
    @Volatile
    private var leftAt = 0L

    /** From the Activity's `onStop`. */
    fun onBackground() {
        leftAt = SystemClock.elapsedRealtime()
    }

    /**
     * From the Activity's `onStart`. Re-locks unless the user has been gone for
     * less than [GRACE_MS]. Runs whether or not the lock is enabled — the gate
     * is what consults the preference — so that turning the lock on does not
     * have to reconstruct a history of absences it never watched.
     */
    fun onForeground() {
        if (SystemClock.elapsedRealtime() - leftAt > GRACE_MS) locked.value = true
    }

    /**
     * A biometric check just passed. Also called when the user turns the lock
     * *on*, which is gated by the same check: having just proved themselves,
     * they should not be asked twice in a row.
     */
    fun unlock() {
        locked.value = false
    }
}

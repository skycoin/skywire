package com.skycoin.skywire.core

import android.annotation.SuppressLint
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.PowerManager
import android.provider.Settings

/**
 * Whether the system will let the visor keep running when the screen has been
 * off for a while, and how to ask it to.
 *
 * **What Doze actually does to us.** The core runs in a foreground service, so
 * it is never killed for being in the background — that is the baseline and it
 * is already in place. Doze is a different mechanism: after the screen has
 * been off and the phone still for a while, the system suspends network access
 * and defers alarms for apps that are not exempt, in maintenance-window
 * batches. A foreground service does not opt out of that.
 *
 * For a short nap this costs nothing worth reporting. dmsg sessions are TCP
 * connections with their own keepalives; they survive a suspended radio the
 * way any idle connection does, and when the window opens the traffic resumes.
 * Over a long idle period they do drop, and the visor reconnects — the app's
 * own supervisor also restarts the child if it exits. What the user sees is a
 * gap: messages that arrive during a deep Doze land when the phone next wakes,
 * not when they were sent.
 *
 * The exemption removes that gap. It is a real cost in battery and the user is
 * the only one who can weigh it, so this is offered and explained, never
 * assumed — the app works without it, and the prompt does not come back once
 * it has been dismissed.
 */
object BatteryOptimization {

    /** Preference remembering that the user was asked and said no. */
    const val PREF_DISMISSED = "battery_prompt_dismissed"

    /**
     * True when the system has been told to leave this app alone. Also true on
     * the handful of devices with no power manager to ask, which is the right
     * answer for a check whose only use is deciding whether to offer a prompt.
     */
    fun isExempt(context: Context): Boolean {
        val pm = context.getSystemService(Context.POWER_SERVICE) as? PowerManager ?: return true
        return pm.isIgnoringBatteryOptimizations(context.packageName)
    }

    /**
     * The one-tap system dialog for this app. Needs
     * REQUEST_IGNORE_BATTERY_OPTIMIZATIONS, which Google Play restricts to
     * apps whose core function genuinely requires it — a VPN service and an
     * always-on peer-to-peer node are both on the permitted list, and this app
     * is both. It is offered from Settings behind an explanation rather than
     * thrown at the user on first launch, which is the part of the policy that
     * is about behaviour rather than eligibility.
     *
     * Suppressed lint: the warning exists to catch apps asking for this
     * without cause. The cause is in the class doc above.
     */
    @SuppressLint("BatteryLife")
    fun requestIntent(context: Context): Intent =
        Intent(
            Settings.ACTION_REQUEST_IGNORE_BATTERY_OPTIMIZATIONS,
            Uri.parse("package:${context.packageName}"),
        )

    /**
     * The full list of apps, for OEM builds where the direct request above is
     * missing or refuses to resolve. One more tap for the user — they have to
     * find Skywire in the list — but it exists everywhere the direct one does
     * not.
     */
    fun settingsIntent(): Intent = Intent(Settings.ACTION_IGNORE_BATTERY_OPTIMIZATION_SETTINGS)

    /**
     * Start whichever of the two the device actually has. Returns false when
     * neither resolves, so the caller can say so rather than appearing to do
     * nothing.
     */
    fun openRequest(context: Context): Boolean {
        for (intent in listOf(requestIntent(context), settingsIntent())) {
            if (runCatching { context.startActivity(intent) }.isSuccess) return true
        }
        return false
    }
}

package com.skycoin.skywire.core

import android.app.Activity
import android.app.AppOpsManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Process
import android.provider.Settings

/**
 * Whether an incoming call may take over the screen, and how to ask for it.
 *
 * An incoming call is supposed to arrive as the call SCREEN — the phone rings
 * and the answer/decline buttons are right there, whatever the device was
 * doing. An app may not launch an Activity from the background, so the
 * sanctioned way to do that is a **full-screen intent**: a notification whose
 * only job is to carry the Activity that replaces it.
 *
 * **Android 14 changed who may use one.** `USE_FULL_SCREEN_INTENT` used to be
 * granted at install; from API 34 it is a special app-op, granted
 * automatically only to apps whose core function is calling or alarms — a
 * default dialer, something on the Telecom framework. Everything else starts
 * denied, and declaring the permission in the manifest changes nothing.
 *
 * What the platform does with a denied one is silent: it accepts the
 * notification, throws the intent away, and posts what is left. Measured on
 * an API 37 emulator, mid-ring:
 *
 * ```
 * USE_FULL_SCREEN_INTENT: default; rejectTime=+58s934ms ago
 * fullscreenIntent=null
 * ```
 *
 * So the call screen never appeared for anyone on a recent Android, and the
 * heads-up banner that was designed as the floor became the whole of it. The
 * only fix is to ask — there is a settings screen for exactly this, and
 * nothing else grants it.
 *
 * Offered and explained, never assumed, like the Doze exemption beside it in
 * Settings: calls still arrive as a banner without it, so this buys the
 * difference between a notification to notice and a phone that rings.
 */
object FullScreenCalls {

    /** Preference remembering that the user was asked and said no. */
    const val PREF_DISMISSED = "fullscreen_calls_prompt_dismissed"

    /**
     * The op behind USE_FULL_SCREEN_INTENT, spelled out because the SDK does
     * not publish an `OPSTR_` constant for it. It is the name `appops` itself
     * uses (`adb shell cmd appops get <pkg> USE_FULL_SCREEN_INTENT`), and
     * [isGranted] degrades to "granted" if it is ever not recognised, so a
     * rename costs the prompt rather than the calls.
     */
    private const val OP_USE_FULL_SCREEN_INTENT = "android:use_full_screen_intent"

    /**
     * True when a ringing call may take the screen.
     *
     * Below API 34 the manifest permission is the whole story and is granted
     * at install, so there is nothing to ask for and nothing to check.
     *
     * At or above it, the app-op is read directly and only MODE_ALLOWED
     * counts. `NotificationManager.canUseFullScreenIntent()` is the obvious
     * call and is NOT used, because it does not agree with what the platform
     * then does: on an API 37 emulator with the op left at MODE_DEFAULT it
     * returned true while every notification posted in that state came back
     * from `dumpsys notification` with `fullscreenIntent=null`. It appears to
     * answer from the manifest permission, which this app holds; the post
     * path applies the stricter rule. A check that says yes while calls
     * arrive as banners is worse than no check — it is the reassuring
     * version of the bug.
     *
     * MODE_DEFAULT therefore reads as "not granted". On a device where the
     * default does resolve to allowed — one where this app counted as a
     * calling app — the cost is offering a switch that is already on, which
     * the card states plainly and which "Not now" ends.
     */
    fun isGranted(context: Context): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return true
        val ops = context.getSystemService(Context.APP_OPS_SERVICE) as? AppOpsManager ?: return true
        val mode = runCatching {
            ops.unsafeCheckOpNoThrow(OP_USE_FULL_SCREEN_INTENT, Process.myUid(), context.packageName)
        }.getOrNull() ?: return true
        return mode == AppOpsManager.MODE_ALLOWED
    }

    /**
     * The settings screen that grants it, for this app.
     *
     * The `package:` uri is what takes the user to this app's own switch
     * rather than the list of every app that has asked. The fallback is this
     * app's notification settings, which exists on every device and is one
     * screen away from the same switch.
     *
     * FLAG_ACTIVITY_NEW_TASK for the same reason it is on the battery intent:
     * the callers are view models holding the Application, and startActivity
     * from a non-Activity context throws without it.
     */
    fun openRequest(context: Context): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return false
        val intents = listOf(
            Intent(
                Settings.ACTION_MANAGE_APP_USE_FULL_SCREEN_INTENT,
                Uri.parse("package:${context.packageName}"),
            ),
            Intent(Settings.ACTION_APP_NOTIFICATION_SETTINGS)
                .putExtra(Settings.EXTRA_APP_PACKAGE, context.packageName),
        )
        for (intent in intents) {
            if (context !is Activity) intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            if (runCatching { context.startActivity(intent) }.isSuccess) return true
        }
        return false
    }
}

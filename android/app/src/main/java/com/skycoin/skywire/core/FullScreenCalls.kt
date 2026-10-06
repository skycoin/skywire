package com.skycoin.skywire.core

import android.app.Activity
import android.app.NotificationManager
import android.content.Context
import android.content.Intent
import android.net.Uri
import android.os.Build
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
 * granted at install; from API 34 it is a special app-op that the user can
 * switch off, and that an app that is not a dialer or an alarm may not get at
 * install at all. Sideloaded on an API 37 emulator, this app did get it.
 *
 * What the platform does with a denied one is silent: it accepts the
 * notification, throws the intent away, and posts what is left, so the call
 * arrives as a heads-up banner. The only fix is to ask — there is a settings
 * screen for exactly this, and nothing else grants it.
 *
 * Offered and explained, never assumed, like the Doze exemption beside it in
 * Settings: calls still arrive as a banner without it, so this buys the
 * difference between a notification to notice and a phone that rings.
 */
object FullScreenCalls {

    /** Preference remembering that the user was asked and said no. */
    const val PREF_DISMISSED = "fullscreen_calls_prompt_dismissed"

    /**
     * True when a ringing call may take the screen. Below API 34 the manifest
     * permission is granted at install, so there is nothing to check.
     *
     * At or above it this asks the platform, which applies the same rule when
     * the notification is posted. Reading the app-op alone is not enough: an
     * op left at MODE_DEFAULT falls back to the install-time permission, and
     * the system's own switch then shows as on. Verified on an API 37
     * emulator, where a call with the op at default lit the locked screen.
     */
    fun isGranted(context: Context): Boolean {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.UPSIDE_DOWN_CAKE) return true
        val nm = context.getSystemService(NotificationManager::class.java) ?: return true
        return runCatching { nm.canUseFullScreenIntent() }.getOrDefault(true)
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

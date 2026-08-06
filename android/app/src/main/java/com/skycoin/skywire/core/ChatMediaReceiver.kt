package com.skycoin.skywire.core

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/**
 * The notification's transport buttons. A receiver rather than a service:
 * every one of these is a single call into the page that is already running,
 * with nothing to keep alive afterwards.
 */
class ChatMediaReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        intent.action?.let { ChatMedia.onAction(context.applicationContext, it) }
    }
}

package com.skycoin.skywire

import android.app.Application
import android.content.res.Configuration
import com.skycoin.skywire.core.AppLocale
import com.skycoin.skywire.core.ChatPageLog
import com.skycoin.skywire.core.CrashLog

/**
 * Application singleton. The core-process plumbing (SkywireCoreService wiring,
 * first-run config gen, log capture) hangs off this when it lands.
 */
class SkywireApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        CrashLog.install(this)
        ChatPageLog.install(this)
        AppLocale.onConfigurationChanged(resources.configuration)
    }

    override fun onConfigurationChanged(newConfig: Configuration) {
        super.onConfigurationChanged(newConfig)
        AppLocale.onConfigurationChanged(newConfig)
    }
}

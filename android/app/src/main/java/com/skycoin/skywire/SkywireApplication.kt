package com.skycoin.skywire

import android.app.Application
import com.skycoin.skywire.core.CrashLog

/**
 * Application singleton. The core-process plumbing (SkywireCoreService wiring,
 * first-run config gen, log capture) hangs off this when it lands.
 */
class SkywireApplication : Application() {
    override fun onCreate() {
        super.onCreate()
        CrashLog.install(this)
    }
}

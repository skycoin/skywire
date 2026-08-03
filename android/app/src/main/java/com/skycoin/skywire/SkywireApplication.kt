package com.skycoin.skywire

import android.app.Application

/**
 * Application singleton. The core-process plumbing (SkywireCoreService wiring,
 * first-run config gen, log capture) hangs off this when it lands.
 */
class SkywireApplication : Application()

package com.skycoin.skywire.core

import android.content.Context
import java.io.File
import java.time.Instant

/**
 * Keeps the stack trace of a crash that is about to take the app down. The
 * platform's exit record only says that it crashed, not where.
 */
object CrashLog {

    private const val MAX_BYTES = 64 * 1024

    /** Install once per process, ahead of the platform's own handler. */
    fun install(context: Context) {
        val file = SkywirePaths(context).crashLogFile
        val previous = Thread.getDefaultUncaughtExceptionHandler()
        Thread.setDefaultUncaughtExceptionHandler { thread, error ->
            runCatching { append(file, thread, error) }
            previous?.uncaughtException(thread, error)
        }
    }

    private fun append(file: File, thread: Thread, error: Throwable) {
        file.parentFile?.mkdirs()
        if (file.length() > MAX_BYTES) {
            val bytes = file.readBytes()
            file.writeBytes(bytes.copyOfRange(bytes.size - MAX_BYTES / 2, bytes.size))
        }
        file.appendText("=== ${Instant.now()} crash on thread ${thread.name} ===\n${error.stackTraceToString()}\n")
    }
}

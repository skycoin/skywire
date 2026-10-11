package com.skycoin.skywire.core

import android.content.Context
import java.io.File
import java.time.Instant
import java.util.concurrent.Executors

/**
 * The chat page's console and what the WebView around it did, kept on disk
 * for the diagnostics export. The page's errors never reach the core's logs,
 * so a page that stopped responding used to leave nothing to read.
 */
object ChatPageLog {

    private const val MAX_BYTES = 256 * 1024L

    @Volatile
    private var file: File? = null

    /** Appends happen here, never on the main thread that reports them. */
    private val writer = Executors.newSingleThreadExecutor { task ->
        Thread(task, "chat-page-log").apply { isDaemon = true }
    }

    fun install(context: Context) {
        file = SkywirePaths(context).chatPageLogFile
    }

    fun add(line: String) {
        val target = file ?: return
        val stamped = "${Instant.now()} $line\n"
        writer.execute { runCatching { append(target, stamped) } }
    }

    private fun append(target: File, text: String) {
        target.parentFile?.mkdirs()
        if (target.length() > MAX_BYTES) {
            val previous = File(target.parentFile, target.name + ".1")
            previous.delete()
            target.renameTo(previous)
        }
        target.appendText(text)
    }
}

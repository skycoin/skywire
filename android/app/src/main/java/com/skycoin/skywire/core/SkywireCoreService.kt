package com.skycoin.skywire.core

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.app.Service
import android.content.Context
import android.content.Intent
import android.os.Build
import android.os.IBinder
import android.os.SystemClock
import androidx.core.app.NotificationCompat
import androidx.core.content.ContextCompat
import com.skycoin.skywire.MainActivity
import com.skycoin.skywire.R
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.launch
import kotlinx.coroutines.runInterruptible
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * Foreground service that owns the visor child process — the phone
 * equivalent of running `skywire visor -c skywire-config.json` under a
 * process supervisor:
 *  - generates the config on first run (via [ConfigManager]),
 *  - execs the extracted Go binary with cwd/env pinned to app-private dirs,
 *  - captures the child's combined stdout/stderr into a rotating file (the
 *    only log source that exists when the visor won't start),
 *  - restarts on crash with exponential backoff,
 *  - stops the child gracefully (SIGTERM unwinds the visor's close stack)
 *    on user disconnect.
 */
class SkywireCoreService : Service() {

    private val scope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
    private lateinit var paths: SkywirePaths
    private lateinit var configManager: ConfigManager
    private lateinit var prefs: AppPreferences
    private var runner: Job? = null

    @Volatile private var child: Process? = null

    @Volatile private var stopRequested = false

    override fun onCreate() {
        super.onCreate()
        paths = SkywirePaths(this)
        configManager = ConfigManager(paths)
        prefs = AppPreferences(this)
        createChannel()
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            ACTION_STOP -> stopCore()
            // ACTION_START, or null on a START_STICKY revival: the system
            // only revives us if the core was meant to be running — resume.
            else -> {
                startForeground(NOTIFICATION_ID, notification(getString(R.string.core_notification_starting)))
                startCore()
            }
        }
        return START_STICKY
    }

    override fun onBind(intent: Intent?): IBinder? = null

    override fun onDestroy() {
        // Last-resort cleanup (system kill): don't leave an orphan visor
        // holding :8000 — the next spawn would fail on the bind.
        child?.destroy()
        scope.cancel()
        super.onDestroy()
    }

    // --- lifecycle ---

    private fun startCore() {
        if (runner?.isActive == true) return
        stopRequested = false
        runner = scope.launch {
            CoreServiceState.mutableState.value = CoreState.Starting(attempt = 1)
            val log = ProcessLog(paths.processLogFile)
            try {
                val primary = TransportPreference.sanitize(
                    prefs.string(TransportPreference.PREF_KEY).first(),
                )
                val config = configManager.ensureConfig(primary).getOrElse { err ->
                    log.line("=== config generation failed ===")
                    log.line(err.message ?: "unknown error")
                    CoreServiceState.mutableState.value =
                        CoreState.Failed(err.message ?: "config generation failed")
                    return@launch
                }

                var attempt = 0
                var backoffMs = INITIAL_BACKOFF_MS
                while (!stopRequested) {
                    attempt++
                    val startedAt = SystemClock.elapsedRealtime()
                    log.line("=== starting visor (attempt $attempt) ===")
                    val process = try {
                        spawnVisor(config)
                    } catch (e: Exception) {
                        log.line("spawn failed: $e")
                        CoreServiceState.mutableState.value =
                            CoreState.Failed("could not start the visor: ${e.message}")
                        return@launch
                    }
                    child = process
                    CoreServiceState.mutableState.value =
                        CoreState.Running(System.currentTimeMillis(), attempt)
                    notify(getString(R.string.core_notification_running))

                    val pump = launch(Dispatchers.IO) {
                        process.inputStream.bufferedReader().forEachLine { log.line(it) }
                    }
                    val exit = runInterruptible(Dispatchers.IO) { process.waitFor() }
                    pump.join()
                    child = null

                    val ranMs = SystemClock.elapsedRealtime() - startedAt
                    log.line("=== visor exited with code $exit after ${ranMs / 1000}s ===")
                    if (stopRequested) break

                    backoffMs = if (ranMs > STABLE_RUN_MS) INITIAL_BACKOFF_MS
                    else (backoffMs * 2).coerceAtMost(MAX_BACKOFF_MS)
                    CoreServiceState.mutableState.value =
                        CoreState.Restarting(attempt + 1, backoffMs)
                    notify(getString(R.string.core_notification_restarting, attempt + 1))
                    // Sliced so a Disconnect during the backoff takes effect in
                    // ~250 ms instead of waiting out the whole delay.
                    val deadline = SystemClock.elapsedRealtime() + backoffMs
                    while (!stopRequested && SystemClock.elapsedRealtime() < deadline) {
                        delay(BACKOFF_SLICE_MS)
                    }
                }
            } catch (e: kotlinx.coroutines.CancellationException) {
                throw e
            } catch (e: Exception) {
                // Anything unexpected must land in Failed, not crash the app.
                log.line("=== core service error: $e ===")
                CoreServiceState.mutableState.value =
                    CoreState.Failed(e.message ?: "core service error")
            } finally {
                // Terminal Failed states survive; everything else is Stopped.
                if (CoreServiceState.mutableState.value !is CoreState.Failed) {
                    CoreServiceState.mutableState.value = CoreState.Stopped
                }
                stopForeground(STOP_FOREGROUND_REMOVE)
                stopSelf()
            }
        }
    }

    private fun stopCore() {
        if (runner?.isActive != true) {
            stopForeground(STOP_FOREGROUND_REMOVE)
            stopSelf()
            return
        }
        stopRequested = true
        CoreServiceState.mutableState.value = CoreState.Stopping
        scope.launch(Dispatchers.IO) {
            val p = child ?: return@launch
            // Android's Process.destroy() sends SIGKILL (unlike desktop
            // JVMs), which would skip the visor's shutdown stack. Deliver a
            // real SIGTERM to the child's pid so it unwinds cleanly (4 s per
            // module, exit 0); kill only if it hangs or the pid lookup fails.
            val pid = findChildVisorPid()
            if (pid != null) {
                runCatching { android.system.Os.kill(pid, android.system.OsConstants.SIGTERM) }
                    .onFailure { p.destroy() }
            } else {
                p.destroy()
            }
            if (!runInterruptible { p.waitFor(GRACEFUL_STOP_S, TimeUnit.SECONDS) }) {
                p.destroyForcibly()
            }
        }
    }

    /**
     * Pid of our direct child running the visor payload, from /proc (own-uid
     * entries are readable): `stat` is `pid (comm) state ppid …`.
     */
    private fun findChildVisorPid(): Int? {
        val myPid = android.os.Process.myPid()
        return File("/proc").listFiles()
            ?.mapNotNull { it.name.toIntOrNull() }
            ?.firstOrNull { pid ->
                runCatching {
                    File("/proc/$pid/cmdline").readText().contains(paths.visorBinary.name) &&
                        File("/proc/$pid/stat").readText()
                            .substringAfterLast(") ")
                            .split(' ')[1]
                            .toInt() == myPid
                }.getOrDefault(false)
            }
    }

    private fun spawnVisor(config: File): Process =
        ProcessBuilder(paths.visorBinary.absolutePath, "visor", "-c", config.absolutePath)
            .directory(paths.dataDir)
            .redirectErrorStream(true)
            .apply { environment().putAll(coreEnv(paths)) }
            .start()

    // --- notification ---

    private fun createChannel() {
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        val channel = NotificationChannel(
            CHANNEL_ID,
            getString(R.string.core_channel_name),
            NotificationManager.IMPORTANCE_LOW,
        ).apply { description = getString(R.string.core_channel_description) }
        manager.createNotificationChannel(channel)
    }

    private fun notification(text: String): Notification {
        val contentIntent = PendingIntent.getActivity(
            this,
            0,
            Intent(this, MainActivity::class.java),
            PendingIntent.FLAG_IMMUTABLE,
        )
        return NotificationCompat.Builder(this, CHANNEL_ID)
            .setSmallIcon(R.drawable.skywire_logo)
            .setContentTitle(getString(R.string.core_notification_title))
            .setContentText(text)
            .setContentIntent(contentIntent)
            .setOngoing(true)
            .setOnlyAlertOnce(true)
            .build()
    }

    private fun notify(text: String) {
        val manager = getSystemService(Context.NOTIFICATION_SERVICE) as NotificationManager
        manager.notify(NOTIFICATION_ID, notification(text))
    }

    companion object {
        private const val CHANNEL_ID = "core"
        private const val NOTIFICATION_ID = 1
        private const val ACTION_START = "com.skycoin.skywire.core.START"
        private const val ACTION_STOP = "com.skycoin.skywire.core.STOP"
        private const val INITIAL_BACKOFF_MS = 1_000L
        private const val MAX_BACKOFF_MS = 30_000L
        private const val BACKOFF_SLICE_MS = 250L
        private const val STABLE_RUN_MS = 60_000L
        private const val GRACEFUL_STOP_S = 15L

        fun start(context: Context) {
            ContextCompat.startForegroundService(
                context,
                Intent(context, SkywireCoreService::class.java).setAction(ACTION_START),
            )
        }

        fun stop(context: Context) {
            context.startService(
                Intent(context, SkywireCoreService::class.java).setAction(ACTION_STOP),
            )
        }
    }
}

/**
 * Append-only rotating capture of the child's combined output. One live file
 * plus one predecessor; the viewer's Process source tails the live file.
 */
internal class ProcessLog(private val file: File) {
    @Synchronized
    fun line(text: String) {
        runCatching {
            if (file.length() > MAX_BYTES) {
                val previous = File(file.parentFile, file.name + ".1")
                previous.delete()
                file.renameTo(previous)
            }
            file.appendText(text + "\n")
        }
    }

    private companion object {
        const val MAX_BYTES = 1_500_000L
    }
}

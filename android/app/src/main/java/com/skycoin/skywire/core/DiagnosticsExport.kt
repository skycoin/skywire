package com.skycoin.skywire.core

import android.app.ActivityManager
import android.app.ApplicationExitInfo
import android.content.Context
import android.os.Build
import android.webkit.WebView
import com.skycoin.skywire.api.VisorApi
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.io.OutputStream
import java.time.Instant
import java.util.zip.ZipEntry
import java.util.zip.ZipOutputStream

/**
 * "Export all" from Logs & diagnostics: every log source this phone has, in
 * one zip, plus the two things that make them readable — what the device is
 * and what the config says.
 *
 * The per-screen `Logs` buttons are for reading a feed while it happens; this
 * is for handing the whole picture to someone else. So it collects rather than
 * tails: each API source is asked for its entire buffer once, and the captured
 * process output is copied whole.
 *
 * **The config is redacted.** A diagnostics bundle is a thing people attach to
 * an issue, and the visor's config carries its secret key — the identity
 * itself. `sk` is stripped ([ConfigManager.redactedConfigJson]); the full file
 * has its own deliberate export in Settings, behind a biometric check and a
 * warning that says what is in it.
 *
 * Nothing here fails the export. A source that cannot be collected — the core
 * is down, so the API answers nothing — becomes a line in `collection-notes.txt`
 * instead of an error, because the bundle is most wanted exactly when things
 * are broken.
 */
object DiagnosticsExport {

    /**
     * Write the bundle into [out], which is closed on the way out. [apps] are
     * the app names whose per-app feeds to include.
     */
    suspend fun writeTo(
        context: Context,
        out: OutputStream,
        apps: List<String>,
    ): Unit = withContext(Dispatchers.IO) {
        val app = context.applicationContext
        val paths = SkywirePaths(app)
        val config = ConfigManager(paths, SecretStore(app), app)
        val api = VisorApi.get(app)
        val notes = mutableListOf<String>()

        // Each is asked for once, whole. `since = 0` / `since = null` is the
        // entire buffer in one page — the same opening page the log viewer's
        // first poll takes.
        val sources = buildList<Pair<String, suspend () -> String>> {
            add("skywire-config.redacted.json" to { config.redactedConfigJson() })
            add("exit-reasons.txt" to { exitReasons(app) })
            add("core-runtime.log" to {
                api.runtimeLogs(since = 0).entries.orEmpty().joinToString("\n")
            })
            apps.forEach { name ->
                add("apps/$name.log" to {
                    api.appLogs(name, since = null).logs.joinToString("\n")
                })
            }
        }

        // The running visor's own report of what it is. Absent when the core
        // is down — which is a state this bundle is often collected in, so it
        // is a "?" in device.txt rather than a reason to fail.
        val coreVersion = runCatching {
            api.summary().let { summary ->
                listOfNotNull(
                    summary.overview.buildInfo?.version?.takeIf { it.isNotEmpty() },
                    summary.buildTag.takeIf { it.isNotEmpty() },
                ).joinToString(" · ")
            }
        }.getOrNull()?.takeIf { it.isNotEmpty() }

        ZipOutputStream(out.buffered()).use { zip ->
            zip.text("README.txt", readme())
            zip.text("device.txt", device(app, coreVersion))

            for ((name, produce) in sources) {
                try {
                    zip.text(name, produce())
                } catch (e: kotlinx.coroutines.CancellationException) {
                    throw e
                } catch (e: Exception) {
                    notes += "$name — not collected: ${e.message ?: e::class.java.simpleName}"
                }
            }

            zip.file(notes, "app-crash.log", paths.crashLogFile)
            zip.file(notes, "skychat-page.log", paths.chatPageLogFile)
            zip.file(
                notes,
                "skychat-page.log.1",
                File(paths.chatPageLogFile.parentFile, paths.chatPageLogFile.name + ".1"),
            )
            // The one source that exists when the visor will not start.
            zip.file(notes, "process-output.log", paths.processLogFile)
            zip.file(
                notes,
                "process-output.log.1",
                File(paths.processLogFile.parentFile, paths.processLogFile.name + ".1"),
            )

            if (notes.isNotEmpty()) {
                zip.text("collection-notes.txt", notes.joinToString("\n"))
            }
        }
    }

    private fun readme(): String = """
        Skywire for Android — diagnostics bundle
        Collected ${Instant.now()}

        core-runtime.log            the visor's runtime ring buffer (logrus JSON, one entry per line)
        apps/<name>.log             each client app's own log, as the visor keeps it.
                                    The names are the processes, not the products:
                                    skychat = SkyChat, skysocks-client = SkySOCKS,
                                    vpn-client = SkyVPN, skydex-client = SkyDEX
        process-output.log[.1]      the visor child process's combined stdout/stderr, captured by
                                    the app — the only source that exists when the visor won't start
        skywire-config.redacted.json the visor config WITHOUT its secret key
        exit-reasons.txt            why the app's last processes ended, as Android recorded it:
                                    LOW_MEMORY or OTHER means the phone stopped the app,
                                    CRASH means the app failed (stack in app-crash.log)
        app-crash.log               stack traces of the app's own crashes, if it has had any
        skychat-page.log[.1]        the SkyChat page's console and its WebView's events: pages
                                    loaded and closed, camera and microphone requests and their
                                    answers, and any time the page stopped answering
        device.txt                  phone, Android and version details
        collection-notes.txt        present only if something could not be collected, and why

        The secret key is not in this bundle. Settings > Export config writes the
        complete config, including the key, as a separate deliberate action.
    """.trimIndent()

    private fun device(context: Context, coreVersion: String?): String {
        val packageInfo = runCatching {
            context.packageManager.getPackageInfo(context.packageName, 0)
        }.getOrNull()
        return buildString {
            appendLine("app.version = ${packageInfo?.versionName ?: "?"}")
            appendLine("core.version = ${coreVersion ?: "?"}")
            appendLine("android.release = ${Build.VERSION.RELEASE}")
            appendLine("android.sdk = ${Build.VERSION.SDK_INT}")
            appendLine("device = ${Build.MANUFACTURER} ${Build.MODEL}")
            appendLine("abis = ${Build.SUPPORTED_ABIS.joinToString(",")}")
            // SkyChat runs in it, and a phone without Google Play ships its own.
            val webView = runCatching { WebView.getCurrentWebViewPackage() }.getOrNull()
            appendLine("webview = ${webView?.let { "${it.packageName} ${it.versionName}" } ?: "?"}")
        }
    }

    /**
     * Android's record of how this app's recent processes ended, newest first
     * (API 30+). It is what tells a crash from the phone stopping the app.
     */
    private fun exitReasons(context: Context): String {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.R) {
            return "Not recorded before Android 11 (this is Android ${Build.VERSION.RELEASE})."
        }
        val am = context.getSystemService(ActivityManager::class.java)
        val exits = am.getHistoricalProcessExitReasons(context.packageName, 0, MAX_EXITS)
        if (exits.isEmpty()) return "No exits recorded."
        return exits.joinToString("\n") { e ->
            "${Instant.ofEpochMilli(e.timestamp)} ${e.processName} pid=${e.pid} " +
                "reason=${exitReasonName(e.reason)} status=${e.status} importance=${e.importance} " +
                "pss=${e.pss}kB rss=${e.rss}kB ${e.description.orEmpty()}"
        }
    }

    private fun exitReasonName(reason: Int): String = when (reason) {
        ApplicationExitInfo.REASON_EXIT_SELF -> "EXIT_SELF"
        ApplicationExitInfo.REASON_SIGNALED -> "SIGNALED"
        ApplicationExitInfo.REASON_LOW_MEMORY -> "LOW_MEMORY"
        ApplicationExitInfo.REASON_CRASH -> "CRASH"
        ApplicationExitInfo.REASON_CRASH_NATIVE -> "CRASH_NATIVE"
        ApplicationExitInfo.REASON_ANR -> "ANR"
        ApplicationExitInfo.REASON_INITIALIZATION_FAILURE -> "INITIALIZATION_FAILURE"
        ApplicationExitInfo.REASON_PERMISSION_CHANGE -> "PERMISSION_CHANGE"
        ApplicationExitInfo.REASON_EXCESSIVE_RESOURCE_USAGE -> "EXCESSIVE_RESOURCE_USAGE"
        ApplicationExitInfo.REASON_USER_REQUESTED -> "USER_REQUESTED"
        ApplicationExitInfo.REASON_USER_STOPPED -> "USER_STOPPED"
        ApplicationExitInfo.REASON_DEPENDENCY_DIED -> "DEPENDENCY_DIED"
        ApplicationExitInfo.REASON_OTHER -> "OTHER"
        ApplicationExitInfo.REASON_FREEZER -> "FREEZER"
        ApplicationExitInfo.REASON_PACKAGE_STATE_CHANGE -> "PACKAGE_STATE_CHANGE"
        ApplicationExitInfo.REASON_PACKAGE_UPDATED -> "PACKAGE_UPDATED"
        else -> "UNKNOWN($reason)"
    }

    private const val MAX_EXITS = 16

    // --- zip plumbing ---

    private fun ZipOutputStream.text(name: String, content: String) {
        putNextEntry(ZipEntry(name))
        write(content.toByteArray(Charsets.UTF_8))
        closeEntry()
    }

    /** A missing rotated log is normal and silent; an unreadable one is a note. */
    private fun ZipOutputStream.file(notes: MutableList<String>, name: String, source: File) {
        if (!source.exists()) return
        runCatching {
            putNextEntry(ZipEntry(name))
            source.inputStream().use { it.copyTo(this) }
            closeEntry()
        }.onFailure { notes += "$name — not collected: ${it.message}" }
    }
}

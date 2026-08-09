package com.skycoin.skywire.core

import android.content.Context
import android.os.Build
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
        val config = ConfigManager(paths, SecretStore(app))
        val api = VisorApi.get(app)
        val notes = mutableListOf<String>()

        // Each is asked for once, whole. `since = 0` / `since = null` is the
        // entire buffer in one page — the same opening page the log viewer's
        // first poll takes.
        val sources = buildList<Pair<String, suspend () -> String>> {
            add("skywire-config.redacted.json" to { config.redactedConfigJson() })
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
        }
    }

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

package com.skycoin.skywire.core

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonArray
import kotlinx.serialization.json.JsonElement
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.JsonPrimitive
import kotlinx.serialization.json.buildJsonObject
import kotlinx.serialization.json.jsonObject
import java.io.File
import java.util.concurrent.TimeUnit

/**
 * Generates and maintains the visor config, on-device, with the same binary
 * that runs the visor. Nothing is hand-written: `config gen` produces the
 * file, then [applyPhoneProfile] enforces the phone constraints the
 * generator has no flags for.
 */
class ConfigManager(private val paths: SkywirePaths, private val secrets: SecretStore) {

    data class CommandResult(val exitCode: Int, val output: String, val timedOut: Boolean) {
        val ok get() = exitCode == 0 && !timedOut
    }

    private val json = Json { prettyPrint = true }

    /**
     * Make sure a phone-profile config exists. Generation runs only when the
     * file is missing; the profile edits are re-applied every time (cheap,
     * idempotent) so the security-relevant pins survive any config rewrite
     * the visor itself performs at runtime.
     */
    suspend fun ensureConfig(transportPrimary: String): Result<File> = withContext(Dispatchers.IO) {
        paths.ensureDirs()
        if (!paths.visorBinary.canExecute()) {
            return@withContext Result.failure(
                IllegalStateException("core binary missing or not executable: ${paths.visorBinary}"),
            )
        }
        if (!paths.configFile.exists()) {
            val gen = runGen()
            if (!gen.ok) {
                return@withContext Result.failure(
                    IllegalStateException(
                        "config gen failed (exit ${gen.exitCode}${if (gen.timedOut) ", timed out" else ""}):\n" +
                            gen.output.takeLast(4000),
                    ),
                )
            }
        }
        try {
            applyPhoneProfile(transportPrimary, ensureSkychatPassword())
            Result.success(paths.configFile)
        } catch (e: Exception) {
            // A file that exists but does not parse would otherwise crash-loop
            // the visor with an opaque fatal — fail here with a usable message.
            Result.failure(
                IllegalStateException(
                    "config file is unreadable (${e.message}) — clearing app data regenerates it",
                    e,
                ),
            )
        }
    }

    /**
     * `config gen` argv. `-r` retains the secret key across regens (and is
     * safe on first run); `--hvaddr` pins the API to loopback — the compiled
     * default `":8000"` binds every interface; the autostart-off and
     * disableapps flags leave exactly the four in-proc apps, all
     * user-initiated; `--nofetch` keeps first-run generation offline (the
     * visor refreshes service endpoints in-memory at runtime anyway).
     */
    private fun genArgs(): List<String> = listOf(
        paths.visorBinary.absolutePath, "config", "gen",
        "-r",
        "-o", paths.configFile.absolutePath,
        "-w",
        "-i", "--auth",
        "--hvaddr", "127.0.0.1:8000",
        "--autoconn",
        "--servechat=false", "--serveproxy=false", "--servevpn=false",
        "--disableapps", "skysocks,vpn-server,vpn-router,skydex-market,skycoin-web",
        "--binpath", paths.binDir.absolutePath,
        "--nofetch",
    )

    private suspend fun runGen(): CommandResult = runCommand(genArgs(), timeoutSeconds = 90)

    /**
     * The edits `config gen` cannot express:
     *  - `cli_addr: ""` — no RPC listener (an empty flag value falls back to
     *    localhost:3435, so this must be a post-edit);
     *  - drop `pty` — dmsgpty would try a unix socket in an unwritable
     *    system temp dir, and the phone has no use for it;
     *  - drop `hypervisor.lan_dmsg_server` — force-enabled by `-i`, opens a
     *    LAN-reachable listener;
     *  - absolute `local_path` (+ the transport log location derived from
     *    it) and `hypervisor.db_path`, so nothing depends on the cwd the
     *    visor happens to get;
     *  - `launcher.bin_path` pinned to an app-private writable dir, and the
     *    per-app flags the phone owns (see [pinAppArgs]);
     *  - drop `skywire-tcp` — skips the `:7777` STCP listener;
     *  - `dmsgscp.disabled` — on by default when absent, writes scp-root;
     *  - `tp_viz.enable=false` — cosmetic (field is never read) but keeps
     *    the config honest about what the phone uses;
     *  - `routing.transport_preference` — the primary transport the app
     *    owns (see [TransportPreference]). Written on every launch because
     *    the app, not the config file, is the source of truth: the visor
     *    persists its own copy when the setting is changed live, and this
     *    keeps the two from drifting apart.
     */
    private fun applyPhoneProfile(transportPrimary: String, skychatPasswordFile: File) {
        val root = json.parseToJsonElement(paths.configFile.readText()).jsonObject
        val edited = buildJsonObject {
            for ((key, value) in root) {
                when (key) {
                    "pty", "skywire-tcp" -> Unit // dropped
                    "cli_addr" -> put(key, JsonPrimitive(""))
                    "local_path" -> put(key, JsonPrimitive(paths.localDir.absolutePath))
                    "transport" -> put(
                        key,
                        value.jsonObject.edit {
                            putObject("log_store") {
                                put(
                                    "location",
                                    JsonPrimitive(File(paths.localDir, "transport_logs").absolutePath),
                                )
                            }
                        },
                    )
                    "hypervisor" -> put(
                        key,
                        value.jsonObject.edit {
                            remove("lan_dmsg_server")
                            put("db_path", JsonPrimitive(File(paths.dataDir, "users.db").absolutePath))
                            putObject("tp_viz") { put("enable", JsonPrimitive(false)) }
                        },
                    )
                    // Re-pinned on every launch, not just at generation: the
                    // launcher creates this directory at startup, so a path
                    // that stopped existing (as the native-library dir does
                    // on every app update) aborts the visor.
                    "launcher" -> put(
                        key,
                        value.jsonObject.edit {
                            put("bin_path", JsonPrimitive(paths.binDir.absolutePath))
                            this["apps"]?.let { apps ->
                                this["apps"] = pinAppArgs(apps, skychatPasswordFile)
                            }
                        },
                    )
                    "routing" -> put(
                        key,
                        value.jsonObject.edit {
                            this["transport_preference"] = JsonArray(
                                TransportPreference.order(transportPrimary).map(::JsonPrimitive),
                            )
                        },
                    )
                    else -> put(key, value)
                }
            }
            putObject("dmsgscp") { put("disabled", JsonPrimitive(true)) }
        }
        paths.configFile.writeText(json.encodeToString(JsonObject.serializer(), edited))
    }

    /**
     * The app flags the phone must own, re-applied on every launch.
     * Everything else in each argv — the server key the SkySOCKS screen
     * writes, above all — passes through untouched, so this never undoes a
     * user's choice.
     */
    private fun pinAppArgs(apps: JsonElement, skychatPasswordFile: File): JsonElement {
        val list = apps as? JsonArray ?: return apps
        return JsonArray(
            list.map { entry ->
                val app = entry as? JsonObject ?: return@map entry
                // On disk the argv is one space-joined string, not an array.
                val args = (app["args"] as? JsonPrimitive)?.content.orEmpty().split(" ")
                    .filter { it.isNotEmpty() }
                val pinned = when ((app["name"] as? JsonPrimitive)?.content) {
                    SOCKS_APP -> phoneSocksArgs(args)
                    SkychatProfile.APP -> SkychatProfile.phoneArgs(args, skychatPasswordFile)
                    else -> return@map entry
                }
                app.edit { this["args"] = JsonPrimitive(pinned.joinToString(" ")) }
            },
        )
    }

    /**
     * The skychat password file, kept in step with the stored secret. Written
     * here rather than through the visor's `PUT …/skychat/password` route
     * because it has to be in place *before* the app is first started —
     * setting it afterwards leaves a window in which the chat surface is open
     * to every app on the phone (see [SkychatProfile]).
     */
    private suspend fun ensureSkychatPassword(): File {
        val file = SkychatProfile.passwordFile(paths)
        val password = secrets.skychatPassword()
        val current = runCatching { file.readText() }.getOrNull()
        if (current == null || !SkychatProfile.matches(current, password)) {
            file.writeText(SkychatProfile.passwordRecord(password))
        }
        return file
    }

    /**
     * - `--addr` host forced to loopback: the generated `:1080` listens on
     *   every interface, i.e. a SOCKS5 proxy any device on the same Wi-Fi
     *   could use. Only the host is rewritten — the port is the one knob
     *   the SkySOCKS screen exposes.
     * - `--reconnect` always on: a phone loses mesh routes routinely (a
     *   cell handover is enough), and without it the app *exits* the
     *   moment its route group dies, leaving a dead proxy until the user
     *   notices. With it, the client re-dials in place.
     */
    private fun phoneSocksArgs(args: List<String>): List<String> {
        val pinned = args.toMutableList()
        val flag = pinned.indexOfFirst { it == "--addr" || it == "-addr" }
        when {
            flag < 0 || flag + 1 >= pinned.size ->
                pinned += listOf("--addr", "$LOOPBACK:$DEFAULT_SOCKS_PORT")
            // A malformed value only gets worse from rewriting — leave it
            // and let the visor report it.
            else -> pinned[flag + 1].substringAfterLast(':').toIntOrNull()
                ?.let { port -> pinned[flag + 1] = "$LOOPBACK:$port" }
        }
        if (pinned.none { it == "--reconnect" || it.startsWith("--reconnect=") }) {
            pinned += "--reconnect"
        }
        return pinned
    }

    /**
     * Auth bootstrap dead-end recovery: the visor's account DB survives an
     * app reinstall of keystore-lost devices; deleting it lets create-account
     * run again with the current password. Only call with the visor stopped.
     */
    fun deleteUsersDb() {
        File(paths.dataDir, "users.db").delete()
    }

    private suspend fun runCommand(argv: List<String>, timeoutSeconds: Long): CommandResult =
        withContext(Dispatchers.IO) {
            val process = ProcessBuilder(argv)
                .directory(paths.dataDir)
                .redirectErrorStream(true)
                .apply { environment().putAll(coreEnv(paths)) }
                .start()
            supervisorScope {
                val output = StringBuilder()
                val reader = launch {
                    process.inputStream.bufferedReader().forEachLine { line ->
                        if (output.length < MAX_CAPTURE) output.appendLine(line)
                    }
                }
                val finished = try {
                    runInterruptible { process.waitFor(timeoutSeconds, TimeUnit.SECONDS) }
                } catch (e: kotlinx.coroutines.CancellationException) {
                    // Don't leave an orphan config-gen child behind when the
                    // service scope is torn down mid-run.
                    process.destroyForcibly()
                    throw e
                }
                if (!finished) process.destroyForcibly().waitFor()
                reader.join()
                CommandResult(
                    exitCode = if (finished) process.exitValue() else -1,
                    output = output.toString(),
                    timedOut = !finished,
                )
            }
        }

    private companion object {
        const val MAX_CAPTURE = 256 * 1024
        const val SOCKS_APP = "skysocks-client"
        const val LOOPBACK = "127.0.0.1"
        const val DEFAULT_SOCKS_PORT = 1080
    }
}

// --- small JsonObject editing helpers ---

private inline fun JsonObject.edit(block: MutableMap<String, kotlinx.serialization.json.JsonElement>.() -> Unit): JsonObject {
    val map = toMutableMap()
    map.block()
    return JsonObject(map)
}

private inline fun MutableMap<String, kotlinx.serialization.json.JsonElement>.putObject(
    key: String,
    block: MutableMap<String, kotlinx.serialization.json.JsonElement>.() -> Unit,
) {
    val nested = (this[key] as? JsonObject)?.toMutableMap() ?: mutableMapOf()
    nested.block()
    this[key] = JsonObject(nested)
}

private inline fun kotlinx.serialization.json.JsonObjectBuilder.putObject(
    key: String,
    block: MutableMap<String, kotlinx.serialization.json.JsonElement>.() -> Unit,
) {
    val nested = mutableMapOf<String, kotlinx.serialization.json.JsonElement>()
    nested.block()
    put(key, JsonObject(nested))
}

private suspend fun <T> runInterruptible(block: () -> T): T =
    kotlinx.coroutines.runInterruptible(Dispatchers.IO, block)

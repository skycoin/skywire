package com.skycoin.skywire.core

import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.supervisorScope
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
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
class ConfigManager(private val paths: SkywirePaths) {

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
    suspend fun ensureConfig(): Result<File> = withContext(Dispatchers.IO) {
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
            applyPhoneProfile()
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
     *  - `launcher.bin_path` pinned to an app-private writable dir;
     *  - drop `skywire-tcp` — skips the `:7777` STCP listener;
     *  - `dmsgscp.disabled` — on by default when absent, writes scp-root;
     *  - `tp_viz.enable=false` — cosmetic (field is never read) but keeps
     *    the config honest about what the phone uses.
     */
    private fun applyPhoneProfile() {
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

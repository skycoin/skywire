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

    /**
     * Opens the config when it is sealed at rest. Held here rather than passed
     * in because *every* path that touches the config has to go through it:
     * [ensureConfig] treats a missing file as first-run and would generate a
     * fresh identity over a sealed one, which is the single worst thing this
     * class could do.
     */
    private val vault = ConfigVault(paths)

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
    suspend fun ensureConfig(
        transportPrimary: String,
        fleetEnabled: Boolean,
        publicAutoconnect: Boolean,
        logLevel: String,
    ): Result<File> = withContext(Dispatchers.IO) {
        paths.ensureDirs()
        // Before the existence check below, which is what decides whether to
        // generate a new identity.
        vault.unseal().getOrElse { return@withContext Result.failure(it) }
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
            applyPhoneProfile(
                transportPrimary,
                fleetEnabled,
                publicAutoconnect,
                logLevel,
                skychatPasswordFile = ensureGatePassword(
                    SkychatProfile.passwordFile(paths),
                    secrets.skychatPassword(),
                ),
                skydexPasswordFile = ensureGatePassword(
                    SkydexProfile.passwordFile(paths),
                    secrets.skydexPassword(),
                ),
            )
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
     *  - `hypervisor.dmsg_ingest` — the Fleet opt-in (see [Fleet]), written
     *    from the app's preference on every launch for the same reason the
     *    transport order is: the phone, not the file, decides;
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
     *  - `log_level` — the same arrangement, for the same reason (see
     *    [CoreLogLevel]).
     */
    private fun applyPhoneProfile(
        transportPrimary: String,
        fleetEnabled: Boolean,
        publicAutoconnect: Boolean,
        logLevel: String,
        skychatPasswordFile: File,
        skydexPasswordFile: File,
    ) {
        val root = json.parseToJsonElement(paths.configFile.readText()).jsonObject
        val edited = buildJsonObject {
            for ((key, value) in root) {
                when (key) {
                    "pty", "skywire-tcp" -> Unit // dropped
                    "cli_addr" -> put(key, JsonPrimitive(""))
                    "log_level" -> put(key, JsonPrimitive(CoreLogLevel.sanitize(logLevel)))
                    "local_path" -> put(key, JsonPrimitive(paths.localDir.absolutePath))
                    "transport" -> put(
                        key,
                        value.jsonObject.edit {
                            // Re-pinned every launch like the transport order:
                            // the phone owns this choice, and `config gen`
                            // wrote its own answer (--autoconn) at generation.
                            put(
                                PublicAutoconnect.CONFIG_KEY,
                                JsonPrimitive(publicAutoconnect),
                            )
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
                            put(Fleet.CONFIG_KEY, JsonPrimitive(fleetEnabled))
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
                                this["apps"] =
                                    pinAppArgs(apps, skychatPasswordFile, skydexPasswordFile)
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
    private fun pinAppArgs(
        apps: JsonElement,
        skychatPasswordFile: File,
        skydexPasswordFile: File,
    ): JsonElement {
        val list = apps as? JsonArray ?: return apps
        return JsonArray(
            list.map { entry ->
                val app = entry as? JsonObject ?: return@map entry
                // On disk the argv is one space-joined string, not an array.
                val args = (app["args"] as? JsonPrimitive)?.content.orEmpty().split(" ")
                    .filter { it.isNotEmpty() }
                val name = (app["name"] as? JsonPrimitive)?.content
                val pinned = when (name) {
                    SOCKS_APP -> phoneSocksArgs(args)
                    SkydexProfile.APP -> SkydexProfile.phoneArgs(args, skydexPasswordFile)
                    SkychatProfile.APP -> SkychatProfile.phoneArgs(
                        args,
                        skychatPasswordFile,
                        SkychatProfile.historyFile(paths),
                    )
                    else -> return@map entry
                }
                app.edit {
                    this["args"] = JsonPrimitive(pinned.joinToString(" "))
                    // skychat is the ONE app that must run whether or not the
                    // user is looking at it. Everything else here is started by
                    // opening its screen, but a chat app that only runs while
                    // its tab is open cannot receive anything: no message
                    // notification, no ringing call, no missed call recorded —
                    // the phone would simply be offline for chat until tapped.
                    if (name == SkychatProfile.APP) {
                        this["auto_start"] = JsonPrimitive(true)
                    }
                }
            },
        )
    }

    /**
     * A gated app's password file, kept in step with the stored secret.
     * Written here rather than through any API route because it has to be in
     * place *before* the app is first started — setting it afterwards leaves a
     * window in which that app's surface is open to every app on the phone
     * (see [SkychatProfile], [SkydexProfile]).
     *
     * Rewritten only when it does not already stand for [password], so a
     * normal launch leaves it alone and a rotated secret (a wiped keystore)
     * does not leave the app 401-ing against its own gate.
     */
    private fun ensureGatePassword(file: File, password: String): File {
        val current = runCatching { file.readText() }.getOrNull()
        if (current == null || !PasswordFile.matches(current, password)) {
            file.writeText(PasswordFile.record(password))
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
        pinned.pinValue(ADDR) { current -> loopbackAddr(current, DEFAULT_SOCKS_PORT) }
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

    // --- identity ---

    /**
     * This visor's public key, read from the config on disk.
     *
     * The API answers the same question, but only while the core runs — and
     * the identity screen has to be able to show you who you are before you
     * connect, and right after an operation that took the core down.
     */
    fun publicKey(): String? = runCatching {
        (readConfig()["pk"] as? JsonPrimitive)?.content?.takeIf { it.isNotEmpty() }
    }.getOrNull()

    /**
     * Derive the public key of [secretKey] — and, in doing so, validate it.
     *
     * Runs the CLI's own `config pk`, which is the point: key handling stays
     * where the key code is. It also has to happen *before* anything is
     * touched, because `config gen` will not report a bad key — handed an SK
     * whose public half will not derive, it silently generates a fresh random
     * keypair (gen.go:674-677), so a mistyped paste would land the user on a
     * brand-new identity instead of an error message.
     *
     * The key travels as an argv element, visible in this child's `/proc`
     * entry for the length of the call. That is our own uid on a modern
     * Android, where no other app may read it, and the alternative — parsing
     * hex and doing secp256k1 in Kotlin — is exactly the hand-rolled key
     * handling this design refuses.
     */
    suspend fun derivePublicKey(secretKey: String): Result<String> {
        val sk = secretKey.trim()
        // A shape check first, so an obvious paste error costs no process.
        if (sk.length != SK_HEX_LENGTH || !sk.all { it.isDigit() || it.lowercaseChar() in 'a'..'f' }) {
            return Result.failure(IllegalArgumentException("not a $SK_HEX_LENGTH-character hex secret key"))
        }
        val result = runCommand(
            listOf(paths.visorBinary.absolutePath, "config", "pk", sk),
            timeoutSeconds = 30,
        )
        // stdout and stderr are merged, and cobra may add its own lines, so
        // take the one line that looks like an answer rather than the last.
        val pk = result.output.lineSequence()
            .map { it.trim() }
            .lastOrNull { it.length == PK_HEX_LENGTH && it.all { c -> c.isDigit() || c.lowercaseChar() in 'a'..'f' } }
        return when {
            result.ok && pk != null -> Result.success(pk)
            else -> Result.failure(
                IllegalArgumentException(
                    result.output.lineSequence()
                        .map { it.trim() }
                        .lastOrNull { it.isNotEmpty() }
                        ?: "the core could not read that secret key",
                ),
            )
        }
    }

    /**
     * Rebuild the config around [secretKey], returning the new public key.
     * **Call with the core stopped.**
     *
     * There is no `--sk` flag: `config gen -r` takes the secret key from the
     * config it is about to overwrite (gen.go:933-947), so installing a key
     * means writing it into that file first and letting the regenerate read it
     * back. Everything else about the regenerate is the first-run pipeline —
     * same argv, and [applyPhoneProfile] re-applies the phone's pins at the
     * next start — with one addition the generator makes for free: the launcher
     * apps are merged rather than rebuilt, so per-app argv survives.
     */
    suspend fun replaceSecretKey(secretKey: String): Result<String> {
        val pk = derivePublicKey(secretKey).getOrElse { return Result.failure(it) }
        vault.unseal().getOrElse { return Result.failure(it) }
        return withContext(Dispatchers.IO) {
            runCatching {
                paths.ensureDirs()
                val existing = runCatching { readConfig() }.getOrDefault(JsonObject(emptyMap()))
                val seeded = JsonObject(
                    existing.toMutableMap().apply {
                        this["sk"] = JsonPrimitive(secretKey.trim())
                        // Dropped rather than corrected: the generator derives
                        // it, and a stale pk sitting next to a new sk for the
                        // length of one command is a lie waiting to be read.
                        remove("pk")
                    },
                )
                paths.configFile.writeText(json.encodeToString(JsonObject.serializer(), seeded))
                val gen = runGen()
                if (!gen.ok) {
                    error("config regeneration failed (exit ${gen.exitCode}):\n${gen.output.takeLast(2000)}")
                }
                clearIdentityData()
                pk
            }
        }
    }

    /**
     * Throw the identity away. **Call with the core stopped.**
     *
     * Deletes the config instead of regenerating one, so the next start runs
     * the untouched first-run path — one pipeline for a new identity, not two.
     */
    fun resetIdentity() {
        paths.configFile.delete()
        // Both forms go: a sealed config left behind would be adopted by the
        // next start as the identity the user just threw away.
        paths.sealedConfigFile.delete()
        clearIdentityData()
    }

    /**
     * Everything on this phone that belonged to the identity being replaced.
     *
     * A visor's key *is* its identity: chat history is a conversation with
     * peers who addressed a key that is about to stop existing, and the app
     * work dirs and transport logs under `local_path` are the same story. They
     * are cleared rather than carried over, so that what the confirmation
     * dialog promises is what happens — a phone that keeps a previous
     * identity's messages under a new one is showing a conversation nobody can
     * continue.
     *
     * `users.db` deliberately stays: the local API account is this app's own
     * device credential ([SecretStore]), not part of the visor's identity.
     */
    private fun clearIdentityData() {
        paths.localDir.deleteRecursively()
        paths.processLogFile.delete()
        File(paths.processLogFile.parentFile, paths.processLogFile.name + ".1").delete()
        paths.ensureDirs()
    }

    // --- export ---

    /** The config exactly as the visor reads it — secret key included. */
    /**
     * The config as text for export. Reads the sealed form when that is what
     * exists, decrypting into this string alone — exporting a config the user
     * asked to keep encrypted must not put a plaintext copy back on the disk.
     */
    suspend fun configJson(): String = vault.readText()

    /**
     * The config with the secret key removed, for the diagnostics bundle.
     *
     * Everything else in the file is either public (the visor's own key, the
     * service endpoints) or a path, and the app passwords live in files under
     * `local_path` that the argv only points at. The one secret in the
     * document is `sk`, and a log bundle is a thing people attach to issues.
     */
    fun redactedConfigJson(): String {
        val redacted = JsonObject(readConfig().toMutableMap().apply { remove("sk") })
        return json.encodeToString(JsonObject.serializer(), redacted)
    }

    /**
     * Reads whichever form is on disk. Through the vault, so the identity
     * screen still shows a public key — and a diagnostics bundle still carries
     * a redacted config — while the config is sealed and the core is down.
     */
    private fun readConfig(): JsonObject =
        json.parseToJsonElement(vault.readTextSync()).jsonObject

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
        const val DEFAULT_SOCKS_PORT = 1080
        val ADDR = listOf("--addr", "-addr")

        /** 32 bytes of secp256k1 secret, 33 of compressed public — as hex. */
        const val SK_HEX_LENGTH = 64
        const val PK_HEX_LENGTH = 66
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

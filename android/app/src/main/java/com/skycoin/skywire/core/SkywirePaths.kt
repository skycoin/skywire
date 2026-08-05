package com.skycoin.skywire.core

import android.content.Context
import java.io.File

/**
 * Every on-disk location the core touches, derived from one app-private root.
 * The visor is exec'd with cwd = [dataDir] so every relative path inside the
 * generated config ("./local", the default config name, gen's cwd-derived
 * users.db) resolves under it.
 */
class SkywirePaths(context: Context) {
    /** Root for everything the visor reads/writes: <filesDir>/skywire. */
    val dataDir: File = File(context.filesDir, "skywire")

    val configFile: File = File(dataDir, "skywire-config.json")

    /** Config `local_path` — app workdirs, log DBs, uptime.db land here. */
    val localDir: File = File(dataDir, "local")

    /** Captured stdout/stderr of the visor child process (rotating). */
    val processLogFile: File = File(dataDir, "skywire-process.log")

    /**
     * The extracted, executable Go payload. Valid because the module packs
     * jniLibs with useLegacyPackaging=true.
     */
    val visorBinary: File = File(context.applicationInfo.nativeLibraryDir, "libskywire-mobile.so")

    /**
     * config `launcher.bin_path`. Deliberately NOT the native-library dir:
     * the launcher calls MkdirAll on this path at startup, and the library
     * dir both is read-only and changes on every app update — a stale one
     * makes the visor abort with "failed to create dir … permission denied".
     * Apps run in-process (empty `binary`), so nothing is ever executed from
     * here; it just has to be stable and writable.
     */
    val binDir: File = File(dataDir, "bin")

    /** TMPDIR for the child — os.TempDir() must be app-writable. */
    val tmpDir: File = File(context.cacheDir, "skywire-tmp")

    fun ensureDirs() {
        dataDir.mkdirs()
        localDir.mkdirs()
        binDir.mkdirs()
        tmpDir.mkdirs()
    }
}

/**
 * Environment for every core invocation (config gen and the visor itself).
 *
 * `HOME`/`TMPDIR` keep the Go code's home- and temp-dir lookups inside
 * app-private storage. `SKYWIRE_ANDROID_API_LEVEL` is what lets the core
 * enumerate network interfaces at all: Android 11+ denies app processes the
 * netlink dump Go's standard library uses, and the workaround only engages
 * when the core knows the device API level — which a CGO-free build cannot
 * discover for itself. `SKYWIRE_ANDROID_VPN_SOCKET` is where vpn-client asks
 * for a TUN: the app owns the interface, so it also owns the name of the
 * socket it hands descriptors out on. The leading `@` is Go's spelling of the
 * abstract namespace [SkyVpnService] binds in.
 */
internal fun coreEnv(paths: SkywirePaths): Map<String, String> = mapOf(
    "HOME" to paths.dataDir.absolutePath,
    "TMPDIR" to paths.tmpDir.absolutePath,
    "SKYWIRE_ANDROID_API_LEVEL" to android.os.Build.VERSION.SDK_INT.toString(),
    "SKYWIRE_ANDROID_VPN_SOCKET" to "@" + SkyVpnService.SOCKET_NAME,
)

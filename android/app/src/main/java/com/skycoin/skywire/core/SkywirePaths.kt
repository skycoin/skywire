package com.skycoin.skywire.core

import android.content.Context
import android.os.Build
import android.provider.Settings
import org.json.JSONObject
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

    /**
     * The config, encrypted, while the core is stopped and the user has asked
     * for it. Never present at the same time as a meaningful [configFile] —
     * see [ConfigVault]. Sits beside it rather than elsewhere so a config
     * backup or a manual copy of the data dir carries whichever form exists.
     */
    val sealedConfigFile: File = File(dataDir, "skywire-config.json.enc")

    /** Config `local_path` — app workdirs, log DBs, uptime.db land here. */
    val localDir: File = File(dataDir, "local")

    /** The visor's contact book, beside `local_path` (pkg/visor/contacts.go). */
    val contactsDir: File = File(dataDir, "contacts")

    /** The Skymail mailbox, beside `local_path` too (skymailDir in pkg/visor/skymail.go). */
    val mailDir: File = File(dataDir, "mail")

    /** The app install the config was last generated for; see [ConfigManager]. */
    val configStampFile: File = File(dataDir, "skywire-config.installed-at")

    /** The config as it was before an update regenerated it, until that finishes. */
    val configBackupFile: File = File(dataDir, "skywire-config.json.pre-update")

    /** Captured stdout/stderr of the visor child process (rotating). */
    val processLogFile: File = File(dataDir, "skywire-process.log")

    /** Stack traces of the app's own crashes, written by [CrashLog]. */
    val crashLogFile: File = File(dataDir, "app-crash.log")

    /** The chat page's console and WebView events, written by [ChatPageLog]. */
    val chatPageLogFile: File = File(dataDir, "skychat-page.log")

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

    /**
     * The device as the hardware survey describes it, passed to the core as
     * `SKYWIRE_ANDROID_DEVICE`: an app cannot read the DMI tables, machine-id
     * or real MACs a linux survey uses. ANDROID_ID is fixed per device, user
     * and signing key; the core hashes it into the survey's machine id.
     */
    val deviceJson: String = JSONObject().apply {
        put("android_id", Settings.Secure.getString(context.contentResolver, Settings.Secure.ANDROID_ID) ?: "")
        put("manufacturer", Build.MANUFACTURER)
        put("brand", Build.BRAND)
        put("model", Build.MODEL)
        put("device", Build.DEVICE)
        put("product", Build.PRODUCT)
        put("board", Build.BOARD)
        put("hardware", Build.HARDWARE)
        put("fingerprint", Build.FINGERPRINT)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            put("soc_manufacturer", Build.SOC_MANUFACTURER)
            put("soc_model", Build.SOC_MODEL)
        }
        put("release", Build.VERSION.RELEASE)
        put("sdk", Build.VERSION.SDK_INT)
    }.toString()

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
 * socket it hands descriptors out on. `SKYWIRE_ANDROID_VPN_SHARE_SOCKET` is
 * where it collects the VPN hotspot's connections ([VpnHotspot]). The leading
 * `@` is Go's spelling of the abstract namespace both are bound in.
 * `SKYWIRE_ANDROID_DEVICE` feeds the hardware survey ([SkywirePaths.deviceJson]).
 */
internal fun coreEnv(paths: SkywirePaths): Map<String, String> = mapOf(
    "HOME" to paths.dataDir.absolutePath,
    "TMPDIR" to paths.tmpDir.absolutePath,
    "SKYWIRE_ANDROID_API_LEVEL" to android.os.Build.VERSION.SDK_INT.toString(),
    "SKYWIRE_ANDROID_VPN_SOCKET" to "@" + SkyVpnService.SOCKET_NAME,
    "SKYWIRE_ANDROID_VPN_SHARE_SOCKET" to "@" + VpnHotspot.SOCKET_NAME,
    "SKYWIRE_ANDROID_DEVICE" to paths.deviceJson,
)

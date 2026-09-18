package com.skycoin.skywire.core

import android.app.PendingIntent
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.provider.Settings
import android.util.Log
import androidx.core.net.toUri
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import okhttp3.Request
import java.io.File
import java.security.MessageDigest
import java.util.concurrent.TimeUnit
import kotlin.coroutines.coroutineContext

/**
 * Finding, fetching and installing a newer build of this app.
 *
 * The app is distributed as an APK on GitHub Releases, not through a store,
 * so nothing tells a user that a new one exists. They find out on Telegram,
 * or they do not find out. This is the missing half: Settings asks the
 * releases API, and if something newer is published it downloads that
 * release's APK and hands it to the system installer.
 *
 * **The mobile release line only.** Releases of the app are cut as
 * `mobile-vX.Y.Z`, titled "Skywire Mobile X.Y.Z"; the project's own
 * `vX.Y.Z` releases are the visor's and version on their own track, currently
 * 1.3.x against the app's 0.0.x. Some of those carry an
 * `android-arm64.apk` built by the release workflow, and taking one as an
 * update would be wrong twice over: it is not the build this app's versions
 * describe, and because 1.3.93 outranks every 0.0.x the phone would jump to
 * it once and then never see a real mobile release again. [isMobileRelease]
 * is the gate, and it is the whole reason this does not simply take the
 * newest APK on the repo.
 *
 * **What actually protects the install** is not this code: Android refuses to
 * upgrade an app with an APK signed by a different key, so a substituted file
 * cannot replace this app whatever it claims to be. The SHA-256 published
 * beside the APK is checked anyway, because a truncated download otherwise
 * surfaces as an unexplained installer failure instead of "the download did
 * not complete".
 */
object AppUpdates {

    private const val TAG = "SkywireUpdates"

    /** Where releases are published. Owner and repo are not configurable. */
    private const val RELEASES_URL =
        "https://api.github.com/repos/skycoin/skywire/releases?per_page=30"

    /** The releases page, for the fallback that opens a browser instead. */
    const val RELEASES_PAGE = "https://github.com/skycoin/skywire/releases"

    /** GitHub rejects an API request that does not say who is asking. */
    private const val USER_AGENT = "skywire-android"

    /** How long a check stands before the screen asks again on its own. */
    val CHECK_INTERVAL_MS = TimeUnit.HOURS.toMillis(6)

    /** Preference holding when the last automatic check ran. */
    const val PREF_LAST_CHECK = "updates_last_check"

    /** Cache subdirectory the downloaded APK lives in until it is installed. */
    private const val DOWNLOAD_DIR = "updates"

    private val json = Json { ignoreUnknownKeys = true }

    // A 56 MB download over whatever connection a phone has: no call timeout,
    // and a read timeout that bounds a stall rather than the transfer. The
    // same client serves the API call, where the payload is a few KB and the
    // difference does not arise.
    private val client = OkHttpClient.Builder()
        .connectTimeout(15, TimeUnit.SECONDS)
        .readTimeout(60, TimeUnit.SECONDS)
        .build()

    /**
     * A release version, compared the way versions are and not the way
     * strings are — "0.0.10" is above "0.0.9", which `>` on the text is not.
     */
    data class Version(val major: Int, val minor: Int, val patch: Int) : Comparable<Version> {
        override fun compareTo(other: Version): Int = compareValuesBy(
            this, other, Version::major, Version::minor, Version::patch,
        )

        override fun toString(): String = "$major.$minor.$patch"
    }

    /** A published release that carries an APK this phone can install. */
    data class Release(
        val version: Version,
        val tag: String,
        val title: String,
        val notes: String,
        val apkUrl: String,
        val apkName: String,
        val apkSize: Long,
        /** Absent on a release published without one — see the class note. */
        val sha256Url: String?,
        val pageUrl: String,
    )

    /**
     * `X.Y.Z` out of a release tag, or null when the tag is not one.
     *
     * Accepts `1.2.3`, `v1.2.3` and `mobile-v1.2.3` — the shape of the tag is
     * not what decides whether a release is ours (see [isMobileRelease]),
     * only what its number is. A suffix is tolerated and ignored
     * (`v1.2.3-rc1` reads as 1.2.3) because the ordering this is for is
     * between releases, and a tag that does not start with three numbers is
     * not a release at all.
     */
    fun versionOf(tag: String): Version? {
        val bare = tag.removePrefix("mobile-").removePrefix("v")
        val m = Regex("""^(\d+)\.(\d+)\.(\d+)""").find(bare) ?: return null
        val (major, minor, patch) = m.destructured
        return Version(major.toInt(), minor.toInt(), patch.toInt())
    }

    /** The version this app was built as, or null when it cannot be read. */
    fun installedVersion(context: Context): Version? = runCatching {
        context.packageManager.getPackageInfo(context.packageName, 0).versionName
    }.getOrNull()?.let(::versionOf)

    /**
     * The newest published release carrying an arm64 APK, or null when there
     * is none newer than what is installed.
     *
     * Deliberately not `/releases/latest`: that is the newest release of the
     * whole project, which is usually a desktop-only one with no APK
     * attached, and asking it would report "no update" forever. The list is
     * read instead and the newest release that actually ships an APK wins.
     *
     * Drafts and pre-releases are skipped. A phone on the stable channel is
     * not the place to discover a release candidate.
     */
    suspend fun check(context: Context): Result<Release?> = withContext(Dispatchers.IO) {
        val installed = installedVersion(context)
            ?: return@withContext Result.failure(IllegalStateException("no installed version"))
        runCatching {
            val body = client.newCall(
                Request.Builder()
                    .url(RELEASES_URL)
                    .header("Accept", "application/vnd.github+json")
                    .header("User-Agent", USER_AGENT)
                    .build(),
            ).execute().use { response ->
                if (!response.isSuccessful) error("GitHub answered ${response.code}")
                response.body.string()
            }
            selectUpdate(body, installed)
        }.onFailure { Log.w(TAG, "release check failed", it) }
    }

    /**
     * The pick, out of the API's own answer — everything [check] does once
     * the bytes are in hand, and the only part of it with anything to get
     * wrong: which tags count, which assets count, and which of the surviving
     * releases is newest. Kept separate from the request so it can be tested
     * against a fixture instead of against GitHub.
     */
    internal fun selectUpdate(body: String, installed: Version): Release? =
        json.decodeFromString<List<GhRelease>>(body)
            .asSequence()
            .filterNot { it.draft || it.prerelease }
            .filter(::isMobileRelease)
            .mapNotNull(::toRelease)
            .filter { it.version > installed }
            .maxByOrNull { it.version }

    /**
     * Whether a release is one of *this app's*.
     *
     * Releases of the app are cut as tag `mobile-vX.Y.Z`, titled
     * "Skywire Mobile X.Y.Z". Both are matched and either alone is enough:
     * requiring both would turn a small change in how one release was typed
     * into "updates stopped working" for everyone already installed, and that
     * failure is silent. Case is not load-bearing.
     *
     * The title is matched as a **phrase, not a substring**. A bare
     * `contains("mobile")` also accepts the project's own releases whenever
     * one is titled "v1.3.95 - mobile fixes", and those really do attach an
     * `android-arm64.apk`. Because every Android artifact in this repo is
     * signed with the one keystore the release workflow guards, such a build
     * would not merely be offered - it would install, and 1.3.95 outranks
     * every 0.0.x forever after. The loose form fails in exactly the way this
     * gate exists to prevent, and it fails on a release note nobody would
     * think twice about writing.
     */
    private fun isMobileRelease(gh: GhRelease): Boolean =
        gh.tagName.startsWith("mobile-", ignoreCase = true) ||
            MOBILE_TITLE.containsMatchIn(gh.name.orEmpty())

    /** "Skywire Mobile", as a phrase on word boundaries - see [isMobileRelease]. */
    private val MOBILE_TITLE = Regex("\\bskywire\\s+mobile\\b", RegexOption.IGNORE_CASE)

    /** A GitHub release becomes ours only if it has an APK we can install. */
    private fun toRelease(gh: GhRelease): Release? {
        val version = versionOf(gh.tagName) ?: return null
        val apk = gh.assets.firstOrNull {
            it.name.endsWith(".apk", ignoreCase = true) && it.name.contains("arm64", true)
        } ?: return null
        val sha = gh.assets.firstOrNull { it.name.equals("${apk.name}.sha256", true) }
        return Release(
            version = version,
            tag = gh.tagName,
            title = gh.name?.takeIf { it.isNotBlank() } ?: gh.tagName,
            notes = gh.body.orEmpty().trim(),
            apkUrl = apk.browserDownloadUrl,
            apkName = apk.name,
            apkSize = apk.size,
            sha256Url = sha?.browserDownloadUrl,
            pageUrl = gh.htmlUrl,
        )
    }

    /**
     * Fetch [release]'s APK into the cache, reporting progress as a fraction.
     *
     * Into `cacheDir` rather than anywhere durable: once the install has run
     * the file is 56 MB of nothing, and a cache is the one place the system
     * will reclaim it without being asked. The directory is emptied first, so
     * an abandoned half-download from a previous attempt cannot accumulate or
     * be mistaken for this one.
     *
     * [onProgress] is called with -1f while the size is unknown, which is
     * what a server that sends no Content-Length leaves us with.
     */
    suspend fun download(
        context: Context,
        release: Release,
        onProgress: (Float) -> Unit,
    ): Result<File> = withContext(Dispatchers.IO) {
        runCatching {
            val dir = File(context.cacheDir, DOWNLOAD_DIR).apply {
                deleteRecursively()
                mkdirs()
            }
            val target = File(dir, release.apkName)
            client.newCall(
                Request.Builder()
                    .url(release.apkUrl)
                    .header("User-Agent", USER_AGENT)
                    .build(),
            ).execute().use { response ->
                if (!response.isSuccessful) error("download answered ${response.code}")
                val body = response.body
                val total = body.contentLength().takeIf { it > 0 } ?: release.apkSize
                body.byteStream().use { input ->
                    target.outputStream().use { output ->
                        val buffer = ByteArray(64 * 1024)
                        var written = 0L
                        while (true) {
                            // Cancellation is the back button or leaving the
                            // screen; without this the transfer would run on
                            // to the end of a 56 MB file that nobody wants.
                            coroutineContext.ensureActive()
                            val read = input.read(buffer)
                            if (read < 0) break
                            output.write(buffer, 0, read)
                            written += read
                            onProgress(if (total > 0) written.toFloat() / total else -1f)
                        }
                    }
                }
            }
            // A release that published no checksum is taken on trust in the
            // platform's signature check, which is the decisive gate anyway.
            // One that published a checksum we then could not read is NOT:
            // the flaky connection that truncates a 56 MB APK is the same
            // connection that fails the 94-byte fetch beside it, so treating
            // unreadable as "no checksum" would retire the check at exactly
            // the moment it is the one thing that would have caught the fault.
            release.sha256Url?.let { url ->
                val published = fetchSha256(url)
                    ?: run {
                        target.delete()
                        error("the published checksum could not be read")
                    }
                val actual = sha256(target)
                if (!published.equals(actual, ignoreCase = true)) {
                    target.delete()
                    error("checksum mismatch: expected $published, got $actual")
                }
            } ?: Log.i(TAG, "release ${release.tag} published no checksum")
            target
        }.onFailure { Log.w(TAG, "download failed", it) }
    }

    /**
     * The digest out of a `sha256sum` file: `<hex>  <filename>`.
     *
     * null means the digest could not be obtained — the fetch failed, the
     * server refused, or what came back was not 64 hex characters. It never
     * means "this release has no checksum"; that is [Release.sha256Url] being
     * null, which the caller separates because the two deserve opposite
     * treatment.
     */
    private fun fetchSha256(url: String): String? = runCatching {
        client.newCall(
            Request.Builder().url(url).header("User-Agent", USER_AGENT).build(),
        ).execute().use { response ->
            if (!response.isSuccessful) return@runCatching null
            response.body.string().trim().substringBefore(' ').takeIf { it.length == 64 }
        }
    }.getOrNull()

    private fun sha256(file: File): String {
        val digest = MessageDigest.getInstance("SHA-256")
        file.inputStream().use { input ->
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val read = input.read(buffer)
                if (read < 0) break
                digest.update(buffer, 0, read)
            }
        }
        return digest.digest().joinToString("") { "%02x".format(it) }
    }

    /**
     * Whether this app may install an APK at all.
     *
     * Since API 26 "unknown sources" is per-app rather than one switch for the
     * whole phone, and it starts off. Holding REQUEST_INSTALL_PACKAGES in the
     * manifest is only permission to *ask*; the user grants the rest in
     * Settings, and [openInstallPermission] is the way there.
     */
    fun canInstall(context: Context): Boolean =
        runCatching { context.packageManager.canRequestPackageInstalls() }.getOrDefault(false)

    /** The per-app "install unknown apps" screen, for this app. */
    fun openInstallPermission(context: Context): Boolean {
        val intent = Intent(
            Settings.ACTION_MANAGE_UNKNOWN_APP_SOURCES,
            "package:${context.packageName}".toUri(),
        ).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        return runCatching { context.startActivity(intent) }.isSuccess
    }

    /**
     * Hand [apk] to the system installer.
     *
     * [PackageInstaller] rather than an ACTION_VIEW on a content:// uri: the
     * session API takes the file by stream, so there is no FileProvider to
     * declare and no grant to leak, and its result comes back as a status
     * broadcast instead of an Activity result nobody is around to receive.
     * The confirmation dialog is still the system's — this cannot install
     * anything quietly, which is the point.
     */
    suspend fun install(context: Context, apk: File): Result<Unit> = withContext(Dispatchers.IO) {
        runCatching {
            val app = context.applicationContext
            val installer = app.packageManager.packageInstaller
            val params = PackageInstaller.SessionParams(
                PackageInstaller.SessionParams.MODE_FULL_INSTALL,
            ).apply { setAppPackageName(app.packageName) }
            val sessionId = installer.createSession(params)
            installer.openSession(sessionId).use { session ->
                session.openWrite("skywire", 0, apk.length()).use { output ->
                    apk.inputStream().use { it.copyTo(output) }
                    session.fsync(output)
                }
                session.commit(statusIntent(app, sessionId).intentSender)
            }
        }.onFailure { Log.w(TAG, "install failed", it) }
    }

    /**
     * Where the installer reports back to.
     *
     * FLAG_MUTABLE is required and is not a lapse: the system fills this
     * intent in with the session's status and, when it needs one, the
     * confirmation Activity to launch. An immutable one arrives empty and the
     * install stalls with no dialog and no error.
     */
    private fun statusIntent(context: Context, sessionId: Int): PendingIntent = PendingIntent
        .getBroadcast(
            context,
            sessionId,
            Intent(context, UpdateInstallReceiver::class.java)
                .setAction(UpdateInstallReceiver.ACTION_INSTALL_STATUS)
                .setPackage(context.packageName),
            PendingIntent.FLAG_MUTABLE or PendingIntent.FLAG_UPDATE_CURRENT,
        )

    /** Open the release page in a browser — the way out when installing fails. */
    fun openReleasePage(context: Context, url: String = RELEASES_PAGE): Boolean {
        val intent = Intent(Intent.ACTION_VIEW, url.toUri())
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        return runCatching { context.startActivity(intent) }.isSuccess
    }

    /** Drop a downloaded APK once it is installed or abandoned. */
    fun clearDownloads(context: Context) {
        runCatching { File(context.cacheDir, DOWNLOAD_DIR).deleteRecursively() }
    }

    // --- the shape of the releases API, only the fields that are used ---

    @Serializable
    private data class GhRelease(
        @SerialName("tag_name") val tagName: String,
        val name: String? = null,
        val body: String? = null,
        val draft: Boolean = false,
        val prerelease: Boolean = false,
        @SerialName("html_url") val htmlUrl: String = RELEASES_PAGE,
        val assets: List<GhAsset> = emptyList(),
    )

    @Serializable
    private data class GhAsset(
        val name: String,
        val size: Long = 0L,
        @SerialName("browser_download_url") val browserDownloadUrl: String,
    )
}

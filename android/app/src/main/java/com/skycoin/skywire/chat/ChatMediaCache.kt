package com.skycoin.skywire.chat

import android.app.DownloadManager
import android.content.Context
import android.os.Build
import android.os.Environment
import android.util.Log
import androidx.core.net.toUri
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import kotlinx.coroutines.withContext
import java.io.File
import java.security.MessageDigest

/**
 * Local copies of served chat files (`/files/…`, `/thumb/…`): voice messages
 * and round videos are played from disk, thumbnails decoded from it. Bounded,
 * oldest first out.
 */
class ChatMediaCache(private val context: Context, private val http: SkychatHttp) {

    private val dir = File(context.cacheDir, "chat-media").apply { mkdirs() }
    private val lock = Mutex()
    private val inflight = HashMap<String, CompletableDeferred<File?>>()

    fun cachedFile(path: String): File? = fileFor(path).takeIf { it.isFile && it.length() > 0 }

    /** The served file at [path] on disk, downloading it once if needed; null when it cannot be had. */
    suspend fun get(path: String): File? {
        cachedFile(path)?.let {
            it.setLastModified(System.currentTimeMillis())
            return it
        }
        val (waiter, owner) = lock.withLock {
            inflight[path]?.let { it to false } ?: CompletableDeferred<File?>().also { inflight[path] = it }.let { it to true }
        }
        if (!owner) return waiter.await()
        val result = runCatching { download(path) }
            .onFailure { Log.d(TAG, "fetch $path failed: ${it.message}") }
            .getOrNull()
        lock.withLock { inflight.remove(path) }
        waiter.complete(result)
        return result
    }

    private suspend fun download(path: String): File = withContext(Dispatchers.IO) {
        val target = fileFor(path)
        val part = File(dir, target.name + ".part")
        http.open(path).use { resp ->
            part.outputStream().use { out -> resp.body.byteStream().copyTo(out) }
        }
        part.renameTo(target)
        prune()
        target
    }

    fun drop(path: String) {
        fileFor(path).delete()
    }

    private fun fileFor(path: String): File {
        val digest = MessageDigest.getInstance("SHA-256").digest(path.toByteArray())
        val ext = path.substringAfterLast('/').substringAfterLast('.', "").take(8).filter { it.isLetterOrDigit() }
        val name = digest.take(16).joinToString("") { "%02x".format(it) } + if (ext.isNotEmpty()) ".$ext" else ""
        return File(dir, name)
    }

    private fun prune() {
        val files = dir.listFiles()?.filter { it.isFile } ?: return
        var total = files.sumOf { it.length() }
        if (total <= MAX_BYTES) return
        for (f in files.sortedBy { it.lastModified() }) {
            total -= f.length()
            f.delete()
            if (total <= MAX_BYTES * 3 / 4) break
        }
    }

    /** Save a served file to the phone's Downloads through the system download manager. */
    suspend fun saveToDownloads(path: String, name: String, mime: String?) {
        val request = DownloadManager.Request(http.url(path).toUri())
            .setTitle(name)
            .addRequestHeader("Authorization", http.authorization())
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
        mime?.takeIf { it.isNotEmpty() }?.let { request.setMimeType(it) }
        // Public Downloads needs no permission from API 29; older phones get the app's own folder.
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            request.setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, name)
        } else {
            request.setDestinationInExternalFilesDir(context, Environment.DIRECTORY_DOWNLOADS, name)
        }
        (context.getSystemService(Context.DOWNLOAD_SERVICE) as DownloadManager).enqueue(request)
    }

    private companion object {
        const val TAG = "ChatMedia"
        const val MAX_BYTES = 256L * 1024 * 1024
    }
}

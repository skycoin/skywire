package com.skycoin.skywire.chat

import android.content.Context
import com.skycoin.skywire.core.SecretStore
import com.skycoin.skywire.core.SkychatProfile
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import okhttp3.Credentials
import okhttp3.MediaType
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.MultipartBody
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.RequestBody
import okhttp3.RequestBody.Companion.toRequestBody
import okhttp3.Response
import okio.BufferedSink
import okio.source
import java.io.IOException
import java.io.InputStream
import java.util.concurrent.TimeUnit

/** A non-2xx answer from skychat, with the body it explained itself in. */
class SkychatHttpException(val code: Int, message: String) : IOException(message)

/**
 * The authenticated wire to skychat's own HTTP surface on loopback.
 *
 * Every call carries the device-local password (see [SkychatProfile]), so no
 * other app on the phone can read or send as this visor.
 */
class SkychatHttp(context: Context) {

    private val secrets = SecretStore(context.applicationContext)

    /** `http://127.0.0.1:<port>`, set once the app answers; null before. */
    @Volatile
    var baseUrl: String? = null

    @Volatile
    private var authHeader: String? = null

    private val client = OkHttpClient.Builder()
        .connectTimeout(3, TimeUnit.SECONDS)
        .readTimeout(30, TimeUnit.SECONDS)
        .build()

    // Sends that reach a peer (a DM dial, a group join, a profile lookup) can
    // take tens of seconds over dmsg.
    private val slow = client.newBuilder()
        .readTimeout(90, TimeUnit.SECONDS)
        .build()

    // Uploads and the event stream have no natural end.
    private val stream = client.newBuilder()
        .readTimeout(0, TimeUnit.SECONDS)
        .writeTimeout(0, TimeUnit.SECONDS)
        .build()

    suspend fun authorization(): String =
        authHeader ?: Credentials.basic(SkychatProfile.USER, secrets.skychatPassword()).also { authHeader = it }

    fun url(path: String): String {
        val base = baseUrl ?: throw IOException("SkyChat is not running")
        return base.trimEnd('/') + path
    }

    private suspend fun request(path: String): Request.Builder =
        Request.Builder().url(url(path)).header("Authorization", authorization())

    suspend fun get(path: String, slowPeer: Boolean = false): String = withContext(Dispatchers.IO) {
        call(if (slowPeer) slow else client, request(path).get().build())
    }

    suspend fun post(path: String, json: String = "{}", slowPeer: Boolean = false): String =
        withContext(Dispatchers.IO) {
            call(if (slowPeer) slow else client, request(path).post(json.toRequestBody(JSON)).build())
        }

    suspend fun postBytes(path: String, bytes: ByteArray, type: String): String = withContext(Dispatchers.IO) {
        call(slow, request(path).post(bytes.toRequestBody(type.toMediaType())).build())
    }

    suspend fun put(path: String, bytes: ByteArray, type: String): String = withContext(Dispatchers.IO) {
        call(slow, request(path).put(bytes.toRequestBody(type.toMediaType())).build())
    }

    suspend fun delete(path: String, json: String? = null): String = withContext(Dispatchers.IO) {
        val builder = request(path)
        if (json != null) builder.delete(json.toRequestBody(JSON)) else builder.delete()
        call(client, builder.build())
    }

    suspend fun bytes(path: String): ByteArray = withContext(Dispatchers.IO) {
        client.newCall(request(path).get().build()).execute().use { resp ->
            if (!resp.isSuccessful) throw SkychatHttpException(resp.code, errorText(resp))
            resp.body.bytes()
        }
    }

    /** A streaming GET the caller closes: downloads, the event stream. */
    suspend fun open(path: String, headers: Map<String, String> = emptyMap()): Response =
        withContext(Dispatchers.IO) {
            val builder = request(path).get()
            headers.forEach { (k, v) -> builder.header(k, v) }
            val resp = stream.newCall(builder.build()).execute()
            if (!resp.isSuccessful) {
                val text = errorText(resp)
                resp.close()
                throw SkychatHttpException(resp.code, text)
            }
            resp
        }

    /**
     * Multipart upload with progress, for `/send-file`. [file] is opened when
     * the body is written, so a picked document is streamed rather than read
     * into memory first.
     */
    suspend fun upload(
        path: String,
        fields: Map<String, String>,
        fileField: String,
        fileName: String,
        mime: String,
        size: Long,
        file: () -> InputStream,
        progress: (sent: Long, total: Long) -> Unit,
    ): String = withContext(Dispatchers.IO) {
        val part = object : RequestBody() {
            override fun contentType(): MediaType = mime.toMediaType()
            override fun contentLength(): Long = size
            override fun writeTo(sink: BufferedSink) {
                file().source().use { source ->
                    var sent = 0L
                    while (true) {
                        val read = source.read(sink.buffer, CHUNK)
                        if (read == -1L) break
                        sink.flush()
                        sent += read
                        progress(sent, size)
                    }
                }
            }
        }
        val body = MultipartBody.Builder().setType(MultipartBody.FORM).apply {
            fields.forEach { (k, v) -> addFormDataPart(k, v) }
            addFormDataPart(fileField, fileName, part)
        }.build()
        call(stream, request(path).post(body).build())
    }

    private fun call(with: OkHttpClient, req: Request): String =
        with.newCall(req).execute().use { resp ->
            val text = resp.body.string()
            if (!resp.isSuccessful) throw SkychatHttpException(resp.code, text.trim().ifEmpty { "HTTP ${resp.code}" })
            text
        }

    private fun errorText(resp: Response): String =
        runCatching { resp.body.string().trim() }.getOrNull()?.ifEmpty { null } ?: "HTTP ${resp.code}"

    companion object {
        private val JSON = "application/json".toMediaType()
        private const val CHUNK = 64 * 1024L
    }
}

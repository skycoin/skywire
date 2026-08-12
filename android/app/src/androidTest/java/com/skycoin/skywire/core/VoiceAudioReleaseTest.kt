package com.skycoin.skywire.core

import android.Manifest
import android.content.Context
import android.media.AudioManager
import androidx.test.ext.junit.runners.AndroidJUnit4
import androidx.test.platform.app.InstrumentationRegistry
import androidx.test.rule.GrantPermissionRule
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import org.junit.After
import org.junit.Assert.assertTrue
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import java.io.InputStream
import java.io.OutputStream
import java.net.InetAddress
import java.net.ServerSocket
import java.net.Socket
import java.util.concurrent.CopyOnWriteArrayList
import kotlin.concurrent.thread

/**
 * [VoiceAudioEngine.stop] must hand the microphone back.
 *
 * It did not. The mic is held open for exactly the lifetime of the request
 * body that carries it — `writeTo` opens the AudioRecord and loops on
 * read/write inside it — and because that body is chunked, OkHttp writes it
 * from inside `execute()`. Cancelling the capture Job therefore reached
 * nothing: a coroutine that never comes back to a suspension point never
 * observes its cancellation, so the `finally` holding `record.stop()` and
 * `record.release()` never ran and the recorder stayed open for the life of
 * the process. The stream client disables every timeout, so nothing else
 * would ever have ended it.
 *
 * The two tests below are the two ways a call ends, and before the fix each
 * failed on its own:
 *
 *  - the visor still draining the stream, where the write never blocks and
 *    only the `live` flag in the innermost loop gets us out;
 *  - the visor having stopped reading — the ordinary case, since that is what
 *    hanging up looks like — where the socket buffer fills, `sink.write`
 *    blocks, no flag can be reached, and cancelling the call is the only exit.
 *
 * A device test rather than a JVM one because both halves of the claim are
 * about real machinery: a real AudioRecord holding a real capture device, and
 * a real TCP connection whose buffer really fills. The fake visor below is the
 * only stand-in, and only because the visor's own audio endpoints are not what
 * is under test — the four routes it answers are the minimum
 * [com.skycoin.skywire.api.VisorApi] touches on the way to opening a stream.
 */
@RunWith(AndroidJUnit4::class)
class VoiceAudioReleaseTest {

    @get:Rule
    val micPermission: GrantPermissionRule =
        GrantPermissionRule.grant(Manifest.permission.RECORD_AUDIO)

    private val ctx: Context
        get() = InstrumentationRegistry.getInstrumentation().targetContext

    private var visor: FakeVisor? = null
    private var scope: CoroutineScope? = null
    private var engine: VoiceAudioEngine? = null

    @After
    fun tearDown() {
        engine?.stop()
        scope?.cancel()
        visor?.close()
    }

    /** The visor is reading the stream — nothing blocks, and stop() must still land. */
    @Test
    fun stopReleasesTheMicrophone() = assertStopReleasesTheMicrophone(drainBody = true)

    /**
     * The visor has stopped reading, which is what the far end of a hang-up
     * looks like. The capture thread ends up parked in `sink.write` with the
     * recorder open, and only cancelling the call can reach it.
     */
    @Test
    fun stopReleasesTheMicrophoneWhenTheVisorStopsReading() =
        assertStopReleasesTheMicrophone(drainBody = false)

    private fun assertStopReleasesTheMicrophone(drainBody: Boolean) {
        val fake = FakeVisor(drainBody).also { it.start() }
        visor = fake
        val runScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)
        scope = runScope
        val run = VoiceAudioEngine(ctx)
        engine = run

        run.start(runScope)

        assertTrue(
            "the engine never opened the microphone, so there was nothing to release — " +
                "check the emulator has a capture device and RECORD_AUDIO is granted",
            waitFor(OPEN_TIMEOUT_MS) { run.capturing },
        )
        assertTrue(
            "the platform reports no active recording while the engine says it is capturing",
            waitFor(OPEN_TIMEOUT_MS) { recordingActive() },
        )

        // Long enough that the writer is well inside the stream — and, when the
        // fake visor is not reading, long enough for the socket buffer to fill
        // at 96 KB/s and leave the thread genuinely blocked mid-write. That
        // blocked state is the whole point of the second case.
        Thread.sleep(SETTLE_MS)

        run.stop()

        assertTrue(
            "capturing was still true ${RELEASE_TIMEOUT_MS}ms after stop() — the recorder " +
                "was never released, which is the microphone indicator the user sees",
            waitFor(RELEASE_TIMEOUT_MS) { !run.capturing },
        )
        assertTrue(
            "the platform still reports an active recording ${RELEASE_TIMEOUT_MS}ms after " +
                "stop() — the capture device is still held",
            waitFor(RELEASE_TIMEOUT_MS) { !recordingActive() },
        )
    }

    private fun recordingActive(): Boolean {
        val audio = ctx.getSystemService(Context.AUDIO_SERVICE) as AudioManager
        return audio.activeRecordingConfigurations.isNotEmpty()
    }

    private fun waitFor(budgetMs: Long, condition: () -> Boolean): Boolean {
        val deadline = System.currentTimeMillis() + budgetMs
        while (System.currentTimeMillis() < deadline) {
            if (condition()) return true
            Thread.sleep(POLL_MS)
        }
        return condition()
    }

    private companion object {
        const val OPEN_TIMEOUT_MS = 15_000L
        const val RELEASE_TIMEOUT_MS = 5_000L
        const val SETTLE_MS = 3_000L
        const val POLL_MS = 50L
    }
}

/**
 * The four routes [com.skycoin.skywire.api.VisorApi] needs to open a voice
 * stream, on the port it is hard-wired to. Deliberately hand-rolled over a
 * ServerSocket: the behaviour that matters is a server that accepts an upload
 * and then *stops reading it*, which is a socket-level condition rather than
 * anything an HTTP mock exposes.
 *
 * [drainBody] false is that case. True keeps reading and discarding, which is
 * a visor with a call still up.
 */
private class FakeVisor(private val drainBody: Boolean) {

    private val server = ServerSocket(PORT, 16, InetAddress.getByName("127.0.0.1"))
    private val sockets = CopyOnWriteArrayList<Socket>()

    @Volatile
    private var running = true

    fun start() {
        thread(isDaemon = true, name = "fake-visor") {
            while (running) {
                val socket = try {
                    server.accept()
                } catch (_: Exception) {
                    return@thread
                }
                sockets += socket
                thread(isDaemon = true, name = "fake-visor-conn") { serve(socket) }
            }
        }
    }

    fun close() {
        running = false
        runCatching { server.close() }
        sockets.forEach { runCatching { it.close() } }
        sockets.clear()
    }

    private fun serve(socket: Socket) {
        runCatching {
            val input = socket.getInputStream()
            val output = socket.getOutputStream()
            // One connection can carry several short requests — OkHttp pools
            // them — so keep serving until the stream routes take it over or
            // the peer goes away.
            while (running) {
                val request = readRequestLine(input) ?: return
                readHeaders(input)
                when {
                    request.contains("/api/csrf") -> respondJson(output, """{"csrf_token":"test"}""")
                    request.contains("/api/about") -> respondJson(output, """{"public_key":"$PK"}""")
                    request.contains("/mic") -> {
                        holdMicUpload(input)
                        return
                    }
                    request.contains("/speaker") -> {
                        // Headers only, then silence: a call with nobody
                        // talking. The reader parks on the body, which is what
                        // the playback loop does for real.
                        output.write(
                            ("HTTP/1.1 200 OK\r\nContent-Type: application/octet-stream\r\n" +
                                "Transfer-Encoding: chunked\r\n\r\n").toByteArray()
                        )
                        output.flush()
                        while (running) Thread.sleep(50)
                        return
                    }
                    else -> respondJson(output, "{}")
                }
            }
        }
    }

    /**
     * Never answers: the visor holds the mic POST open for the whole call, so
     * the response only comes when the body ends. Either the body is drained
     * as fast as it arrives, or it is left alone until the window closes and
     * the client's next write blocks — the state this whole test exists for.
     */
    private fun holdMicUpload(input: InputStream) {
        val scratch = ByteArray(8 * 1024)
        while (running) {
            if (drainBody) {
                if (input.read(scratch) < 0) return
            } else {
                Thread.sleep(50)
            }
        }
    }

    private fun readRequestLine(input: InputStream): String? {
        val line = readLine(input) ?: return null
        return line.ifEmpty { null }
    }

    private fun readHeaders(input: InputStream) {
        while (true) {
            val line = readLine(input) ?: return
            if (line.isEmpty()) return
        }
    }

    // Byte-at-a-time so the body is never swallowed into a reader's buffer —
    // the mic route needs the raw stream exactly where the headers ended.
    private fun readLine(input: InputStream): String? {
        val out = StringBuilder()
        while (true) {
            val c = input.read()
            if (c < 0) return if (out.isEmpty()) null else out.toString()
            if (c == '\n'.code) return out.toString().removeSuffix("\r")
            out.append(c.toChar())
        }
    }

    private fun respondJson(output: OutputStream, body: String) {
        val bytes = body.toByteArray()
        output.write(
            ("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\n" +
                "Content-Length: ${bytes.size}\r\n\r\n").toByteArray()
        )
        output.write(bytes)
        output.flush()
    }

    private companion object {
        // VisorApi.BASE is hard-wired to 127.0.0.1:8000.
        const val PORT = 8000
        const val PK = "0000000000000000000000000000000000000000000000000000000000000000ff"
    }
}

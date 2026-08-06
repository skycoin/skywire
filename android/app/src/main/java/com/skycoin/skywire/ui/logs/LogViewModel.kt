package com.skycoin.skywire.ui.logs

import android.app.Application
import androidx.lifecycle.AndroidViewModel
import androidx.lifecycle.viewModelScope
import com.skycoin.skywire.api.VisorApi
import com.skycoin.skywire.core.SkywirePaths
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import java.io.RandomAccessFile

data class LogUiState(
    val entries: List<LogEntry> = emptyList(),
    /**
     * The first fetch for this source has not come back yet. Worth its own
     * state because one source is slow enough to matter: a remote visor's
     * buffer travels the whole ring over dmsg and takes several seconds, and
     * "No log entries yet." during that wait reads as "this is broken".
     */
    val loading: Boolean = false,
    val following: Boolean = true,
    val levels: Set<LogLevel> = emptySet(),
    val query: String = "",
    val dropped: Long = 0,
    /** Bumped on every append — a scroll-to-bottom key that keeps working
     *  after [entries] hits its size cap and the list size stops changing. */
    val revision: Long = 0,
    val error: String? = null,
) {
    /**
     * Level chips are additive; empty selection means "everything". ERROR
     * implies FATAL (no separate chip), and unparsed UNKNOWN lines always
     * pass — they are often the crash text a filter must not hide.
     */
    val visible: List<LogEntry>
        get() = entries.filter { entry ->
            val levelOk = levels.isEmpty() ||
                entry.level in levels ||
                entry.level == LogLevel.UNKNOWN ||
                (entry.level == LogLevel.FATAL && LogLevel.ERROR in levels)
            levelOk && (query.isBlank() || entry.raw.contains(query, ignoreCase = true))
        }
}

/**
 * Tails one log source. Polling stops while paused and resumes from the
 * kept cursor, so pausing never loses lines the source still holds.
 */
class LogViewModel(app: Application) : AndroidViewModel(app) {

    private val api = VisorApi.get(app)
    private val paths = SkywirePaths(app)
    private val mutable = MutableStateFlow(LogUiState())
    val uiState: StateFlow<LogUiState> = mutable.asStateFlow()

    private var pump: Job? = null
    private var source: String = LogSources.CORE

    // Per-source cursors.
    private var runtimeSince = 0L
    private var appSince: String? = null
    private var fileOffset = 0L
    private var filePendingTail = ""

    fun start(source: String) {
        if (pump?.isActive == true && source == this.source) return
        pump?.cancel()
        this.source = source
        runtimeSince = 0L
        appSince = null
        fileOffset = 0L
        filePendingTail = ""
        mutable.value = LogUiState(loading = true, following = mutable.value.following)
        pump = viewModelScope.launch {
            while (true) {
                if (mutable.value.following) {
                    runCatching { poll() }
                        .onFailure { e -> mutable.value = mutable.value.copy(error = e.message) }
                    // Whatever the first round did, the wait is over — an empty
                    // list from here on really is an empty list.
                    if (mutable.value.loading) {
                        mutable.value = mutable.value.copy(loading = false)
                    }
                }
                delay(POLL_INTERVAL_MS)
            }
        }
    }

    fun setFollowing(following: Boolean) {
        mutable.value = mutable.value.copy(following = following)
    }

    fun toggleLevel(level: LogLevel) {
        val levels = mutable.value.levels.toMutableSet()
        if (!levels.add(level)) levels.remove(level)
        mutable.value = mutable.value.copy(levels = levels)
    }

    fun setQuery(query: String) {
        mutable.value = mutable.value.copy(query = query)
    }

    private suspend fun poll() = when {
        source == LogSources.PROCESS -> pollProcessFile()
        LogSources.isApp(source) -> pollAppLogs(LogSources.appName(source))
        LogSources.isVisor(source) -> pollRuntimeLogs(LogSources.visorPk(source))
        else -> pollRuntimeLogs()
    }

    /** [pk] null tails this phone's visor; Fleet passes a remote one. */
    private suspend fun pollRuntimeLogs(pk: String? = null) {
        // No explicit session bootstrap: the client re-logins on 401 by
        // itself, and probing /api/user each poll would spam the very ring
        // buffer this feed displays.
        val firstPoll = runtimeSince == 0L
        val delta = api.runtimeLogs(runtimeSince, pk)
        if (delta.latest < runtimeSince) {
            // The visor restarted (its line counter reset) — start over so
            // the new process's startup lines aren't silently skipped.
            runtimeSince = 0
            return
        }
        val fresh = delta.entries.orEmpty().map(LogParser::parseJsonEntry)
        runtimeSince = delta.latest
        // On the opening poll the whole ring is "dropped" relative to cursor
        // 0 — that is the buffer's age, not lines this viewer missed.
        append(fresh, dropped = if (firstPoll) 0 else delta.dropped)
    }

    private suspend fun pollAppLogs(appName: String) {
        val page = api.appLogs(appName, appSince)
        if (page.logs.isEmpty()) {
            mutable.value = mutable.value.copy(error = null)
            return
        }
        val previous = appSince
        // The server re-delivers the boundary line whenever its 4-digit
        // fraction ends in zero (RFC3339Nano drops trailing zeros), so drop
        // anything at or before the cursor — but keep unparsable lines
        // (blank timestamp), which would otherwise vanish.
        val fresh = page.logs
            .map(LogParser::parseTextLine)
            .filter { previous == null || it.timestamp.isEmpty() || it.timestamp > previous }
        appSince = page.lastLogTimestamp.ifEmpty { appSince }
        append(fresh)
    }

    /**
     * Tail of the captured child output. Reads only the bytes appended since
     * the last poll (decoded as UTF-8, partial trailing line carried over);
     * a shrinking file means the log rotated, so restart from the beginning.
     */
    private suspend fun pollProcessFile() = withContext(Dispatchers.IO) {
        val file = paths.processLogFile
        if (!file.exists()) return@withContext
        val length = file.length()
        if (length < fileOffset) {
            fileOffset = 0
            filePendingTail = ""
        }
        if (length == fileOffset) return@withContext
        var chunk: ByteArray? = null
        RandomAccessFile(file, "r").use { raf ->
            raf.seek(fileOffset)
            val buf = ByteArray((length - fileOffset).coerceAtMost(MAX_READ_BYTES).toInt())
            val n = raf.read(buf)
            if (n > 0) {
                fileOffset += n
                chunk = if (n == buf.size) buf else buf.copyOf(n)
            }
        }
        val bytes = chunk ?: return@withContext
        val text = filePendingTail + String(bytes, Charsets.UTF_8)
        val parts = text.split('\n')
        filePendingTail = parts.last()
        append(parts.dropLast(1).filter { it.isNotBlank() }.map(LogParser::parseTextLine))
    }

    /** Everything the share sheet gets — capped: a full 2000-line buffer in
     *  an Intent extra risks TransactionTooLargeException. */
    fun shareText(): String =
        mutable.value.visible.takeLast(MAX_SHARE_LINES).joinToString("\n") { it.raw }

    private fun append(fresh: List<LogEntry>, dropped: Long = 0) {
        if (fresh.isEmpty() && dropped == 0L) {
            if (mutable.value.error != null) mutable.value = mutable.value.copy(error = null)
            return
        }
        val current = mutable.value
        mutable.value = current.copy(
            entries = (current.entries + fresh).takeLast(MAX_ENTRIES),
            dropped = current.dropped + dropped,
            revision = current.revision + 1,
            error = null,
        )
    }

    private companion object {
        const val POLL_INTERVAL_MS = 1_200L
        const val MAX_ENTRIES = 2_000
        const val MAX_SHARE_LINES = 500
        const val MAX_READ_BYTES = 512_000L
    }
}

package com.skycoin.skywire.ui.logs

import android.content.Intent
import android.widget.Toast
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.BasicTextField
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Pause
import androidx.compose.material.icons.filled.PlayArrow
import androidx.compose.material.icons.filled.Share
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.core.VisorNames
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.appProductName
import com.skycoin.skywire.ui.components.shortPk

/**
 * The one log viewer, used by every screen that has a log feed: the core
 * runtime feed, any app's feed, and the captured process output (the only
 * source that survives a visor that won't start).
 */
@Composable
fun LogViewerScreen(
    source: String,
    onBack: () -> Unit,
    viewModel: LogViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    val clipboard = LocalClipboardManager.current
    val copied = stringResource(R.string.copied_to_clipboard)
    val listState = rememberLazyListState()

    LaunchedEffect(source) { viewModel.start(source) }

    // Keyed on the append revision, not the list size — the size stops
    // changing once the buffer hits its cap, but the tail keeps moving.
    val visible = state.visible
    LaunchedEffect(state.revision, state.following) {
        if (state.following && visible.isNotEmpty()) {
            listState.animateScrollToItem(visible.lastIndex)
        }
    }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = titleFor(source),
                onBack = onBack,
                actions = {
                    IconButton(onClick = { viewModel.setFollowing(!state.following) }) {
                        Icon(
                            if (state.following) Icons.Default.Pause else Icons.Default.PlayArrow,
                            contentDescription = stringResource(
                                if (state.following) R.string.logs_pause else R.string.logs_follow,
                            ),
                        )
                    }
                    IconButton(
                        onClick = {
                            val text = viewModel.shareText()
                            context.startActivity(
                                Intent.createChooser(
                                    Intent(Intent.ACTION_SEND).apply {
                                        type = "text/plain"
                                        putExtra(Intent.EXTRA_TEXT, text)
                                    },
                                    context.getString(R.string.logs_share),
                                ),
                            )
                        },
                    ) {
                        Icon(Icons.Default.Share, contentDescription = stringResource(R.string.logs_share))
                    }
                },
            )
        },
    ) { padding ->
        Column(
            Modifier
                .fillMaxSize()
                .padding(padding),
        ) {
            FilterBar(state, viewModel)
            state.dropped.takeIf { it > 0 }?.let { dropped ->
                Text(
                    stringResource(R.string.logs_dropped, dropped),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }
            state.error?.let { error ->
                Text(
                    error,
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier.padding(horizontal = 16.dp, vertical = 4.dp),
                )
            }

            if (visible.isEmpty()) {
                Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) {
                    if (state.loading) {
                        Column(horizontalAlignment = Alignment.CenterHorizontally) {
                            CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
                            Spacer(Modifier.height(12.dp))
                            Text(
                                stringResource(R.string.logs_loading),
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    } else {
                        Text(
                            stringResource(R.string.logs_empty),
                            style = MaterialTheme.typography.bodyMedium,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                        )
                    }
                }
            } else {
                LazyColumn(
                    state = listState,
                    modifier = Modifier.fillMaxSize(),
                    contentPadding = androidx.compose.foundation.layout.PaddingValues(
                        horizontal = 12.dp,
                        vertical = 8.dp,
                    ),
                ) {
                    items(visible) { entry ->
                        LogRow(entry) {
                            clipboard.setText(AnnotatedString(entry.raw))
                            Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun FilterBar(state: LogUiState, viewModel: LogViewModel) {
    Column(Modifier.padding(horizontal = 12.dp)) {
        Row(
            Modifier
                .fillMaxWidth()
                .horizontalScroll(rememberScrollState()),
            horizontalArrangement = Arrangement.spacedBy(6.dp),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            FILTER_LEVELS.forEach { level ->
                FilterChip(
                    selected = level in state.levels,
                    onClick = { viewModel.toggleLevel(level) },
                    label = { Text(level.name, style = MaterialTheme.typography.labelSmall) },
                )
            }
        }
        Box(
            Modifier
                .fillMaxWidth()
                .padding(vertical = 8.dp)
                .background(
                    MaterialTheme.colorScheme.surfaceVariant,
                    RoundedCornerShape(12.dp),
                )
                .padding(horizontal = 12.dp, vertical = 10.dp),
        ) {
            if (state.query.isEmpty()) {
                Text(
                    stringResource(R.string.logs_search_hint),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            BasicTextField(
                value = state.query,
                onValueChange = viewModel::setQuery,
                singleLine = true,
                textStyle = MaterialTheme.typography.bodyMedium.copy(
                    color = MaterialTheme.colorScheme.onSurface,
                ),
                cursorBrush = androidx.compose.ui.graphics.SolidColor(
                    MaterialTheme.colorScheme.primary,
                ),
                modifier = Modifier.fillMaxWidth(),
            )
        }
    }
}

@Composable
private fun LogRow(entry: LogEntry, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 3.dp),
    ) {
        Text(
            entry.level.name.take(1),
            style = MaterialTheme.typography.labelSmall,
            color = levelColor(entry.level),
            modifier = Modifier.padding(end = 8.dp, top = 2.dp),
        )
        Column {
            if (entry.module.isNotEmpty() || entry.timestamp.isNotEmpty()) {
                Text(
                    listOf(entry.timestamp.takeLast(TIME_TAIL), entry.module)
                        .filter { it.isNotEmpty() }
                        .joinToString(" · "),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Text(
                entry.message,
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                color = if (entry.level == LogLevel.ERROR || entry.level == LogLevel.FATAL) {
                    MaterialTheme.colorScheme.error
                } else {
                    MaterialTheme.colorScheme.onSurface
                },
            )
        }
    }
}

@Composable
private fun titleFor(source: String): String = when {
    source == LogSources.PROCESS -> stringResource(R.string.logs_source_process)
    // The product name alone here, unlike the diagnostics list: a centered
    // app-bar title has no room for "SkySOCKS (skysocks-client)", and the row
    // that opened this screen already showed both.
    LogSources.isApp(source) -> LogSources.appName(source)
        .let { name -> appProductName(name) ?: name }
    // A remote visor's feed. Titled with the name the user gave it in Fleet —
    // the same store, read straight from here rather than threaded through the
    // navigation route — and its key when they have not named it.
    LogSources.isVisor(source) -> {
        val pk = LogSources.visorPk(source)
        val context = LocalContext.current
        val names by remember(context) { VisorNames(context).names() }
            .collectAsState(initial = emptyMap())
        names[pk]?.takeIf { it.isNotEmpty() } ?: shortPk(pk)
    }
    else -> stringResource(R.string.logs_source_core)
}

private fun levelColor(level: LogLevel): Color = when (level) {
    LogLevel.ERROR, LogLevel.FATAL -> Color(0xFFDC2626)
    LogLevel.WARN -> Color(0xFFF59E0B)
    LogLevel.INFO -> Color(0xFF0072FF)
    else -> Color(0xFF9CA3AF)
}

private val FILTER_LEVELS = listOf(
    LogLevel.ERROR,
    LogLevel.WARN,
    LogLevel.INFO,
    LogLevel.DEBUG,
    LogLevel.TRACE,
)

private const val TIME_TAIL = 12

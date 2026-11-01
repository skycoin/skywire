package com.skycoin.skywire.ui.chat

import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.combinedClickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.InsertDriveFile
import androidx.compose.material.icons.rounded.Done
import androidx.compose.material.icons.rounded.DoneAll
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.RadioButtonUnchecked
import androidx.compose.material.icons.rounded.Schedule
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontStyle
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.ChatFile
import com.skycoin.skywire.chat.ChatMessage
import com.skycoin.skywire.chat.ReplyRef
import com.skycoin.skywire.chat.humanSize
import com.skycoin.skywire.chat.isAudio
import com.skycoin.skywire.chat.isImage
import com.skycoin.skywire.chat.isVideo
import com.skycoin.skywire.chat.isVideoMessage
import com.skycoin.skywire.chat.isVoiceMessage
import com.skycoin.skywire.chat.isSquareVideoMessage
import com.skycoin.skywire.chat.requestFile
import com.skycoin.skywire.chat.download
import com.skycoin.skywire.chat.resend
import com.skycoin.skywire.chat.tickStatus
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter

private val CLOCK = DateTimeFormatter.ofPattern("HH:mm")

/** Bubble colours for one side. */
data class BubbleColors(val bg: Color, val fg: Color, val meta: Color, val accent: Color)

@Composable
fun bubbleColors(mine: Boolean): BubbleColors = if (mine) {
    BubbleColors(
        MaterialTheme.colorScheme.primary,
        MaterialTheme.colorScheme.onPrimary,
        MaterialTheme.colorScheme.onPrimary.copy(alpha = 0.72f),
        MaterialTheme.colorScheme.onPrimary,
    )
} else {
    BubbleColors(
        MaterialTheme.colorScheme.surfaceContainerHighest,
        MaterialTheme.colorScheme.onSurface,
        MaterialTheme.colorScheme.onSurfaceVariant,
        MaterialTheme.colorScheme.primary,
    )
}

@OptIn(ExperimentalFoundationApi::class)
@Composable
fun MessageRow(
    vm: ChatViewModel,
    key: String,
    m: ChatMessage,
    group: Boolean,
    highlighted: Boolean,
    onTap: () -> Unit,
    onQuote: (ReplyRef) -> Unit,
    onLongPress: () -> Unit = onTap,
) {
    val repo = vm.repo
    val mine = m.mine
    val colors = bubbleColors(mine)
    val rowBg by animateColorAsState(if (highlighted) MaterialTheme.colorScheme.primary.copy(alpha = 0.16f) else Color.Transparent, label = "hl")
    Row(
        Modifier
            .fillMaxWidth()
            .background(rowBg)
            .combinedClickable(onClick = onTap, onLongClick = onLongPress)
            .padding(horizontal = 10.dp, vertical = 2.dp),
        horizontalArrangement = if (mine) Arrangement.End else Arrangement.Start,
    ) {
        Column(horizontalAlignment = if (mine) Alignment.End else Alignment.Start, modifier = Modifier.widthIn(max = 310.dp)) {
            if (group && !mine) {
                Text(
                    repo.displayName(m.from),
                    style = MaterialTheme.typography.labelMedium,
                    color = keyColor(m.from),
                    fontWeight = FontWeight.SemiBold,
                    modifier = Modifier.padding(start = 10.dp, bottom = 2.dp),
                )
            }
            val shape = RoundedCornerShape(
                topStart = 18.dp, topEnd = 18.dp,
                bottomStart = if (mine) 18.dp else 4.dp, bottomEnd = if (mine) 4.dp else 18.dp,
            )
            val media = m.file?.let { it.isVideoMessage() || (it.isImage() && (it.url.isNotEmpty() || it.localPath.isNotEmpty())) } == true
            Column(
                Modifier
                    .clip(shape)
                    .background(if (media) Color.Transparent else colors.bg)
                    .padding(if (media) 0.dp else 10.dp),
            ) {
                if (m.reply != null) {
                    Quote(vm, m.reply, colors) { onQuote(m.reply) }
                } else if (m.forwarded) {
                    Text(
                        if (m.forwardFrom.isNotEmpty()) "↪ " + stringResource(R.string.chat_forwarded_from, m.forwardFrom) else "↪ " + stringResource(R.string.chat_forwarded),
                        style = MaterialTheme.typography.labelSmall,
                        fontStyle = FontStyle.Italic,
                        color = colors.meta,
                        modifier = Modifier.padding(bottom = 4.dp),
                    )
                }
                val f = m.file
                if (f != null) {
                    FileContent(vm, key, m, f, colors)
                    UploadProgress(vm, m, colors)
                } else {
                    SelectionContainer {
                        Text(shownText(m.text), color = colors.fg, style = MaterialTheme.typography.bodyLarge)
                    }
                }
                if (!media) Meta(vm, key, m, group, colors)
            }
            if (media) Meta(vm, key, m, group, bubbleColors(false).copy(meta = MaterialTheme.colorScheme.onSurfaceVariant))
        }
    }
}

@Composable
private fun Quote(vm: ChatViewModel, r: ReplyRef, colors: BubbleColors, onClick: () -> Unit) {
    val repo = vm.repo
    val who = when {
        r.sender == ChatMessage.ME || r.sender == repo.myPk.value -> stringResource(R.string.chat_you)
        r.sender.isNotEmpty() -> repo.displayName(r.sender)
        else -> stringResource(R.string.chat_message)
    }
    Row(
        Modifier
            .padding(bottom = 6.dp)
            .clip(RoundedCornerShape(8.dp))
            .background(colors.fg.copy(alpha = 0.10f))
            .clickable(onClick = onClick)
            .padding(end = 8.dp),
    ) {
        Box(Modifier.width(3.dp).height(36.dp).background(colors.accent))
        Column(Modifier.padding(start = 8.dp, top = 3.dp, bottom = 3.dp)) {
            Text(who, style = MaterialTheme.typography.labelMedium, color = colors.accent, fontWeight = FontWeight.SemiBold, maxLines = 1)
            Text(shownText(r.preview), style = MaterialTheme.typography.bodySmall, color = colors.fg.copy(alpha = 0.85f), maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

@Composable
private fun Meta(vm: ChatViewModel, key: String, m: ChatMessage, group: Boolean, colors: BubbleColors) {
    Row(
        Modifier.padding(top = 2.dp, start = 4.dp, end = 4.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Text(
            Instant.ofEpochMilli(m.ts).atZone(ZoneId.systemDefault()).format(CLOCK),
            style = MaterialTheme.typography.labelSmall,
            color = colors.meta,
        )
        if (m.mine && key != com.skycoin.skywire.chat.SAVED_KEY) {
            Spacer(Modifier.width(4.dp))
            Tick(vm, key, m, tickStatus(m, group), colors)
        }
    }
}

@Composable
private fun Tick(vm: ChatViewModel, key: String, m: ChatMessage, status: String, colors: BubbleColors) {
    val size = Modifier.size(14.dp)
    when (status) {
        "queued" -> Icon(Icons.Rounded.Schedule, stringResource(R.string.chat_tick_queued), size, tint = MaterialTheme.colorScheme.tertiary)
        "pending" -> Icon(Icons.Rounded.RadioButtonUnchecked, stringResource(R.string.chat_tick_pending), size, tint = colors.meta)
        "sent" -> Icon(Icons.Rounded.Done, stringResource(R.string.chat_tick_sent), size, tint = colors.meta)
        "received" -> Icon(Icons.Rounded.DoneAll, stringResource(R.string.chat_tick_received), size, tint = colors.meta)
        "read" -> Icon(Icons.Rounded.DoneAll, stringResource(R.string.chat_tick_read), size, tint = READ_TINT)
        "failed" -> {
            val f = m.file
            val retryable = f == null || java.io.File(f.localPath).isFile
            if (retryable) {
                Text(
                    "⚠ " + stringResource(if (f == null) R.string.chat_resend else R.string.chat_retry),
                    style = MaterialTheme.typography.labelSmall,
                    fontWeight = FontWeight.Bold,
                    color = MaterialTheme.colorScheme.error,
                    modifier = Modifier
                        .clip(RoundedCornerShape(6.dp))
                        .background(MaterialTheme.colorScheme.errorContainer)
                        .clickable { vm.repo.resend(key, m.key) }
                        .padding(horizontal = 6.dp, vertical = 1.dp),
                )
            } else {
                Icon(Icons.Rounded.Warning, stringResource(R.string.chat_transfer_failed), size, tint = MaterialTheme.colorScheme.error)
            }
        }
    }
}

private val READ_TINT = Color(0xFF7DF2B9)

@Composable
private fun UploadProgress(vm: ChatViewModel, m: ChatMessage, colors: BubbleColors) {
    val uploads by vm.repo.uploads.collectAsState()
    val (sent, total) = uploads[m.key] ?: return
    Column(Modifier.padding(top = 4.dp).widthIn(min = 160.dp)) {
        LinearProgressIndicator(
            progress = { if (total > 0) sent.toFloat() / total else 0f },
            modifier = Modifier.fillMaxWidth(),
            color = colors.accent,
            trackColor = colors.fg.copy(alpha = 0.2f),
        )
        Text("${humanSize(sent)} / ${humanSize(total)}", style = MaterialTheme.typography.labelSmall, color = colors.meta)
    }
}

/** Choose the renderer by name, as every skychat UI does (index.html renderFile). */
@Composable
private fun FileContent(vm: ChatViewModel, key: String, m: ChatMessage, f: ChatFile, colors: BubbleColors) {
    val hasBytes = f.url.isNotEmpty() || java.io.File(f.localPath).isFile
    when {
        hasBytes && f.isImage() -> ImageThumb(vm, f)
        hasBytes && f.isVoiceMessage() -> VoicePlayer(vm, m, f, colors, voice = true)
        hasBytes && f.isVideoMessage() -> RoundVideo(vm, m, f, square = f.isSquareVideoMessage())
        hasBytes && f.isAudio() -> VoicePlayer(vm, m, f, colors, voice = false)
        hasBytes && f.isVideo() -> VideoCard(vm, f, colors)
        else -> FileCard(vm, m, f, colors)
    }
}

@Composable
private fun FileCard(vm: ChatViewModel, m: ChatMessage, f: ChatFile, colors: BubbleColors) {
    val repo = vm.repo
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.widthIn(min = 180.dp)) {
        Box(
            Modifier.size(40.dp).clip(RoundedCornerShape(10.dp)).background(colors.fg.copy(alpha = 0.12f)),
            contentAlignment = Alignment.Center,
        ) { Icon(Icons.AutoMirrored.Rounded.InsertDriveFile, null, tint = colors.fg) }
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f, fill = false)) {
            Text(
                when {
                    f.isVoiceMessage() -> stringResource(R.string.chat_voice_message)
                    f.isVideoMessage() -> stringResource(R.string.chat_video_message)
                    else -> f.name
                },
                color = colors.fg, style = MaterialTheme.typography.bodyMedium, maxLines = 2, overflow = TextOverflow.Ellipsis,
            )
            Text(
                if (f.status == "failed") stringResource(R.string.chat_transfer_failed_lower) else humanSize(f.size),
                color = colors.meta, style = MaterialTheme.typography.labelSmall,
            )
            if (f.url.isEmpty() && f.from.isNotEmpty() && f.id.isNotEmpty()) {
                TextButton(onClick = { repo.requestFile(m) }, contentPadding = androidx.compose.foundation.layout.PaddingValues(0.dp)) {
                    Text(stringResource(R.string.chat_rerequest), color = colors.accent, style = MaterialTheme.typography.labelMedium)
                }
            }
        }
        if (f.url.startsWith("/files/")) {
            IconButton(onClick = { vm.repo.download(m) }) { Icon(Icons.Rounded.Download, stringResource(R.string.chat_download), tint = colors.fg) }
        }
    }
}

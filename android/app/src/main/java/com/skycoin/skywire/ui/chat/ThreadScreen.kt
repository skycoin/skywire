package com.skycoin.skywire.ui.chat

import android.text.format.DateFormat
import androidx.activity.compose.BackHandler
import androidx.compose.animation.animateColorAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.rememberLazyListState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.Reply
import androidx.compose.material.icons.rounded.Block
import androidx.compose.material.icons.rounded.Bookmark
import androidx.compose.material.icons.rounded.Call
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.DeleteForever
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Link
import androidx.compose.material.icons.rounded.LinkOff
import androidx.compose.material.icons.automirrored.rounded.Logout
import androidx.compose.material.icons.rounded.MoreVert
import androidx.compose.material.icons.rounded.Notifications
import androidx.compose.material.icons.rounded.NotificationsOff
import androidx.compose.material.icons.rounded.PushPin
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material.icons.rounded.Refresh
import androidx.compose.material.icons.rounded.Route
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.automirrored.rounded.Shortcut
import androidx.compose.material3.Badge
import androidx.compose.material3.BadgedBox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.derivedStateOf
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.runtime.setValue
import androidx.compose.runtime.snapshotFlow
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.ChatMessage
import com.skycoin.skywire.chat.ReplyRef
import com.skycoin.skywire.chat.SAVED_KEY
import com.skycoin.skywire.chat.abbrevPk
import com.skycoin.skywire.chat.acceptRequest
import com.skycoin.skywire.chat.blockRequest
import com.skycoin.skywire.chat.canDeleteForEveryone
import com.skycoin.skywire.chat.deleteChat
import com.skycoin.skywire.chat.deleteForEveryone
import com.skycoin.skywire.chat.deleteForMe
import com.skycoin.skywire.chat.deleteGroup
import com.skycoin.skywire.chat.download
import com.skycoin.skywire.chat.isAdmin
import com.skycoin.skywire.chat.isMuted
import com.skycoin.skywire.chat.isoMillis
import com.skycoin.skywire.chat.leaveGroup
import com.skycoin.skywire.chat.loadOlder
import com.skycoin.skywire.chat.messagePreview
import com.skycoin.skywire.chat.parseTime
import com.skycoin.skywire.chat.pinKey
import com.skycoin.skywire.chat.placeCall
import com.skycoin.skywire.chat.requestFile
import com.skycoin.skywire.chat.saveToSaved
import com.skycoin.skywire.chat.sendAnyway
import com.skycoin.skywire.chat.setBlocked
import com.skycoin.skywire.chat.toggleMute
import com.skycoin.skywire.chat.togglePair
import com.skycoin.skywire.chat.togglePin
import com.skycoin.skywire.chat.inviteLink
import com.skycoin.skywire.chat.retryLink
import com.skycoin.skywire.chat.rewatchLink
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** One entry of the reversed list: a message or the day line above a run of them. */
private sealed interface Row2 {
    val id: String

    data class Msg(val m: ChatMessage) : Row2 {
        override val id get() = m.key
    }

    data class Day(val day: LocalDate) : Row2 {
        override val id get() = "day-$day"
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ThreadScreen(vm: ChatViewModel, key: String, onBack: () -> Unit) {
    val repo = vm.repo
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val messages by repo.thread(key).collectAsState()
    val groups by repo.groups.collectAsState()
    val names by repo.names.collectAsState()
    val presence by repo.presence.collectAsState()
    val pair by repo.pair.collectAsState()
    val privacy by repo.privacy.collectAsState()
    val local by repo.store.state.collectAsState()
    val notif by repo.store.notif.collectAsState()
    val voice by repo.voiceAvailable.collectAsState()
    val hist by repo.hist.collectAsState()
    val links by repo.links.collectAsState()
    val network by repo.network.collectAsState()
    val replies by vm.replies.collectAsState()
    val avatars by repo.store.avatarsChanged.collectAsState()

    val saved = key == SAVED_KEY
    val group = groups.firstOrNull { it.id == key }
    val isGroup = !saved && repo.isGroupKey(key)
    val title = when {
        saved -> stringResource(R.string.chat_saved_messages)
        group != null -> group.name.ifEmpty { group.id }
        isGroup -> key
        else -> names[key]?.takeIf { it.isNotEmpty() } ?: abbrevPk(key)
    }
    val paired = !isGroup && !saved && key in pair.paired
    val muted = isMutedIn(notif.muted, key)
    val pinned = local.pinned == pinKey(key, isGroup)

    var menu by remember { mutableStateOf(false) }
    var routeMenu by remember { mutableStateOf(false) }
    var qr by remember { mutableStateOf<QrSpec?>(null) }
    var confirm by remember { mutableStateOf<Pair<String, () -> Unit>?>(null) }
    var sheetFor by remember { mutableStateOf<ChatMessage?>(null) }
    // Message keys picked by a long press; while any are, a tap picks too.
    var selected by remember(key) { mutableStateOf(emptySet<String>()) }
    val toggle = { m: ChatMessage -> selected = if (m.key in selected) selected - m.key else selected + m.key }
    BackHandler(enabled = selected.isNotEmpty()) { selected = emptySet() }
    var highlight by remember { mutableStateOf<String?>(null) }

    val rows = remember(messages) { buildRows(messages) }
    val listState = rememberLazyListState()
    val atBottom by remember { derivedStateOf { listState.firstVisibleItemIndex == 0 && listState.firstVisibleItemScrollOffset < 80 } }
    LaunchedEffect(atBottom) { repo.setAtBottom(key, atBottom) }

    // New messages follow the reader down only while they are at the bottom (or it is their own).
    var lastNewest by remember { mutableStateOf(messages.lastOrNull()?.key) }
    LaunchedEffect(messages.lastOrNull()?.key) {
        val newest = messages.lastOrNull() ?: return@LaunchedEffect
        if (newest.key != lastNewest) {
            lastNewest = newest.key
            if (atBottom || newest.mine) listState.animateScrollToItem(0)
            if (atBottom) repo.markRead(key)
        }
    }

    // Older group backlog when the top comes into view.
    val rowCount by rememberUpdatedState(rows.size)
    LaunchedEffect(listState, isGroup) {
        if (!isGroup) return@LaunchedEffect
        snapshotFlow { listState.layoutInfo.visibleItemsInfo.lastOrNull()?.index ?: 0 }.collect { last ->
            if (last >= rowCount - 3) repo.loadOlder(key)
        }
    }

    fun scrollToTs(ts: Long) {
        val i = rows.indexOfFirst { it is Row2.Msg && kotlin.math.abs(it.m.ts - ts) < 1000 }
        if (i < 0) return
        val k = (rows[i] as Row2.Msg).m.key
        scope.launch {
            listState.animateScrollToItem(i)
            highlight = k
            delay(1600)
            if (highlight == k) highlight = null
        }
    }

    val target by vm.scrollTarget.collectAsState()
    LaunchedEffect(target, rows.size) {
        val t = target ?: return@LaunchedEffect
        if (t.first != key) return@LaunchedEffect
        repeat(20) {
            val i = rows.indexOfFirst { it is Row2.Msg && kotlin.math.abs(it.m.ts - t.second) < 2000 }
            if (i >= 0) {
                vm.scrollTarget.value = null
                scrollToTs((rows[i] as Row2.Msg).m.ts)
                return@LaunchedEffect
            }
            delay(150)
        }
        vm.scrollTarget.value = null
    }

    Scaffold(
        contentWindowInsets = WindowInsets(0),
        topBar = topBar@{
            if (selected.isNotEmpty()) {
                Row(
                    Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).padding(horizontal = 4.dp, vertical = 6.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    IconButton(onClick = { selected = emptySet() }) { Icon(Icons.Rounded.Close, stringResource(R.string.chat_clear_selection)) }
                    Text(
                        stringResource(R.string.chat_selected_count, selected.size),
                        style = MaterialTheme.typography.titleMedium,
                        fontWeight = FontWeight.SemiBold,
                        modifier = Modifier.weight(1f).padding(start = 8.dp),
                    )
                    IconButton(onClick = {
                        confirm = context.getString(R.string.chat_confirm_delete_selected) to {
                            messages.filter { it.key in selected }.forEach { repo.deleteForMe(key, it) }
                            selected = emptySet()
                        }
                    }) { Icon(Icons.Rounded.Delete, stringResource(R.string.chat_delete_selected), tint = MaterialTheme.colorScheme.error) }
                }
                return@topBar
            }
            Row(
                Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surface).padding(horizontal = 4.dp, vertical = 6.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                IconButton(onClick = onBack) { Icon(Icons.AutoMirrored.Rounded.ArrowBack, stringResource(R.string.chat_back_to_chats)) }
                Row(
                    Modifier.weight(1f).clip(RoundedCornerShape(12.dp)).clickable {
                        when {
                            saved -> Unit
                            isGroup -> vm.push(ChatPage.GroupSettings(key))
                            else -> vm.push(ChatPage.ContactSettings(key))
                        }
                    }.padding(4.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    when {
                        saved -> ChatAvatar(SAVED_KEY, "", 40.dp, AvatarKind.SAVED)
                        isGroup -> ChatAvatar(
                            key, title, 40.dp, if (group?.isChannel == true) AvatarKind.CHANNEL else AvatarKind.GROUP,
                            version = group?.avatar.hashCode(),
                            load = group?.avatar?.takeIf { it.isNotEmpty() }?.let { a -> { com.skycoin.skywire.chat.decodeDataUrl(a) } },
                        )
                        else -> ChatAvatar(key, title, 40.dp, version = avatars, online = presence[key]?.online, load = { repo.store.avatar(key) })
                    }
                    Spacer(Modifier.width(10.dp))
                    Column {
                        Text(title, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        val sub = when {
                            saved -> stringResource(R.string.chat_saved_sub)
                            group != null -> groupSubtitle(group, repo.myPk.value)
                            isGroup -> ""
                            else -> listOfNotNull(
                                presence[key]?.let { p ->
                                    if (!p.online) stringResource(R.string.chat_presence_offline)
                                    else if (p.rttMs > 0) stringResource(R.string.chat_presence_rtt, p.rttMs)
                                    else stringResource(R.string.chat_presence_online)
                                },
                                if (paired) epochTag(pair.records[key]) else null,
                            ).joinToString(" · ")
                        }
                        if (sub.isNotEmpty()) {
                            Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
                if (!saved && !isGroup && voice) {
                    IconButton(onClick = { repo.placeCall(key) }) { Icon(Icons.Rounded.Call, stringResource(R.string.chat_call)) }
                }
                Box {
                    IconButton(onClick = { menu = true }) {
                        val pending = group?.pendingJoins ?: 0
                        if (pending > 0) {
                            BadgedBox(badge = { Badge { Text(pending.toString()) } }) { Icon(Icons.Rounded.MoreVert, stringResource(R.string.chat_actions)) }
                        } else {
                            Icon(
                                Icons.Rounded.MoreVert, stringResource(R.string.chat_actions),
                                tint = if (muted) MaterialTheme.colorScheme.tertiary else MaterialTheme.colorScheme.onSurface,
                            )
                        }
                    }
                    DropdownMenu(expanded = menu, onDismissRequest = { menu = false }) {
                        fun item(icon: ImageVector, label: Int, danger: Boolean = false, act: () -> Unit) = @Composable {
                            DropdownMenuItem(
                                leadingIcon = { Icon(icon, null, tint = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant) },
                                text = { Text(stringResource(label), color = if (danger) MaterialTheme.colorScheme.error else Color.Unspecified) },
                                onClick = {
                                    menu = false
                                    act()
                                },
                            )
                        }
                        when {
                            saved -> item(Icons.Rounded.DeleteForever, R.string.chat_clear_notes, danger = true) {
                                confirm = context.getString(R.string.chat_confirm_clear_notes) to { repo.deleteChat(SAVED_KEY) }
                            }()
                            isGroup -> {
                                val channel = group?.isChannel == true
                                item(Icons.Rounded.PushPin, if (pinned) R.string.chat_unpin else R.string.chat_pin) { repo.togglePin(key) }()
                                item(if (muted) Icons.Rounded.Notifications else Icons.Rounded.NotificationsOff, if (muted) R.string.chat_unmute else R.string.chat_mute) { repo.toggleMute(key) }()
                                item(Icons.Rounded.Settings, R.string.chat_settings) { vm.push(ChatPage.GroupSettings(key)) }()
                                item(Icons.Rounded.Link, R.string.chat_invite_link) {
                                    scope.launch {
                                        repo.inviteLink(key)?.let {
                                            copyText(context, it)
                                            repo.note(R.string.chat_invite_copied)
                                        }
                                    }
                                }()
                                if (group != null) {
                                    item(Icons.Rounded.QrCode2, R.string.chat_qr_code) {
                                        qr = QrSpec(
                                            "skychat://${group.ownerPk.lowercase()}/${group.id}",
                                            R.string.chat_qr_title_group, if (channel) R.string.chat_qr_help_channel else R.string.chat_qr_help_group,
                                            titleArg = title, inviteGid = if (repo.isAdmin(group)) group.id else null,
                                        )
                                    }()
                                }
                                HorizontalDivider()
                                item(Icons.AutoMirrored.Rounded.Logout, if (channel) R.string.chat_leave_channel else R.string.chat_leave_group) {
                                    confirm = context.getString(if (channel) R.string.chat_confirm_leave_channel else R.string.chat_confirm_leave_group) to {
                                        repo.leaveGroup(key) { vm.home() }
                                    }
                                }()
                                item(Icons.Rounded.DeleteForever, if (channel) R.string.chat_delete_channel else R.string.chat_delete_group, danger = true) {
                                    confirm = context.getString(if (channel) R.string.chat_confirm_delete_channel else R.string.chat_confirm_delete_group) to {
                                        repo.deleteGroup(key) { vm.home() }
                                    }
                                }()
                            }
                            else -> {
                                if (voice) item(Icons.Rounded.Call, R.string.chat_call) { repo.placeCall(key) }()
                                item(Icons.Rounded.PushPin, if (pinned) R.string.chat_unpin else R.string.chat_pin) { repo.togglePin(key) }()
                                item(if (muted) Icons.Rounded.Notifications else Icons.Rounded.NotificationsOff, if (muted) R.string.chat_unmute else R.string.chat_mute) { repo.toggleMute(key) }()
                                if (pair.available) {
                                    item(if (paired) Icons.Rounded.LinkOff else Icons.Rounded.Link, if (paired) R.string.chat_unpair else R.string.chat_pair) {
                                        if (paired) {
                                            confirm = context.getString(R.string.chat_confirm_unpair) to { repo.togglePair(key) }
                                        } else {
                                            repo.togglePair(key)
                                        }
                                    }()
                                }
                                item(Icons.Rounded.Route, R.string.chat_network) { routeMenu = true }()
                                item(Icons.Rounded.QrCode2, R.string.chat_qr_code) {
                                    qr = QrSpec("skychat://$key", R.string.chat_contact_address, R.string.chat_qr_help_me)
                                }()
                                item(Icons.Rounded.Settings, R.string.chat_settings) { vm.push(ChatPage.ContactSettings(key)) }()
                                if (privacy != null) {
                                    val blocked = key in privacy!!.blocked
                                    item(Icons.Rounded.Block, if (blocked) R.string.chat_unblock else R.string.chat_block) { repo.setBlocked(key, !blocked) }()
                                }
                                HorizontalDivider()
                                item(Icons.Rounded.DeleteForever, R.string.chat_delete_chat, danger = true) {
                                    confirm = context.getString(R.string.chat_confirm_delete_chat) to {
                                        repo.deleteChat(key)
                                        vm.home()
                                    }
                                }()
                            }
                        }
                    }
                    DropdownMenu(expanded = routeMenu, onDismissRequest = { routeMenu = false }) {
                        val options = buildList {
                            add("skynet" to "Skynet")
                            add("dmsg" to "DMSG")
                            if (paired) add("cxo" to stringResource(R.string.chat_network_cxo))
                        }
                        for ((value, label) in options) {
                            DropdownMenuItem(
                                leadingIcon = { RadioButton(selected = network == value, onClick = null) },
                                text = { Text(label) },
                                onClick = {
                                    routeMenu = false
                                    repo.network.value = value
                                    repo.rewatchLink(key)
                                },
                            )
                        }
                    }
                }
            }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            Box(Modifier.weight(1f).fillMaxWidth()) {
                LazyColumn(
                    state = listState,
                    reverseLayout = true,
                    modifier = Modifier.fillMaxSize(),
                    verticalArrangement = Arrangement.spacedBy(2.dp),
                ) {
                    items(rows, key = { it.id }) { row ->
                        when (row) {
                            is Row2.Day -> DayDivider(row.day)
                            is Row2.Msg -> MessageRow(
                                vm = vm,
                                key = key,
                                m = row.m,
                                group = isGroup,
                                highlighted = highlight == row.m.key || row.m.key in selected,
                                onTap = { if (selected.isNotEmpty()) toggle(row.m) else sheetFor = row.m },
                                onLongPress = { toggle(row.m) },
                                onQuote = { r -> parseTime(r.ts)?.let { scrollToTs(it) } },
                            )
                        }
                    }
                    if (isGroup && hist[key]?.loading == true) {
                        item(key = "loading") {
                            Text(
                                stringResource(R.string.chat_loading_earlier),
                                style = MaterialTheme.typography.bodySmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                                modifier = Modifier.fillMaxWidth().padding(12.dp),
                            )
                        }
                    }
                }
            }
            if (!saved && !isGroup && network != "cxo") {
                LinkBar(vm, key, title, links[key], messages.count { it.status == "queued" })
            }
            if (!saved && !isGroup && privacy?.requests?.contains(key) == true) {
                RequestBar(
                    onBlock = { repo.blockRequest(key) { vm.home() } },
                    onAccept = { repo.acceptRequest(key) },
                )
            }
            replies[key]?.let { r ->
                ReplyChip(
                    who = if (r.sender == ChatMessage.ME || r.sender == repo.myPk.value) stringResource(R.string.chat_you) else repo.displayName(r.sender),
                    preview = shownText(r.preview),
                    onCancel = { vm.setReply(key, null) },
                )
            }
            if (group != null && !group.postable) {
                Text(
                    cannotPostRes(group.cannotPostCode)?.let { stringResource(it) }
                        ?: group.cannotPostReason.ifEmpty { stringResource(R.string.chat_cannot_post) },
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer).padding(16.dp),
                )
            } else {
                Composer(vm, key)
            }
        }
    }

    sheetFor?.let { m ->
        ModalBottomSheet(onDismissRequest = { sheetFor = null }) {
            MessageSheet(vm, key, m, isGroup) { sheetFor = null }
        }
    }
    qr?.let { QrDialog(it, vm) { qr = null } }
    confirm?.let { (text, act) ->
        ConfirmDialog(text, stringResource(R.string.chat_ok), onConfirm = act, onDismiss = { confirm = null })
    }
}

private fun buildRows(messages: List<ChatMessage>): List<Row2> {
    val zone = ZoneId.systemDefault()
    val out = ArrayList<Row2>(messages.size + 8)
    for (i in messages.indices.reversed()) {
        val m = messages[i]
        out += Row2.Msg(m)
        val day = Instant.ofEpochMilli(m.ts).atZone(zone).toLocalDate()
        val prev = messages.getOrNull(i - 1)?.let { Instant.ofEpochMilli(it.ts).atZone(zone).toLocalDate() }
        if (prev != day) out += Row2.Day(day)
    }
    return out
}

@Composable
private fun DayDivider(day: LocalDate) {
    val locale = LocalConfiguration.current.locales[0]
    Box(Modifier.fillMaxWidth().padding(vertical = 8.dp), contentAlignment = Alignment.Center) {
        Text(
            day.format(DateTimeFormatter.ofPattern(DateFormat.getBestDateTimePattern(locale, "EEEMMMd"), locale)),
            style = MaterialTheme.typography.labelMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.clip(RoundedCornerShape(12.dp)).background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(horizontal = 10.dp, vertical = 3.dp),
        )
    }
}

@Composable
private fun epochTag(record: com.skycoin.skywire.chat.PairRecord?): String? {
    record ?: return null
    return if (record.forwardSecret && record.epoch.isNotEmpty()) {
        stringResource(R.string.chat_epoch_secret, record.epoch.take(8))
    } else {
        stringResource(R.string.chat_epoch_pending)
    }
}

@Composable
private fun LinkBar(vm: ChatViewModel, pk: String, name: String, state: com.skycoin.skywire.chat.LinkState?, queued: Int) {
    if (state == null || state.ready) {
        if (queued == 0) return
    }
    val error = state?.error?.isNotEmpty() == true && state.ready != true
    Row(
        Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(horizontal = 16.dp, vertical = 8.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (!error) {
            CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
            Spacer(Modifier.width(10.dp))
        }
        Column(Modifier.weight(1f)) {
            Text(
                if (error) stringResource(R.string.chat_link_no_route, name) else stringResource(R.string.chat_link_connecting, name),
                style = MaterialTheme.typography.bodySmall,
            )
            if (queued > 0) {
                Text(
                    stringResource(if (queued == 1) R.string.chat_link_waiting_one else R.string.chat_link_waiting_many, queued),
                    style = MaterialTheme.typography.labelSmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
        if (error) {
            TextButton(onClick = { vm.repo.retryLink(pk) }) { Text(stringResource(R.string.chat_retry)) }
            if (queued > 0) TextButton(onClick = { vm.repo.sendAnyway(pk) }) { Text(stringResource(R.string.chat_send_anyway)) }
        }
    }
}

@Composable
private fun RequestBar(onBlock: () -> Unit, onAccept: () -> Unit) {
    Column(Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(horizontal = 16.dp, vertical = 8.dp)) {
        Text(stringResource(R.string.chat_request_bar), style = MaterialTheme.typography.bodySmall)
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.End) {
            TextButton(onClick = onBlock) { Text(stringResource(R.string.chat_block), color = MaterialTheme.colorScheme.error) }
            TextButton(onClick = onAccept) { Text(stringResource(R.string.chat_accept)) }
        }
    }
}

@Composable
private fun ReplyChip(who: String, preview: String, onCancel: () -> Unit) {
    Row(
        Modifier.fillMaxWidth().background(MaterialTheme.colorScheme.surfaceContainer).padding(start = 16.dp, end = 4.dp, top = 6.dp, bottom = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Box(Modifier.width(3.dp).height(32.dp).background(MaterialTheme.colorScheme.primary))
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Text(stringResource(R.string.chat_replying_to, who), style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.primary)
            Text(preview, style = MaterialTheme.typography.bodySmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
        IconButton(onClick = onCancel) { Icon(Icons.Rounded.Close, stringResource(R.string.chat_cancel)) }
    }
}

/** The touch sheet (index.html openMsgSheet), with the desktop ⋯ menu's file actions folded in. */
@Composable
private fun MessageSheet(vm: ChatViewModel, key: String, m: ChatMessage, group: Boolean, close: () -> Unit) {
    val repo = vm.repo
    val context = LocalContext.current
    var confirmAll by remember { mutableStateOf(false) }
    Column(Modifier.fillMaxWidth().padding(bottom = 24.dp)) {
        Text(
            shownText(m.text.ifEmpty { m.file?.name.orEmpty() }).ifEmpty { stringResource(R.string.chat_message) },
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            maxLines = 2,
            overflow = TextOverflow.Ellipsis,
            modifier = Modifier.padding(horizontal = 24.dp, vertical = 8.dp),
        )
        @Composable
        fun action(icon: ImageVector, label: Int, danger: Boolean = false, act: () -> Unit) {
            Row(
                Modifier.fillMaxWidth().clickable {
                    close()
                    act()
                }.padding(horizontal = 24.dp, vertical = 14.dp),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                Icon(icon, null, tint = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
                Spacer(Modifier.width(20.dp))
                Text(stringResource(label), color = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurface)
            }
        }
        if (m.status != "queued") {
            action(Icons.AutoMirrored.Rounded.Reply, R.string.chat_reply) {
                vm.setReply(key, ReplyRef(if (m.mine) ChatMessage.ME else m.from, isoMillis(m.ts), messagePreview(m)))
            }
        }
        if (m.text.isNotEmpty() && m.file == null) {
            action(Icons.Rounded.ContentCopy, R.string.chat_copy) {
                copyText(context, m.text)
                repo.note(R.string.chat_copied)
            }
        }
        action(Icons.AutoMirrored.Rounded.Shortcut, R.string.chat_forward) { vm.push(ChatPage.Forward(key, m.key)) }
        if (key != SAVED_KEY) action(Icons.Rounded.Bookmark, R.string.chat_save_to_saved) { repo.saveToSaved(m) }
        m.file?.let { f ->
            if (f.url.startsWith("/files/")) action(Icons.Rounded.Download, R.string.chat_download) { repo.download(m) }
            if (f.from.isNotEmpty() && f.id.isNotEmpty()) action(Icons.Rounded.Refresh, R.string.chat_rerequest) { repo.requestFile(m) }
        }
        action(Icons.Rounded.Delete, R.string.chat_delete_for_me, danger = true) { repo.deleteForMe(key, m) }
        if (repo.canDeleteForEveryone(key, m)) {
            action(Icons.Rounded.DeleteForever, R.string.chat_delete_for_all, danger = true) { repo.deleteForEveryone(key, m) }
        }
    }
}

/** The visor's reason a group refuses this phone's posts, in the app's language (pkg/skychat/group PostBlock*). */
internal fun cannotPostRes(code: String): Int? = when (code) {
    "banned" -> R.string.chat_cannot_post_banned
    "removed" -> R.string.chat_cannot_post_removed
    "muted" -> R.string.chat_cannot_post_muted
    "channel" -> R.string.chat_cannot_post_channel
    "read_only" -> R.string.chat_cannot_post_read_only
    else -> null
}

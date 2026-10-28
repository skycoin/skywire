package com.skycoin.skywire.ui.chat

import androidx.compose.foundation.ExperimentalFoundationApi
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.horizontalScroll
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.CallMade
import androidx.compose.material.icons.automirrored.rounded.CallReceived
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.Call
import androidx.compose.material.icons.rounded.Campaign
import androidx.compose.material.icons.rounded.Check
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Contacts
import androidx.compose.material.icons.rounded.GroupAdd
import androidx.compose.material.icons.rounded.MoreVert
import androidx.compose.material.icons.rounded.NotificationsOff
import androidx.compose.material.icons.rounded.PersonAdd
import androidx.compose.material.icons.rounded.PushPin
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material.icons.rounded.Search
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.ChevronRight
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FloatingActionButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.CallRecord
import com.skycoin.skywire.chat.ChatMessage
import com.skycoin.skywire.chat.GroupInfo
import com.skycoin.skywire.chat.SAVED_KEY
import com.skycoin.skywire.chat.ThreadSummary
import com.skycoin.skywire.chat.abbrevPk
import com.skycoin.skywire.chat.acceptInvite
import com.skycoin.skywire.chat.callLog
import com.skycoin.skywire.chat.declineInvite
import com.skycoin.skywire.chat.myAddress
import com.skycoin.skywire.chat.parseTime
import com.skycoin.skywire.chat.placeCall
import com.skycoin.skywire.chat.setTab
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

private val TABS = listOf("all", "dm", "groups", "channels", "calls")

/** The conversation list: the "me" row, the tabs, and every section the page had. */
@Composable
fun ChatHome(vm: ChatViewModel, onBack: () -> Unit) {
    val repo = vm.repo
    val local by repo.store.state.collectAsState()
    val summaries by repo.summaries.collectAsState()
    val groups by repo.groups.collectAsState()
    val names by repo.names.collectAsState()
    val pair by repo.pair.collectAsState()
    val privacy by repo.privacy.collectAsState()
    val presence by repo.presence.collectAsState()
    val profile by repo.profile.collectAsState()
    val myPk by repo.myPk.collectAsState()
    val sseUp by repo.sseUp.collectAsState()
    val notif by repo.store.notif.collectAsState()
    val avatars by repo.store.avatarsChanged.collectAsState()
    val tab = local.tab.takeIf { it in TABS } ?: "all"
    var startMenu by remember { mutableStateOf(false) }
    var more by remember { mutableStateOf(false) }
    var qr by remember { mutableStateOf<QrSpec?>(null) }

    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.app_skychat),
                subtitle = stringResource(if (sseUp) R.string.chat_connected else R.string.chat_reconnecting),
                onBack = onBack,
                help = HelpTopic(R.string.help_chat_title, R.string.help_chat_body),
                actions = {
                    IconButton(onClick = { vm.push(ChatPage.Search) }) {
                        Icon(Icons.Rounded.Search, stringResource(R.string.chat_search))
                    }
                    Box {
                        IconButton(onClick = { more = true }) {
                            Icon(Icons.Rounded.MoreVert, stringResource(R.string.chat_actions))
                        }
                        DropdownMenu(expanded = more, onDismissRequest = { more = false }) {
                            DropdownMenuItem(
                                leadingIcon = { Icon(Icons.Rounded.QrCode2, null) },
                                text = { Text(stringResource(R.string.chat_your_address)) },
                                onClick = {
                                    more = false
                                    val address = repo.myAddress()
                                    if (address.isNotEmpty()) qr = QrSpec(address, R.string.chat_your_address, R.string.chat_qr_help_me)
                                },
                            )
                            DropdownMenuItem(
                                leadingIcon = { Icon(Icons.Rounded.Contacts, null) },
                                text = { Text(stringResource(R.string.chat_address_book)) },
                                onClick = {
                                    more = false
                                    vm.push(ChatPage.Contacts(pick = false))
                                },
                            )
                            DropdownMenuItem(
                                leadingIcon = { Icon(Icons.Rounded.Settings, null) },
                                text = { Text(stringResource(R.string.chat_settings)) },
                                onClick = {
                                    more = false
                                    vm.push(ChatPage.Settings)
                                },
                            )
                        }
                    }
                },
            )
        },
        floatingActionButton = {
            Box {
                FloatingActionButton(onClick = { startMenu = true }) {
                    Icon(Icons.Rounded.Add, stringResource(R.string.chat_start_menu))
                }
                DropdownMenu(expanded = startMenu, onDismissRequest = { startMenu = false }) {
                    StartItem(Icons.Rounded.PersonAdd, R.string.chat_add_by_address, R.string.chat_add_by_address_sub) {
                        startMenu = false
                        vm.push(ChatPage.AddAddress("", fromLink = false))
                    }
                    StartItem(Icons.Rounded.Contacts, R.string.chat_pick_contact, R.string.chat_pick_contact_sub) {
                        startMenu = false
                        vm.push(ChatPage.Contacts(pick = true))
                    }
                    StartItem(Icons.Rounded.GroupAdd, R.string.chat_new_group, R.string.chat_new_group_sub) {
                        startMenu = false
                        vm.push(ChatPage.NewGroup(channel = false))
                    }
                    StartItem(Icons.Rounded.Campaign, R.string.chat_new_channel, R.string.chat_new_channel_sub) {
                        startMenu = false
                        vm.push(ChatPage.NewGroup(channel = true))
                    }
                }
            }
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            MeRow(
                name = profile?.name.orEmpty(),
                pk = myPk,
                avatar = profile?.avatar.orEmpty(),
                onClick = { vm.push(ChatPage.MyProfile) },
            )
            Row(
                Modifier.fillMaxWidth().horizontalScroll(rememberScrollState()).padding(horizontal = 12.dp, vertical = 4.dp),
                horizontalArrangement = Arrangement.spacedBy(6.dp),
            ) {
                for (t in TABS) {
                    FilterChip(
                        selected = tab == t,
                        onClick = { repo.setTab(t) },
                        label = { Text(stringResource(tabLabel(t)), maxLines = 1) },
                    )
                }
            }
            if (tab == "calls") {
                CallsList(vm)
                return@Column
            }
            val requests = privacy?.requests.orEmpty().toSet()
            val pinnedKey = local.pinned
            fun fits(group: GroupInfo?): Boolean = when (tab) {
                "all" -> true
                "dm" -> group == null
                "groups" -> group != null && !group.isChannel
                "channels" -> group?.isChannel == true
                else -> false
            }
            val dmKeys = local.recipients.filter { it != myPk }
            val sortedDms = dmKeys.sortedByDescending { summaries[it]?.last?.ts ?: 0L }
            val pinnedGroup = pinnedKey?.removePrefix("grp:")?.takeIf { pinnedKey.startsWith("grp:") }?.let { id -> groups.firstOrNull { it.id == id } }
            val pinnedDm = pinnedKey?.removePrefix("dm:")?.takeIf { pinnedKey.startsWith("dm:") }
            val showPinned = when {
                pinnedGroup != null -> fits(pinnedGroup)
                pinnedDm != null -> tab == "all" || tab == "dm"
                else -> false
            }
            val visibleGroups = groups.filter { !it.isChannel }.sortedByDescending { summaries[it.id]?.last?.ts ?: 0L }
            val visibleChannels = groups.filter { it.isChannel }.sortedByDescending { summaries[it.id]?.last?.ts ?: 0L }
            val dmTabs = tab == "all" || tab == "dm"
            val nothing = dmKeys.isEmpty() && groups.isEmpty() && summaries[SAVED_KEY]?.last == null

            LazyColumn(Modifier.fillMaxSize(), contentPadding = PaddingValues(bottom = 96.dp)) {
                if (dmTabs) {
                    item(key = "saved") {
                        val s = summaries[SAVED_KEY]
                        ConversationRow(
                            title = stringResource(R.string.chat_saved_messages),
                            preview = s?.last?.let { previewText(it, own = false) } ?: stringResource(R.string.chat_saved_sub),
                            time = s?.last?.ts,
                            avatar = { ChatAvatar(SAVED_KEY, "", 48.dp, AvatarKind.SAVED) },
                            onClick = { vm.openThread(SAVED_KEY) },
                        )
                    }
                }
                if (showPinned) {
                    section(R.string.chat_pinned)
                    if (pinnedGroup != null) {
                        item(key = "pin-g") {
                            GroupRow(vm, pinnedGroup, summaries[pinnedGroup.id], muted = isMutedIn(notif.muted, pinnedGroup.id), pinned = true)
                        }
                    } else if (pinnedDm != null) {
                        item(key = "pin-d") {
                            DmRow(vm, pinnedDm, summaries[pinnedDm], names, presence[pinnedDm]?.online, pinned = true, paired = pinnedDm in pair.paired, muted = isMutedIn(notif.muted, pinnedDm), avatars = avatars)
                        }
                    }
                }
                if (dmTabs && pair.invites.isNotEmpty()) {
                    section(R.string.chat_pair_invites)
                    items(pair.invites, key = { "inv-$it" }) { pk ->
                        InviteRow(pk, onAccept = { repo.acceptInvite(pk) }, onDecline = { repo.declineInvite(pk) })
                    }
                }
                if (tab == "all" || tab == "groups") {
                    val list = visibleGroups.filter { pinnedGroup?.id != it.id }
                    if (list.isNotEmpty()) {
                        section(R.string.chat_groups)
                        items(list, key = { "g-" + it.id }) { g -> GroupRow(vm, g, summaries[g.id], muted = isMutedIn(notif.muted, g.id)) }
                    }
                }
                if (tab == "all" || tab == "channels") {
                    val list = visibleChannels.filter { pinnedGroup?.id != it.id }
                    if (list.isNotEmpty()) {
                        section(R.string.chat_channels)
                        items(list, key = { "c-" + it.id }) { g -> GroupRow(vm, g, summaries[g.id], muted = isMutedIn(notif.muted, g.id)) }
                    }
                }
                if (dmTabs) {
                    val req = sortedDms.filter { it in requests }
                    if (req.isNotEmpty()) {
                        section(R.string.chat_requests)
                        items(req, key = { "r-$it" }) { pk ->
                            DmRow(vm, pk, summaries[pk], names, presence[pk]?.online, paired = pk in pair.paired, muted = isMutedIn(notif.muted, pk), avatars = avatars)
                        }
                    }
                    val chats = sortedDms.filter { it !in requests && it != pinnedDm }
                    if (chats.isNotEmpty()) section(R.string.chat_chats)
                    items(chats, key = { "d-$it" }) { pk ->
                        DmRow(vm, pk, summaries[pk], names, presence[pk]?.online, paired = pk in pair.paired, muted = isMutedIn(notif.muted, pk), avatars = avatars)
                    }
                }
                if (nothing) {
                    item(key = "welcome") {
                        Column(Modifier.fillMaxWidth().padding(32.dp), horizontalAlignment = Alignment.CenterHorizontally) {
                            Text(stringResource(R.string.chat_welcome), style = MaterialTheme.typography.titleMedium)
                            Spacer(Modifier.height(8.dp))
                            Text(
                                stringResource(R.string.chat_welcome_body),
                                style = MaterialTheme.typography.bodyMedium,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                }
            }
        }
    }
    qr?.let { spec -> QrDialog(spec, vm, onDismiss = { qr = null }) }
}

private fun tabLabel(tab: String): Int = when (tab) {
    "dm" -> R.string.chat_tab_dms
    "groups" -> R.string.chat_groups
    "channels" -> R.string.chat_channels
    "calls" -> R.string.chat_tab_calls
    else -> R.string.chat_tab_all
}

private fun LazyListScope.section(title: Int) {
    item(key = "s-$title") {
        Text(
            stringResource(title),
            style = MaterialTheme.typography.labelLarge,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(start = 20.dp, top = 14.dp, bottom = 4.dp),
        )
    }
}

@Composable
private fun StartItem(icon: ImageVector, title: Int, sub: Int, onClick: () -> Unit) {
    DropdownMenuItem(
        leadingIcon = { Icon(icon, null) },
        text = {
            Column {
                Text(stringResource(title), fontWeight = FontWeight.SemiBold)
                Text(stringResource(sub), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        onClick = onClick,
    )
}

@Composable
private fun MeRow(name: String, pk: String, avatar: String, onClick: () -> Unit) {
    Row(
        Modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 4.dp)
            .clip(RoundedCornerShape(16.dp))
            .background(MaterialTheme.colorScheme.surfaceContainerHigh)
            .clickable(onClick = onClick)
            .padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        ChatAvatar(pk.ifEmpty { "me" }, name, 40.dp, version = avatar.hashCode(), load = avatar.takeIf { it.isNotEmpty() }?.let { a -> { com.skycoin.skywire.chat.decodeDataUrl(a) } })
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(
                name.ifEmpty { stringResource(if (pk.isEmpty()) R.string.chat_your_profile else R.string.chat_set_your_name) },
                style = MaterialTheme.typography.titleSmall,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            Text(
                if (pk.isEmpty()) "—" else abbrevPk(pk),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        Icon(Icons.Rounded.ChevronRight, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

/** One row: avatar, title, preview, time and badges. */
@Composable
fun ConversationRow(
    title: String,
    preview: String,
    time: Long?,
    avatar: @Composable () -> Unit,
    onClick: () -> Unit,
    unread: Int = 0,
    muted: Boolean = false,
    pinned: Boolean = false,
    badge: String? = null,
    pending: Int = 0,
) {
    Row(
        Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 10.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        avatar()
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                if (pinned) {
                    Icon(Icons.Rounded.PushPin, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                    Spacer(Modifier.width(4.dp))
                }
                Text(
                    title,
                    style = MaterialTheme.typography.titleSmall,
                    fontWeight = FontWeight.SemiBold,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (badge != null) {
                    Spacer(Modifier.width(6.dp))
                    Text(
                        badge,
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onPrimaryContainer,
                        modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(MaterialTheme.colorScheme.primaryContainer).padding(horizontal = 5.dp, vertical = 1.dp),
                    )
                }
                if (muted) {
                    Spacer(Modifier.width(4.dp))
                    Icon(Icons.Rounded.NotificationsOff, null, Modifier.size(14.dp), tint = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                Spacer(Modifier.weight(1f))
                if (time != null && time > 0) {
                    Text(listTime(time), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            Spacer(Modifier.height(2.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    preview,
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                if (pending > 0) CountBadge(pending, MaterialTheme.colorScheme.tertiary)
                if (unread > 0) CountBadge(unread, if (muted) MaterialTheme.colorScheme.outline else MaterialTheme.colorScheme.primary)
            }
        }
    }
}

@Composable
private fun CountBadge(n: Int, color: Color) {
    Spacer(Modifier.width(6.dp))
    Box(
        Modifier.clip(CircleShape).background(color).padding(horizontal = 7.dp, vertical = 2.dp),
        contentAlignment = Alignment.Center,
    ) {
        Text(if (n > 999) "999+" else n.toString(), style = MaterialTheme.typography.labelSmall, color = Color.White)
    }
}

@Composable
private fun DmRow(
    vm: ChatViewModel,
    pk: String,
    summary: ThreadSummary?,
    names: Map<String, String>,
    online: Boolean?,
    pinned: Boolean = false,
    paired: Boolean = false,
    muted: Boolean = false,
    avatars: Int = 0,
) {
    val repo = vm.repo
    val label = names[pk]?.takeIf { it.isNotEmpty() } ?: abbrevPk(pk)
    ConversationRow(
        title = label,
        preview = summary?.last?.let { previewText(it, own = true) } ?: stringResource(if (pinned) R.string.chat_pinned else R.string.chat_new_conversation),
        time = summary?.last?.ts,
        avatar = { ChatAvatar(pk, label, 48.dp, version = avatars, online = online, load = { repo.store.avatar(pk) }) },
        onClick = { vm.openThread(pk) },
        unread = summary?.unread ?: 0,
        muted = muted,
        pinned = pinned,
        badge = if (paired) "CXO" else null,
    )
}

@Composable
private fun GroupRow(vm: ChatViewModel, g: GroupInfo, summary: ThreadSummary?, muted: Boolean, pinned: Boolean = false) {
    val sub = groupSubtitle(g, vm.repo.myPk.value)
    ConversationRow(
        title = g.name.ifEmpty { g.id },
        preview = summary?.last?.let { previewText(it, own = true, group = true, sender = vm.repo.displayName(it.from)) } ?: sub,
        time = summary?.last?.ts,
        avatar = {
            ChatAvatar(
                g.id, g.name, 48.dp, if (g.isChannel) AvatarKind.CHANNEL else AvatarKind.GROUP,
                version = g.avatar.hashCode(),
                load = g.avatar.takeIf { it.isNotEmpty() }?.let { a -> { com.skycoin.skywire.chat.decodeDataUrl(a) } },
            )
        },
        onClick = { vm.openThread(g.id) },
        unread = summary?.unread ?: 0,
        muted = muted,
        pinned = pinned,
        pending = g.pendingJoins,
    )
}

@Composable
private fun InviteRow(pk: String, onAccept: () -> Unit, onDecline: () -> Unit) {
    Row(Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 6.dp), verticalAlignment = Alignment.CenterVertically) {
        ChatAvatar(pk, "", 40.dp)
        Spacer(Modifier.width(12.dp))
        Text(pk.take(8) + "…" + pk.takeLast(8), Modifier.weight(1f), style = MaterialTheme.typography.bodyMedium)
        IconButton(onClick = onAccept) { Icon(Icons.Rounded.Check, stringResource(R.string.chat_accept_invite), tint = MaterialTheme.colorScheme.primary) }
        IconButton(onClick = onDecline) { Icon(Icons.Rounded.Close, stringResource(R.string.chat_decline_invite)) }
    }
}

/** `role · N members · kind · state`, the line under a group's name (index.html _groupSubtitle). */
@Composable
fun groupSubtitle(g: GroupInfo, me: String): String {
    val parts = mutableListOf<String>()
    parts += when {
        g.role.isEmpty() || g.role == "member" -> stringResource(R.string.chat_role_member)
        g.role == "owner" -> stringResource(R.string.chat_role_owner)
        else -> g.role
    }
    val n = g.members.size
    parts += stringResource(if (n == 1) R.string.chat_members_one else R.string.chat_members_many, n)
    parts += when {
        g.isChannel -> stringResource(R.string.chat_kind_channel)
        g.approval -> stringResource(R.string.chat_kind_private)
        else -> stringResource(R.string.chat_kind_public)
    }
    when {
        g.status == "awaiting_approval" -> parts += stringResource(R.string.chat_state_waiting)
        g.status == "denied" -> parts += stringResource(R.string.chat_state_declined)
        g.status == "banned" -> parts += stringResource(R.string.chat_state_banned)
        g.readOnly -> parts += stringResource(R.string.chat_state_read_only)
        g.canPost == false -> parts += stringResource(R.string.chat_state_restricted)
    }
    return parts.joinToString(" · ")
}

/** `↑ `/`↓ ` and the shown text, the way list rows preview (index.html updatePreview). */
@Composable
fun previewText(m: ChatMessage, own: Boolean, group: Boolean = false, sender: String = ""): String {
    val text = shownText(m.text.ifEmpty { m.file?.name.orEmpty() })
    return when {
        !own -> text
        m.mine -> "↑ $text"
        group -> "$sender: $text"
        else -> "↓ $text"
    }
}

/** Localise the stock captions that travel in English (index.html _shownText). */
@Composable
fun shownText(text: String): String {
    val t = text.trim()
    Regex("^(🎤|📹) (Voice|Video) message$").find(t)?.let { m ->
        return m.groupValues[1] + " " + stringResource(if (m.groupValues[2] == "Voice") R.string.chat_voice_message else R.string.chat_video_message)
    }
    Regex("^📞 (Missed call|Incoming call|Outgoing call)( · \\d+:\\d\\d)?$").find(t)?.let { m ->
        return "📞 " + outcomeLabel(m.groupValues[1]) + m.groupValues[2]
    }
    return text
}

@Composable
fun outcomeLabel(outcome: String): String = when (outcome) {
    "Missed call" -> stringResource(R.string.call_missed_title)
    "Incoming call" -> stringResource(R.string.chat_call_incoming)
    "Outgoing call" -> stringResource(R.string.chat_call_outgoing)
    else -> outcome
}

/** Time for a list row: the clock today, the weekday this week, the date before. */
fun listTime(ms: Long): String {
    val zone = ZoneId.systemDefault()
    val t = Instant.ofEpochMilli(ms).atZone(zone)
    val today = LocalDate.now(zone)
    return when {
        t.toLocalDate() == today -> t.format(DateTimeFormatter.ofPattern("HH:mm"))
        t.toLocalDate().isAfter(today.minusDays(7)) -> t.format(DateTimeFormatter.ofPattern("EEE"))
        else -> t.format(DateTimeFormatter.ofLocalizedDate(FormatStyle.SHORT))
    }
}

@Composable
private fun CallsList(vm: ChatViewModel) {
    val repo = vm.repo
    var calls by remember { mutableStateOf<List<CallRecord>?>(null) }
    var failed by remember { mutableStateOf(false) }
    val names by repo.names.collectAsState()
    LaunchedEffect(names) {
        val list = repo.callLog()
        failed = list == null
        calls = list
    }
    when {
        failed -> Hint(R.string.chat_calls_failed)
        calls == null -> Unit
        calls!!.isEmpty() -> Hint(R.string.chat_no_calls)
        else -> LazyColumn(Modifier.fillMaxSize()) {
            items(calls!!) { c ->
                val missed = c.outcome == "Missed call"
                val name = repo.displayName(c.peer)
                Row(
                    Modifier.fillMaxWidth().clickable { repo.placeCall(c.peer.lowercase()) }.padding(horizontal = 16.dp, vertical = 10.dp),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    ChatAvatar(c.peer.lowercase(), name, 44.dp, load = { repo.store.avatar(c.peer.lowercase()) })
                    Spacer(Modifier.width(12.dp))
                    Column(Modifier.weight(1f)) {
                        Text(name, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                        Row(verticalAlignment = Alignment.CenterVertically) {
                            Icon(
                                if (c.direction == "out") Icons.AutoMirrored.Rounded.CallMade else Icons.AutoMirrored.Rounded.CallReceived,
                                null,
                                Modifier.size(14.dp),
                                tint = if (missed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Spacer(Modifier.width(4.dp))
                            val parts = listOfNotNull(
                                outcomeLabel(c.outcome),
                                parseTime(c.at)?.let { listTime(it) },
                                c.seconds.takeIf { it > 0 }?.let { s -> if (s < 60) stringResource(R.string.chat_secs, s) else stringResource(R.string.chat_mins_secs, s / 60, s % 60) },
                            )
                            Text(
                                parts.joinToString(" · "),
                                style = MaterialTheme.typography.bodySmall,
                                color = if (missed) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                        }
                    }
                    Icon(Icons.Rounded.Call, stringResource(R.string.chat_call_name, name), tint = MaterialTheme.colorScheme.primary)
                }
                HorizontalDivider(Modifier.padding(start = 72.dp), color = MaterialTheme.colorScheme.outlineVariant)
            }
        }
    }
}

@Composable
fun Hint(res: Int) {
    Box(Modifier.fillMaxWidth().padding(32.dp), contentAlignment = Alignment.Center) {
        Text(stringResource(res), style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    }
}

fun isMutedIn(muted: Set<String>, key: String): Boolean = key in muted || key.lowercase() in muted

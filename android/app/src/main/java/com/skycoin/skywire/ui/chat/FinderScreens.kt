package com.skycoin.skywire.ui.chat

import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.selection.toggleable
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Image
import androidx.compose.material.icons.rounded.QrCodeScanner
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.focus.FocusRequester
import androidx.compose.ui.focus.focusRequester
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import com.journeyapps.barcodescanner.ScanContract
import com.journeyapps.barcodescanner.ScanOptions
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.Catalog
import com.skycoin.skywire.chat.ChatMessage
import com.skycoin.skywire.chat.PeerLookup
import com.skycoin.skywire.chat.Resolved
import com.skycoin.skywire.chat.SAVED_KEY
import com.skycoin.skywire.chat.SearchResult
import com.skycoin.skywire.chat.abbrevPk
import com.skycoin.skywire.chat.forward
import com.skycoin.skywire.chat.join
import com.skycoin.skywire.chat.lookupPeer
import com.skycoin.skywire.chat.searchMessages
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.decodeQrImage
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

private sealed interface AddrState {
    data object Empty : AddrState
    data object Pending : AddrState
    data class Error(val text: String) : AddrState
    data class Done(val res: Resolved) : AddrState
}

/** One field for every way of naming something; resolve first, then offer the one action that applies. */
@Composable
fun AddAddressScreen(vm: ChatViewModel, prefill: String, fromLink: Boolean, onBack: () -> Unit) {
    val repo = vm.repo
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val groups by repo.groups.collectAsState()
    val local by repo.store.state.collectAsState()
    var raw by remember { mutableStateOf(prefill) }
    var state by remember { mutableStateOf<AddrState>(AddrState.Empty) }
    var now by remember { mutableIntStateOf(if (prefill.isNotEmpty()) 1 else 0) }
    var profile by remember { mutableStateOf<PeerLookup?>(null) }
    var catalog by remember { mutableStateOf<Catalog?>(null) }
    var save by remember { mutableStateOf(true) }
    var working by remember { mutableStateOf(false) }
    val focus = remember { FocusRequester() }

    LaunchedEffect(raw, now) {
        profile = null
        catalog = null
        val text = raw.trim()
        if (text.isEmpty()) {
            state = AddrState.Empty
            return@LaunchedEffect
        }
        val pk = keyFrom(text)
        if (pk.isNotEmpty()) {
            state = AddrState.Done(Resolved(target = "dm", pk = pk, address = "skychat://$pk"))
        } else {
            if (now == 0) delay(350)
            state = AddrState.Pending
            state = runCatching { repo.api.resolve(text) }.fold(
                { AddrState.Done(it) },
                { AddrState.Error(repo.errorText(it).ifEmpty { context.getString(R.string.chat_could_not_resolve) }) },
            )
        }
        now = 0
        val done = state as? AddrState.Done ?: return@LaunchedEffect
        if (done.res.target == "dm") {
            save = !repo.isContact(done.res.pk)
            launch { profile = repo.lookupPeer(done.res.pk)?.takeIf { !it.unreachable && (it.name.isNotEmpty() || it.avatar != null) } }
            launch { catalog = runCatching { repo.api.catalog(done.res.pk) }.getOrNull() }
        }
    }
    LaunchedEffect(Unit) { if (!fromLink && prefill.isEmpty()) focus.requestFocus() }

    val scan = rememberLauncherForActivityResult(ScanContract()) { result ->
        result.contents?.let {
            raw = it.trim()
            now++
            repo.note(R.string.chat_code_scanned)
        }
    }
    val fromImage = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri ->
        uri ?: return@rememberLauncherForActivityResult
        scope.launch {
            val text = withContext(Dispatchers.Default) { decodeQrImage(context, uri) }
            if (text.isNullOrEmpty()) {
                repo.note(R.string.chat_no_qr_in_image, error = true)
            } else {
                raw = text.trim()
                now++
                repo.note(R.string.chat_code_scanned)
            }
        }
    }

    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_add_by_address), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(20.dp)) {
            OutlinedTextField(
                value = raw,
                onValueChange = { raw = it },
                placeholder = { Text(stringResource(R.string.chat_address_placeholder)) },
                modifier = Modifier.fillMaxWidth().focusRequester(focus),
                maxLines = 4,
            )
            Spacer(Modifier.height(8.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = {
                    scan.launch(ScanOptions().setDesiredBarcodeFormats(ScanOptions.QR_CODE).setBeepEnabled(false).setOrientationLocked(true))
                }) {
                    Icon(Icons.Rounded.QrCodeScanner, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(stringResource(R.string.chat_scan_qr))
                }
                OutlinedButton(onClick = { fromImage.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) }) {
                    Icon(Icons.Rounded.Image, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(stringResource(R.string.chat_from_image))
                }
            }
            Spacer(Modifier.height(16.dp))
            val s = state
            when (s) {
                AddrState.Empty -> Help(R.string.chat_address_hint)
                AddrState.Pending -> ResultCard(null, stringResource(R.string.chat_looking_it_up), stringResource(R.string.chat_asking_host), loading = true)
                is AddrState.Error -> {
                    ResultCard(null, stringResource(R.string.chat_not_resolved), s.text, bad = true)
                    Help(R.string.chat_address_offline_hint)
                }
                is AddrState.Done -> {
                    val res = s.res
                    if (res.target == "dm") {
                        val nick = repo.names.value[res.pk]
                        val p = profile
                        val title = nick ?: p?.name?.takeIf { it.isNotEmpty() } ?: abbrevPk(res.pk)
                        val sub = when {
                            p?.name?.isNotEmpty() == true && nick != null -> stringResource(R.string.chat_person_publishes, p.name)
                            p?.name?.isNotEmpty() == true -> stringResource(R.string.chat_person_published)
                            else -> stringResource(R.string.chat_person_dm)
                        }
                        ResultCard(res.pk, title, sub, avatar = p?.avatar)
                        Help(R.string.chat_person_hint)
                        if (!repo.isContact(res.pk)) {
                            Row(
                                Modifier.toggleable(value = save, role = androidx.compose.ui.semantics.Role.Checkbox) { save = it },
                                verticalAlignment = Alignment.CenterVertically,
                            ) {
                                Checkbox(checked = save, onCheckedChange = null)
                                Text(stringResource(R.string.chat_save_to_contacts))
                            }
                        }
                        Spacer(Modifier.height(8.dp))
                        val known = res.pk in local.recipients
                        Button(modifier = Modifier.fillMaxWidth(), onClick = {
                            if (save && !repo.isContact(res.pk)) {
                                repo.addContact(res.pk, p?.name, p?.avatar)
                                repo.note(R.string.chat_saved_to_contacts)
                            }
                            repo.ensureRecipient(res.pk)
                            vm.openThread(res.pk)
                        }) { Text(stringResource(if (known) R.string.chat_open_chat else R.string.chat_start_chat)) }
                        catalog?.takeIf { it.entries.isNotEmpty() }?.let { c ->
                            Spacer(Modifier.height(16.dp))
                            Text(
                                stringResource(R.string.chat_catalog_title) + if (c.truncated) " " + stringResource(R.string.chat_catalog_truncated) else "",
                                style = MaterialTheme.typography.titleSmall,
                            )
                            for (e in c.entries) {
                                val joined = groups.any { it.id == e.id }
                                val meta = listOfNotNull(
                                    stringResource(if (e.kind == "channel") R.string.chat_noun_channel else R.string.chat_noun_group),
                                    stringResource(if (e.policy == "approval") R.string.chat_approval_needed else R.string.chat_open_to_join),
                                    e.priceHint.takeIf { it.isNotEmpty() }?.let { stringResource(R.string.chat_price_claimed, it) },
                                ).joinToString(" · ")
                                Row(
                                    Modifier.fillMaxWidth().clip(RoundedCornerShape(12.dp)).clickable {
                                        raw = e.address
                                    }.padding(vertical = 8.dp),
                                    verticalAlignment = Alignment.CenterVertically,
                                ) {
                                    ChatAvatar(e.id, e.name, 36.dp, if (e.kind == "channel") AvatarKind.CHANNEL else AvatarKind.GROUP)
                                    Spacer(Modifier.width(10.dp))
                                    Column(Modifier.weight(1f)) {
                                        Text(e.name.ifEmpty { e.id }, maxLines = 1, overflow = TextOverflow.Ellipsis)
                                        Text(meta, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                                    }
                                    Text(stringResource(if (joined) R.string.chat_catalog_open else R.string.chat_catalog_view), color = MaterialTheme.colorScheme.primary)
                                }
                            }
                        }
                    } else {
                        val g = res.group
                        val channel = g?.kind == "channel"
                        val noun = stringResource(if (channel) R.string.chat_noun_channel else R.string.chat_noun_group)
                        val sub = when {
                            g?.banned == true -> "$noun · " + stringResource(R.string.chat_addr_banned)
                            res.joined -> "$noun · " + stringResource(R.string.chat_addr_joined)
                            res.status == "awaiting_approval" -> "$noun · " + stringResource(R.string.chat_addr_waiting)
                            res.status == "denied" -> "$noun · " + stringResource(R.string.chat_addr_denied)
                            else -> listOf(
                                noun,
                                stringResource(if (g?.policy == "approval") R.string.chat_approval_needed else R.string.chat_open_to_join),
                                stringResource(
                                    when {
                                        channel -> R.string.chat_only_admins_post
                                        g?.mode == "private" -> R.string.chat_encrypted
                                        else -> R.string.chat_not_encrypted
                                    },
                                ),
                            ).joinToString(" · ")
                        }
                        ResultCard(g?.id ?: "", g?.name?.ifEmpty { null } ?: g?.id ?: stringResource(R.string.chat_unnamed), sub, group = true, channel = channel, bad = g?.banned == true)
                        when {
                            g?.priceHint?.isNotEmpty() == true -> Text(stringResource(R.string.chat_price_hint, g.priceHint), style = MaterialTheme.typography.bodySmall)
                            channel -> Help(R.string.chat_channel_join_hint)
                            g?.policy == "approval" -> Help(R.string.chat_approval_join_hint)
                            else -> Help(R.string.chat_open_join_hint)
                        }
                        Spacer(Modifier.height(8.dp))
                        val label = when {
                            g == null || g.banned || res.status == "awaiting_approval" -> null
                            res.joined -> if (channel) R.string.chat_open_channel else R.string.chat_open_group
                            channel -> R.string.chat_join_channel
                            g.policy == "approval" -> R.string.chat_send_request
                            else -> R.string.chat_join_group
                        }
                        if (label != null) {
                            Button(enabled = !working, modifier = Modifier.fillMaxWidth(), onClick = {
                                if (res.joined) {
                                    vm.openThread(g!!.id)
                                    return@Button
                                }
                                working = true
                                scope.launch {
                                    val info = repo.join(res)
                                    working = false
                                    if (info != null) vm.openThread(info.id.ifEmpty { g!!.id }) else if (repo.groupInfo(g!!.id) != null) onBack()
                                }
                            }) {
                                if (working) {
                                    CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                                    Spacer(Modifier.width(8.dp))
                                    Text(stringResource(R.string.chat_working))
                                } else {
                                    Text(stringResource(label))
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun ResultCard(
    key: String?,
    title: String,
    sub: String,
    loading: Boolean = false,
    bad: Boolean = false,
    group: Boolean = false,
    channel: Boolean = false,
    avatar: ByteArray? = null,
) {
    Row(
        Modifier.fillMaxWidth().clip(RoundedCornerShape(16.dp)).background(MaterialTheme.colorScheme.surfaceContainerHigh).padding(12.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        if (loading) {
            CircularProgressIndicator(Modifier.size(40.dp))
        } else if (key != null) {
            ChatAvatar(
                key.ifEmpty { title }, title, 44.dp,
                if (channel) AvatarKind.CHANNEL else if (group) AvatarKind.GROUP else AvatarKind.PERSON,
                version = avatar?.size, load = avatar?.let { a -> { a } },
            )
        } else {
            Box(Modifier.size(44.dp).clip(RoundedCornerShape(22.dp)).background(MaterialTheme.colorScheme.errorContainer), contentAlignment = Alignment.Center) {
                Text("!", color = MaterialTheme.colorScheme.error, fontWeight = FontWeight.Bold)
            }
        }
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            Text(sub, style = MaterialTheme.typography.bodySmall, color = if (bad) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

/** People, groups, channels and messages in one field (index.html openSearch). */
@Composable
fun SearchScreen(vm: ChatViewModel, onBack: () -> Unit) {
    val repo = vm.repo
    val local by repo.store.state.collectAsState()
    val groups by repo.groups.collectAsState()
    val names by repo.names.collectAsState()
    var query by remember { mutableStateOf("") }
    var hits by remember { mutableStateOf<List<SearchResult>?>(null) }
    val focus = remember { FocusRequester() }
    LaunchedEffect(Unit) { focus.requestFocus() }
    LaunchedEffect(query) {
        hits = null
        if (query.isBlank()) return@LaunchedEffect
        delay(250)
        hits = repo.searchMessages(query)
    }
    val q = query.trim().lowercase()
    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_search), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text(stringResource(R.string.chat_search_placeholder)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp).focusRequester(focus),
            )
            if (q.isEmpty()) {
                Hint(R.string.chat_search_empty)
                return@Column
            }
            val people = (local.recipients + local.contacts).distinct().filter { pk ->
                pk.contains(q) || (names[pk] ?: abbrevPk(pk)).lowercase().contains(q)
            }
            val gs = groups.filter { it.name.lowercase().contains(q) || it.id.lowercase().contains(q) }
            LazyColumn(Modifier.fillMaxSize()) {
                fun header(res: Int) = item(key = "h$res") {
                    Text(stringResource(res), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(start = 20.dp, top = 12.dp, bottom = 4.dp))
                }
                if (people.isNotEmpty()) {
                    header(R.string.chat_contacts)
                    items(people, key = { "p$it" }) { pk ->
                        val label = names[pk] ?: abbrevPk(pk)
                        SimpleRow(pk, label, abbrevPk(pk), AvatarKind.PERSON, load = { repo.store.avatar(pk) }) {
                            repo.ensureRecipient(pk)
                            vm.openThread(pk)
                        }
                    }
                }
                for (channel in listOf(false, true)) {
                    val list = gs.filter { it.isChannel == channel }
                    if (list.isEmpty()) continue
                    header(if (channel) R.string.chat_channels else R.string.chat_groups)
                    items(list, key = { "g${it.id}" }) { g ->
                        SimpleRow(g.id, g.name.ifEmpty { g.id }, groupSubtitle(g, repo.myPk.value), if (channel) AvatarKind.CHANNEL else AvatarKind.GROUP) { vm.openThread(g.id) }
                    }
                }
                header(R.string.chat_messages_header)
                val h = hits
                when {
                    h == null -> item(key = "searching") { Hint(R.string.chat_searching) }
                    h.isEmpty() && people.isEmpty() && gs.isEmpty() -> item(key = "none") { Hint(R.string.chat_nothing_found) }
                    else -> items(h.size, key = { "m$it" }) { i ->
                        val hit = h[i]
                        val title = when {
                            hit.key == SAVED_KEY -> stringResource(R.string.chat_saved_messages)
                            hit.isGroup -> repo.groupInfo(hit.key)?.name ?: hit.key
                            else -> repo.displayName(hit.key)
                        }
                        val prefix = when {
                            hit.from == ChatMessage.ME || hit.from == repo.myPk.value -> stringResource(R.string.chat_you_prefix) + " "
                            hit.isGroup -> repo.displayName(hit.from) + ": "
                            else -> ""
                        }
                        Column(
                            Modifier.fillMaxWidth().clickable {
                                if (!hit.isGroup && hit.key != SAVED_KEY) repo.ensureRecipient(hit.key)
                                vm.scrollTarget.value = hit.key to hit.ts
                                vm.openThread(hit.key)
                            }.padding(horizontal = 20.dp, vertical = 8.dp),
                        ) {
                            Row {
                                Text(title, style = MaterialTheme.typography.titleSmall, modifier = Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                                Text(listTime(hit.ts), style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                            }
                            Text(snippet(prefix, shownText(hit.text), q), style = MaterialTheme.typography.bodySmall, maxLines = 2, overflow = TextOverflow.Ellipsis)
                        }
                    }
                }
            }
        }
    }
}

@Composable
private fun snippet(prefix: String, text: String, q: String) = buildAnnotatedString {
    append(prefix)
    val i = text.lowercase().indexOf(q)
    if (i < 0) {
        append(text.take(90))
        return@buildAnnotatedString
    }
    val start = (i - 30).coerceAtLeast(0)
    if (start > 0) append("…")
    append(text.substring(start, i))
    withStyle(SpanStyle(fontWeight = FontWeight.Bold, background = MaterialTheme.colorScheme.primary.copy(alpha = 0.2f))) {
        append(text.substring(i, i + q.length))
    }
    val end = (i + q.length + 60).coerceAtMost(text.length)
    append(text.substring(i + q.length, end))
    if (end < text.length) append("…")
}

@Composable
private fun SimpleRow(key: String, title: String, sub: String, kind: AvatarKind, load: (suspend () -> ByteArray?)? = null, onClick: () -> Unit) {
    Row(Modifier.fillMaxWidth().clickable(onClick = onClick).padding(horizontal = 16.dp, vertical = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        ChatAvatar(key, title, 40.dp, kind, load = load)
        Spacer(Modifier.width(12.dp))
        Column(Modifier.weight(1f)) {
            Text(title, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
            if (sub.isNotEmpty()) Text(sub, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, maxLines = 1, overflow = TextOverflow.Ellipsis)
        }
    }
}

/** Every destination in one list; picking one sends at once (index.html forward modal). */
@Composable
fun ForwardScreen(vm: ChatViewModel, fromKey: String, msgKey: String, onBack: () -> Unit) {
    val repo = vm.repo
    val local by repo.store.state.collectAsState()
    val groups by repo.groups.collectAsState()
    val names by repo.names.collectAsState()
    var query by remember { mutableStateOf("") }
    val msg = repo.thread(fromKey).value.firstOrNull { it.key == msgKey }
    val origin = repo.groupInfo(fromKey)?.takeIf { it.isChannel }?.name
    val q = query.trim().lowercase()
    fun match(label: String, id: String) = q.isEmpty() || label.lowercase().contains(q) || id.lowercase().contains(q)
    fun pick(to: String) {
        msg ?: return
        repo.forward(fromKey, msg, to)
        if (!repo.isGroupKey(to) && to != SAVED_KEY) repo.ensureRecipient(to)
        vm.openThread(to)
    }
    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_forward_to), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            Text(
                if (origin != null) stringResource(R.string.chat_forward_origin_channel, origin) else stringResource(R.string.chat_forward_origin),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(horizontal = 20.dp, vertical = 4.dp),
            )
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text(stringResource(R.string.chat_forward_search)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            )
            val chats = local.recipients.filter { match(names[it] ?: abbrevPk(it), it) }
            val book = local.contacts.filter { it !in local.recipients && match(names[it] ?: abbrevPk(it), it) }
            val gs = groups.filter { it.postable && !it.readOnly && (it.status.isEmpty() || it.status == "active") && match(it.name, it.id) }
            val saved = match(stringResource(R.string.chat_saved_messages), SAVED_KEY)
            LazyColumn(Modifier.fillMaxSize()) {
                if (saved) {
                    item(key = "saved") {
                        SimpleRow(SAVED_KEY, stringResource(R.string.chat_saved_messages), stringResource(R.string.chat_notes_on_device), AvatarKind.SAVED) { pick(SAVED_KEY) }
                    }
                }
                fun header(res: Int) = item(key = "h$res") {
                    Text(stringResource(res), style = MaterialTheme.typography.labelLarge, color = MaterialTheme.colorScheme.primary, modifier = Modifier.padding(start = 20.dp, top = 12.dp, bottom = 4.dp))
                }
                if (chats.isNotEmpty()) {
                    header(R.string.chat_chats)
                    items(chats, key = { "c$it" }) { pk ->
                        val n = names[pk]
                        SimpleRow(pk, n ?: abbrevPk(pk), if (n != null) abbrevPk(pk) else "", AvatarKind.PERSON, load = { repo.store.avatar(pk) }) { pick(pk) }
                    }
                }
                if (book.isNotEmpty()) {
                    header(R.string.chat_address_book)
                    items(book, key = { "b$it" }) { pk ->
                        val n = names[pk]
                        SimpleRow(pk, n ?: abbrevPk(pk), if (n != null) abbrevPk(pk) else "", AvatarKind.PERSON, load = { repo.store.avatar(pk) }) { pick(pk) }
                    }
                }
                if (gs.isNotEmpty()) {
                    header(R.string.chat_groups_and_channels)
                    items(gs, key = { "g${it.id}" }) { g ->
                        SimpleRow(g.id, g.name.ifEmpty { g.id }, "", if (g.isChannel) AvatarKind.CHANNEL else AvatarKind.GROUP) { pick(g.id) }
                    }
                }
                if (!saved && chats.isEmpty() && book.isEmpty() && gs.isEmpty()) {
                    item(key = "none") { Hint(R.string.chat_nothing_matches) }
                }
            }
        }
    }
}

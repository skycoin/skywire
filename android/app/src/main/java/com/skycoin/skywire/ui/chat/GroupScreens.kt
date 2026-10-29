package com.skycoin.skywire.ui.chat

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.selection.toggleable
import androidx.compose.ui.semantics.Role
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.Button
import androidx.compose.material3.Checkbox
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.GroupInfo
import com.skycoin.skywire.chat.JoinRequest
import com.skycoin.skywire.chat.abbrevPk
import com.skycoin.skywire.chat.createGroup
import com.skycoin.skywire.chat.dataUrl
import com.skycoin.skywire.chat.isAdmin
import com.skycoin.skywire.chat.isFounder
import com.skycoin.skywire.chat.memberAction
import com.skycoin.skywire.chat.rotateKey
import com.skycoin.skywire.chat.setGroupAvatar
import com.skycoin.skywire.chat.setGroupName
import com.skycoin.skywire.chat.setJoinCost
import com.skycoin.skywire.chat.setListed
import com.skycoin.skywire.chat.setPeerBackfill
import com.skycoin.skywire.chat.setReadOnly
import com.skycoin.skywire.ui.components.SkyTopBar
import kotlinx.coroutines.flow.filter
import kotlinx.coroutines.launch

@Composable
fun NewGroupScreen(vm: ChatViewModel, channel: Boolean, onBack: () -> Unit) {
    val repo = vm.repo
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var name by remember { mutableStateOf("") }
    var private by remember { mutableStateOf(false) }
    var backfill by remember { mutableStateOf(true) }
    var listed by remember { mutableStateOf(false) }
    var busy by remember { mutableStateOf(false) }
    Scaffold(topBar = { SkyTopBar(title = stringResource(if (channel) R.string.chat_new_channel_title else R.string.chat_create_group), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(20.dp)) {
            OutlinedTextField(
                value = name,
                onValueChange = { name = it.take(64) },
                label = { Text(stringResource(if (channel) R.string.chat_channel_name else R.string.chat_group_name)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Spacer(Modifier.height(16.dp))
            if (channel) {
                Help(R.string.chat_channel_help_1)
                Help(R.string.chat_channel_help_2)
                CheckRow(R.string.chat_list_catalog, listed) { listed = it }
                Help(R.string.chat_list_catalog_help)
            } else {
                SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth()) {
                    SegmentedButton(selected = !private, onClick = { private = false }, shape = SegmentedButtonDefaults.itemShape(0, 2)) {
                        Text(stringResource(R.string.chat_kind_public_short), maxLines = 1)
                    }
                    SegmentedButton(selected = private, onClick = { private = true }, shape = SegmentedButtonDefaults.itemShape(1, 2)) {
                        Text(stringResource(R.string.chat_kind_private_short), maxLines = 1)
                    }
                }
                Spacer(Modifier.height(8.dp))
                Text(stringResource(if (private) R.string.chat_kind_private_label else R.string.chat_kind_public_label), style = MaterialTheme.typography.titleSmall)
                Help(if (private) R.string.chat_kind_private_help else R.string.chat_kind_public_help)
                CheckRow(R.string.chat_backfill, backfill) { backfill = it }
                Help(R.string.chat_backfill_help)
            }
            Spacer(Modifier.height(16.dp))
            Button(
                enabled = !busy,
                modifier = Modifier.fillMaxWidth(),
                onClick = {
                    if (name.isBlank()) {
                        repo.note(if (channel) R.string.chat_enter_channel_name else R.string.chat_enter_group_name, error = true)
                        return@Button
                    }
                    busy = true
                    scope.launch {
                        val created = repo.createGroup(
                            name.trim(),
                            if (channel) "channel" else if (private) "private" else "public",
                            peerBackfill = if (channel) null else backfill,
                            listed = if (channel) listed else null,
                        )
                        busy = false
                        if (created != null) {
                            val (id, invite) = created
                            if (invite.isNotEmpty()) {
                                copyText(context, invite)
                                repo.note(if (channel) R.string.chat_channel_created else R.string.chat_group_created)
                            }
                            vm.openThread(id)
                        }
                    }
                },
            ) { Text(stringResource(R.string.chat_create)) }
        }
    }
}

private val JOIN_COSTS = listOf(0 to R.string.chat_cost_free, 16 to R.string.chat_cost_low, 18 to R.string.chat_cost_normal, 22 to R.string.chat_cost_high, 26 to R.string.chat_cost_max)

/** The admin panel (index.html groupAdminModal): what you may change depends on founder/admin/member. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun GroupSettingsScreen(vm: ChatViewModel, gid: String, onBack: () -> Unit) {
    val repo = vm.repo
    val groups by repo.groups.collectAsState()
    val names by repo.names.collectAsState()
    val myPk by repo.myPk.collectAsState()
    val g = groups.firstOrNull { it.id == gid }
    var requests by remember { mutableStateOf<List<JoinRequest>?>(null) }
    var rotating by remember { mutableStateOf(false) }
    if (g == null) {
        Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_group_settings), onBack = onBack) }) { padding ->
            Column(Modifier.padding(padding)) { Hint(R.string.chat_group_gone) }
        }
        return
    }
    val admin = repo.isAdmin(g)
    val founder = repo.isFounder(g)
    val channel = g.isChannel
    LaunchedEffect(gid, admin) {
        if (!admin) return@LaunchedEffect
        suspend fun load() {
            requests = runCatching { repo.api.groupRequests(gid) }.getOrNull()
        }
        load()
        repo.groupChanged.filter { it == gid }.collect { load() }
    }
    var name by remember(g.name) { mutableStateOf(g.name) }
    val pickAvatar = rememberAvatarPicker(vm, AvatarTarget.GROUP) { repo.setGroupAvatar(gid, dataUrl(it)) }

    Scaffold(topBar = { SkyTopBar(title = stringResource(if (channel) R.string.chat_channel_settings else R.string.chat_group_settings), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                ChatAvatar(
                    g.id, g.name, 64.dp, if (channel) AvatarKind.CHANNEL else AvatarKind.GROUP,
                    version = g.avatar.hashCode(),
                    load = g.avatar.takeIf { it.isNotEmpty() }?.let { a -> { com.skycoin.skywire.chat.decodeDataUrl(a) } },
                )
                Spacer(Modifier.width(12.dp))
                Column(Modifier.weight(1f)) {
                    Text(g.name.ifEmpty { g.id }, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold)
                    Text(groupSubtitle(g, myPk), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
            }
            if (founder) {
                Spacer(Modifier.height(12.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    OutlinedTextField(
                        value = name,
                        onValueChange = { name = it.take(64) },
                        label = { Text(stringResource(if (channel) R.string.chat_channel_name else R.string.chat_group_name)) },
                        singleLine = true,
                        modifier = Modifier.weight(1f),
                    )
                    Spacer(Modifier.width(8.dp))
                    OutlinedButton(onClick = { repo.setGroupName(gid, name) }) { Text(stringResource(R.string.chat_rename)) }
                }
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = pickAvatar) { Text(stringResource(R.string.chat_change_picture)) }
                    if (g.avatar.isNotEmpty()) OutlinedButton(onClick = { repo.setGroupAvatar(gid, "") }) { Text(stringResource(R.string.chat_remove_picture)) }
                }
                Help(R.string.chat_meta_note)
            }
            if (admin) {
                GDivider()
                if (!channel) {
                    CheckRow(R.string.chat_read_only, g.readOnly) { repo.setReadOnly(gid, it) }
                }
                CheckRow(R.string.chat_list_visor_catalog, g.listed) { repo.setListed(gid, it) }
                Help(R.string.chat_list_visor_catalog_help)
                JoinCostRow(g) { repo.setJoinCost(gid, it) }
                Help(R.string.chat_join_cost_help)
                if (!channel) {
                    CheckRow(R.string.chat_backfill, g.peerBackfill != false) { repo.setPeerBackfill(gid, it) }
                    Help(R.string.chat_backfill_admin_help)
                }
            }
            if (g.governanceSealed) {
                GDivider()
                Text(stringResource(R.string.chat_sealed_title), style = MaterialTheme.typography.titleSmall)
                Help(R.string.chat_sealed_help)
            }
            if (g.encrypted && admin) {
                GDivider()
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Column(Modifier.weight(1f)) {
                        Text(stringResource(R.string.chat_key_epoch, g.keyEpoch), style = MaterialTheme.typography.titleSmall)
                        keyAge(g)?.let { Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
                    }
                    OutlinedButton(enabled = !rotating, onClick = {
                        rotating = true
                        repo.rotateKey(gid) { rotating = false }
                    }) { Text(stringResource(if (rotating) R.string.chat_rotating else R.string.chat_rotate_key)) }
                }
                Help(R.string.chat_key_help)
            }

            GDivider()
            val reqs = requests.orEmpty()
            if (admin && reqs.isNotEmpty()) {
                Text(stringResource(R.string.chat_join_requests) + " (${reqs.size})", style = MaterialTheme.typography.titleSmall)
                for (r in reqs) {
                    Row(Modifier.fillMaxWidth().padding(vertical = 4.dp), verticalAlignment = Alignment.CenterVertically) {
                        Column(Modifier.weight(1f)) {
                            Text(names[r.pk.lowercase()] ?: abbrevPk(r.pk), maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(r.note.ifEmpty { stringResource(R.string.chat_wants_to_join) }, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        TextButton(onClick = { repo.memberAction(gid, "requests/approve", r.pk) }) { Text(stringResource(R.string.chat_approve)) }
                        TextButton(onClick = { repo.memberAction(gid, "requests/deny", r.pk) }) {
                            Text(stringResource(R.string.chat_decline), color = MaterialTheme.colorScheme.error)
                        }
                    }
                }
                Spacer(Modifier.height(8.dp))
                Text(stringResource(if (channel) R.string.chat_in_channel else R.string.chat_in_group), style = MaterialTheme.typography.titleSmall)
            } else {
                Text(stringResource(R.string.chat_members), style = MaterialTheme.typography.titleSmall)
            }
            if (g.members.isEmpty()) Help(R.string.chat_no_members)
            val admins = (g.admins + g.ownerPk).map { it.lowercase() }.filter { it.isNotEmpty() }.toSet()
            for (pk in g.members.map { it.lowercase() }) {
                val isOwner = pk == g.ownerPk.lowercase()
                val isAdminRow = pk in admins
                val restricted = g.muted.any { it.equals(pk, true) }
                val tags = listOfNotNull(
                    if (isOwner) stringResource(R.string.chat_tag_founder) else if (isAdminRow) stringResource(R.string.chat_tag_admin) else null,
                    if (pk == myPk) stringResource(R.string.chat_tag_you) else null,
                    if (restricted) stringResource(R.string.chat_state_restricted) else null,
                )
                Column(Modifier.fillMaxWidth().padding(vertical = 4.dp)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        ChatAvatar(pk, repo.displayName(pk), 32.dp, load = { repo.store.avatar(pk) })
                        Spacer(Modifier.width(10.dp))
                        Column(Modifier.weight(1f)) {
                            Text(repo.displayName(pk), maxLines = 1, overflow = TextOverflow.Ellipsis)
                            if (tags.isNotEmpty()) Text(tags.joinToString(" · "), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                    }
                    if (admin && !isOwner && pk != myPk) {
                        FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), modifier = Modifier.padding(start = 42.dp)) {
                            TextButton(onClick = { repo.memberAction(gid, if (isAdminRow) "members/demote" else "members/promote", pk) }) {
                                Text(stringResource(if (isAdminRow) R.string.chat_demote else R.string.chat_make_admin))
                            }
                            TextButton(onClick = { repo.memberAction(gid, if (restricted) "members/unmute" else "members/mute", pk) }) {
                                Text(stringResource(if (restricted) R.string.chat_unrestrict else R.string.chat_restrict))
                            }
                            TextButton(onClick = { repo.memberAction(gid, "members/remove", pk) }) { Text(stringResource(R.string.chat_remove)) }
                            TextButton(onClick = { repo.memberAction(gid, "members/ban", pk) }) {
                                Text(stringResource(R.string.chat_ban), color = MaterialTheme.colorScheme.error)
                            }
                        }
                    }
                }
            }
            if (admin && admins.size <= 1 && g.members.size > 1) {
                Help(R.string.chat_solo_admin)
            }
            if (g.banned.isNotEmpty()) {
                GDivider()
                Text(stringResource(R.string.chat_banned), style = MaterialTheme.typography.titleSmall)
                for (pk in g.banned.map { it.lowercase() }) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(repo.displayName(pk), Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        if (admin) TextButton(onClick = { repo.memberAction(gid, "members/unban", pk) }) { Text(stringResource(R.string.chat_unban)) }
                    }
                }
            }
            Spacer(Modifier.height(24.dp))
        }
    }
}

@Composable
private fun JoinCostRow(g: GroupInfo, onPick: (Int) -> Unit) {
    var open by remember { mutableStateOf(false) }
    val bits = g.joinPowBits ?: 18
    val shown = JOIN_COSTS.minBy { kotlin.math.abs(it.first - bits) }
    Row(Modifier.fillMaxWidth().padding(top = 8.dp), verticalAlignment = Alignment.CenterVertically) {
        Text(stringResource(R.string.chat_join_cost), Modifier.weight(1f), style = MaterialTheme.typography.bodyLarge)
        TextButton(onClick = { open = true }) { Text(stringResource(shown.second)) }
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            for ((b, label) in JOIN_COSTS) {
                DropdownMenuItem(text = { Text(stringResource(label)) }, onClick = {
                    open = false
                    if (b != bits) onPick(b)
                })
            }
        }
    }
}

@Composable
private fun keyAge(g: GroupInfo): String? {
    if (g.keyAgeSeconds <= 0) return null
    val age = stringResource(R.string.chat_key_in_use, humanDuration(g.keyAgeSeconds))
    val next = g.keyRotatesInSeconds?.let {
        if (it > 0) stringResource(R.string.chat_key_rotates_in, humanDuration(it)) else stringResource(R.string.chat_key_rotation_due)
    }
    return listOfNotNull(age, next).joinToString(" · ")
}

@Composable
private fun humanDuration(seconds: Long): String {
    val s = kotlin.math.abs(seconds)
    return when {
        s < 60 -> stringResource(R.string.chat_dur_under_minute)
        s < 3600 -> stringResource(R.string.chat_dur_min, s / 60)
        s < 86400 -> (s / 3600).let { stringResource(if (it == 1L) R.string.chat_dur_hour else R.string.chat_dur_hours, it) }
        else -> (s / 86400).let { stringResource(if (it == 1L) R.string.chat_dur_day else R.string.chat_dur_days, it) }
    }
}

@Composable
fun Help(res: Int) {
    Text(
        stringResource(res),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(vertical = 4.dp),
    )
}

@Composable
fun CheckRow(label: Int, checked: Boolean, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().toggleable(value = checked, role = Role.Checkbox, onValueChange = onChange),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Checkbox(checked = checked, onCheckedChange = null)
        Text(stringResource(label), style = MaterialTheme.typography.bodyLarge)
    }
}

@Composable
private fun GDivider() {
    HorizontalDivider(Modifier.padding(vertical = 12.dp), color = MaterialTheme.colorScheme.outlineVariant)
}

package com.skycoin.skywire.ui.chat

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Edit
import androidx.compose.material.icons.rounded.PersonAdd
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.ChatRepository
import com.skycoin.skywire.chat.PeerLookup
import com.skycoin.skywire.chat.abbrevPk
import com.skycoin.skywire.chat.clearProfile
import com.skycoin.skywire.chat.decodeDataUrl
import com.skycoin.skywire.chat.isPk
import com.skycoin.skywire.chat.lookupPeer
import com.skycoin.skywire.chat.removeContact
import com.skycoin.skywire.chat.removeProfilePicture
import com.skycoin.skywire.chat.saveProfile
import com.skycoin.skywire.ui.components.SkyTopBar
import kotlinx.coroutines.delay
import kotlinx.coroutines.launch

/** A pasted key or `skychat://<pk>`, normalised; empty when it is neither. */
fun keyFrom(raw: String): String {
    val bare = raw.trim().lowercase().removePrefix("skychat://").trimEnd('/')
    return if (isPk(bare)) bare else ""
}

/** The address book: browse opens a chat, pick hands the key to Add by address. */
@Composable
fun ContactsScreen(vm: ChatViewModel, pick: Boolean, onBack: () -> Unit) {
    val repo = vm.repo
    val local by repo.store.state.collectAsState()
    val names by repo.names.collectAsState()
    val presence by repo.presence.collectAsState()
    val avatars by repo.store.avatarsChanged.collectAsState()
    var query by remember { mutableStateOf("") }
    var removing by remember { mutableStateOf<String?>(null) }
    val q = query.trim().lowercase()
    val list = local.contacts
        .filter { q.isEmpty() || it.contains(q) || names[it]?.lowercase()?.contains(q) == true }
        .sortedWith(compareBy({ names[it].isNullOrEmpty() }, { (names[it] ?: it).lowercase() }))
    Scaffold(
        topBar = {
            SkyTopBar(
                title = stringResource(if (pick) R.string.chat_choose_contact else R.string.chat_address_book),
                onBack = onBack,
                actions = {
                    if (!pick) {
                        IconButton(onClick = { vm.push(ChatPage.NewContact("")) }) {
                            Icon(Icons.Rounded.PersonAdd, stringResource(R.string.chat_add_contact))
                        }
                    }
                },
            )
        },
    ) { padding ->
        Column(Modifier.fillMaxSize().padding(padding)) {
            OutlinedTextField(
                value = query,
                onValueChange = { query = it },
                placeholder = { Text(stringResource(R.string.chat_search_contacts)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth().padding(horizontal = 16.dp, vertical = 8.dp),
            )
            if (list.isEmpty()) {
                Hint(if (local.contacts.isEmpty()) R.string.chat_no_contacts else R.string.chat_no_contact_match)
            }
            LazyColumn(Modifier.fillMaxSize()) {
                items(list, key = { it }) { pk ->
                    val label = names[pk]?.takeIf { it.isNotEmpty() } ?: abbrevPk(pk)
                    Row(
                        Modifier.fillMaxWidth().clickable {
                            if (pick) {
                                vm.replace(ChatPage.AddAddress("skychat://$pk", fromLink = false))
                            } else {
                                repo.ensureRecipient(pk)
                                vm.openThread(pk)
                            }
                        }.padding(horizontal = 16.dp, vertical = 8.dp),
                        verticalAlignment = Alignment.CenterVertically,
                    ) {
                        ChatAvatar(pk, label, 44.dp, version = avatars, online = presence[pk]?.online, load = { repo.store.avatar(pk) })
                        Spacer(Modifier.width(12.dp))
                        Column(Modifier.weight(1f)) {
                            Text(label, style = MaterialTheme.typography.titleSmall, maxLines = 1, overflow = TextOverflow.Ellipsis)
                            Text(abbrevPk(pk), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                        }
                        if (!pick) {
                            IconButton(onClick = {
                                repo.ensureRecipient(pk)
                                vm.push(ChatPage.ContactSettings(pk))
                            }) { Icon(Icons.Rounded.Edit, stringResource(R.string.chat_edit_contact)) }
                            IconButton(onClick = { removing = pk }) { Icon(Icons.Rounded.Close, stringResource(R.string.chat_remove_contact)) }
                        }
                    }
                }
            }
        }
    }
    removing?.let { pk ->
        ConfirmDialog(
            stringResource(R.string.chat_confirm_remove_contact),
            stringResource(R.string.chat_remove),
            onConfirm = { repo.removeContact(pk) },
            onDismiss = { removing = null },
        )
    }
}

/** Per-peer nickname and local picture (index.html Contact Settings). */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ContactSettingsScreen(vm: ChatViewModel, pk: String, onBack: () -> Unit) {
    val repo = vm.repo
    val names by repo.names.collectAsState()
    val local by repo.store.state.collectAsState()
    val avatars by repo.store.avatarsChanged.collectAsState()
    val scope = rememberCoroutineScope()
    var name by remember(pk) { mutableStateOf(names[pk].orEmpty()) }
    var offered by remember { mutableStateOf<ByteArray?>(null) }
    var asking by remember { mutableStateOf(false) }
    val saved = pk in local.contacts
    val pickAvatar = rememberAvatarPicker(vm, AvatarTarget.CONTACT) { bytes ->
        repo.store.setAvatar(pk, bytes)
        repo.note(R.string.chat_avatar_updated)
    }
    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_contact_settings), onBack = onBack) }) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            ChatAvatar(pk, name, 112.dp, version = avatars + (offered?.size ?: 0), load = { offered ?: repo.store.avatar(pk) })
            Spacer(Modifier.height(12.dp))
            SelectionContainer {
                Text(pk, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
            }
            Spacer(Modifier.height(16.dp))
            OutlinedTextField(
                value = name,
                onValueChange = { name = it.take(ChatRepository.NAME_MAX) },
                label = { Text(stringResource(R.string.chat_contact_name)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Text(
                stringResource(R.string.chat_contact_name_help),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 6.dp),
            )
            Spacer(Modifier.height(16.dp))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp), verticalArrangement = Arrangement.spacedBy(8.dp)) {
                Button(onClick = {
                    val had = names[pk].orEmpty()
                    repo.setName(pk, name)
                    val pic = offered
                    if (pic != null) {
                        repo.store.setAvatar(pk, pic)
                        offered = null
                    }
                    repo.note(
                        when {
                            name.isNotBlank() && pic != null -> R.string.chat_saved_as_with_picture
                            name.isBlank() && pic != null -> R.string.chat_picture_saved
                            name.isNotBlank() -> R.string.chat_saved_as
                            had.isNotEmpty() -> R.string.chat_name_cleared
                            else -> R.string.chat_enter_name_first
                        },
                        name.trim(),
                    )
                }) { Text(stringResource(R.string.chat_save_name)) }
                OutlinedButton(enabled = !asking, onClick = {
                    asking = true
                    repo.note(R.string.chat_asking_profile)
                    scope.launch {
                        val p = repo.lookupPeer(pk)
                        asking = false
                        when {
                            p == null || p.unreachable -> repo.note(R.string.chat_profile_unreachable, error = true)
                            p.name.isEmpty() && p.avatar == null -> repo.note(R.string.chat_profile_nothing, error = true)
                            else -> {
                                if (p.name.isNotEmpty()) name = p.name
                                offered = p.avatar
                                repo.note(
                                    when {
                                        p.name.isNotEmpty() && p.avatar != null -> R.string.chat_profile_offer_both
                                        p.name.isNotEmpty() -> R.string.chat_profile_offer_name
                                        else -> R.string.chat_profile_offer_picture
                                    },
                                    p.name,
                                )
                            }
                        }
                    }
                }) {
                    if (asking) CircularProgressIndicator(Modifier.padding(end = 8.dp).width(16.dp).height(16.dp), strokeWidth = 2.dp)
                    Text(stringResource(R.string.chat_get_published))
                }
                OutlinedButton(onClick = pickAvatar) { Text(stringResource(R.string.chat_change_avatar)) }
                OutlinedButton(onClick = {
                    if (!repo.store.hasAvatar(pk)) {
                        repo.note(R.string.chat_no_custom_avatar, error = true)
                    } else {
                        repo.store.setAvatar(pk, null)
                        repo.note(R.string.chat_avatar_removed)
                    }
                }) { Text(stringResource(R.string.chat_remove_avatar)) }
                if (saved) {
                    TextButton(onClick = { repo.removeContact(pk) }) {
                        Text(stringResource(R.string.chat_remove_from_contacts), color = MaterialTheme.colorScheme.error)
                    }
                } else {
                    TextButton(onClick = {
                        repo.addContact(pk)
                        repo.note(R.string.chat_saved_to_contacts)
                    }) { Text(stringResource(R.string.chat_save_to_contacts)) }
                }
            }
        }
    }
}

/** Add someone by key; their published name and picture are offered (index.html New contact). */
@Composable
fun NewContactScreen(vm: ChatViewModel, prefill: String, onBack: () -> Unit) {
    val repo = vm.repo
    var raw by remember { mutableStateOf(prefill) }
    var name by remember { mutableStateOf("") }
    var chosen by remember { mutableStateOf<ByteArray?>(null) }
    var lookup by remember { mutableStateOf<PeerLookup?>(null) }
    var looking by remember { mutableStateOf(false) }
    var retry by remember { mutableIntStateOf(0) }
    val pk = keyFrom(raw)
    val already = pk.isNotEmpty() && repo.isContact(pk)
    LaunchedEffect(pk, retry) {
        lookup = null
        if (pk.isEmpty() || already) return@LaunchedEffect
        looking = true
        delay(350)
        val p = repo.lookupPeer(pk)
        looking = false
        lookup = p
        if (p != null && !p.unreachable) {
            if (name.isEmpty() && p.name.isNotEmpty()) name = p.name
            if (chosen == null) chosen = p.avatar
        }
    }
    val pickAvatar = rememberAvatarPicker(vm, AvatarTarget.CONTACT) { chosen = it }
    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_new_contact), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(20.dp)) {
            OutlinedTextField(
                value = raw,
                onValueChange = { raw = it },
                label = { Text(stringResource(R.string.chat_their_key)) },
                modifier = Modifier.fillMaxWidth(),
                maxLines = 3,
            )
            Spacer(Modifier.height(8.dp))
            when {
                pk.isEmpty() -> Unit
                already -> Text(stringResource(R.string.chat_already_in_book), color = MaterialTheme.colorScheme.error, style = MaterialTheme.typography.bodySmall)
                looking -> Row(verticalAlignment = Alignment.CenterVertically) {
                    CircularProgressIndicator(Modifier.width(16.dp).height(16.dp), strokeWidth = 2.dp)
                    Spacer(Modifier.width(8.dp))
                    Text(stringResource(R.string.chat_looking_up), style = MaterialTheme.typography.bodySmall)
                }
                lookup?.unreachable == true -> Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(stringResource(R.string.chat_lookup_unreachable), style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
                    TextButton(onClick = { retry++ }) { Text(stringResource(R.string.chat_try_again)) }
                }
                lookup != null && lookup!!.name.isEmpty() && lookup!!.avatar == null ->
                    Text(stringResource(R.string.chat_publishes_nothing), style = MaterialTheme.typography.bodySmall)
                lookup != null -> Text(
                    stringResource(if (lookup!!.name.isNotEmpty()) R.string.chat_name_published else R.string.chat_picture_no_name),
                    style = MaterialTheme.typography.bodySmall,
                )
            }
            Spacer(Modifier.height(12.dp))
            Row(verticalAlignment = Alignment.CenterVertically) {
                ChatAvatar(pk.ifEmpty { "new" }, name, 64.dp, version = chosen?.size, load = { chosen })
                Spacer(Modifier.width(12.dp))
                OutlinedButton(onClick = pickAvatar) { Text(stringResource(R.string.chat_choose_picture)) }
            }
            Spacer(Modifier.height(12.dp))
            OutlinedTextField(
                value = name,
                onValueChange = { name = it.take(ChatRepository.NAME_MAX) },
                label = { Text(stringResource(R.string.chat_nickname)) },
                singleLine = true,
                keyboardOptions = KeyboardOptions(imeAction = ImeAction.Done),
                modifier = Modifier.fillMaxWidth(),
            )
            Text(
                stringResource(R.string.chat_new_contact_help),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 6.dp),
            )
            Spacer(Modifier.height(16.dp))
            Button(
                onClick = {
                    when {
                        pk.isEmpty() -> repo.note(R.string.chat_enter_valid_key, error = true)
                        already -> repo.note(R.string.chat_key_already_saved, error = true)
                        name.isBlank() && lookup?.name.isNullOrEmpty() -> repo.note(R.string.chat_give_nickname, error = true)
                        else -> {
                            val finalName = name.ifBlank { lookup?.name.orEmpty() }
                            repo.setName(pk, finalName)
                            repo.addContact(pk, finalName, chosen)
                            repo.note(R.string.chat_saved_to_contacts_named, finalName)
                            onBack()
                        }
                    }
                },
                modifier = Modifier.fillMaxWidth(),
            ) { Text(stringResource(R.string.chat_save_contact)) }
        }
    }
}

/** What this visor publishes about itself (index.html "Your profile"). */
@Composable
fun MyProfileScreen(vm: ChatViewModel, onBack: () -> Unit) {
    val repo = vm.repo
    val profile by repo.profile.collectAsState()
    val available by repo.profileAvailable.collectAsState()
    val myPk by repo.myPk.collectAsState()
    var name by remember(profile?.name) { mutableStateOf(profile?.name.orEmpty()) }
    var pending by remember { mutableStateOf<ByteArray?>(null) }
    var busy by remember { mutableStateOf(false) }
    var confirmStop by remember { mutableStateOf(false) }
    val pick = rememberAvatarPicker(vm, AvatarTarget.ME) {
        pending = it
        repo.note(R.string.chat_press_save)
    }
    val current = profile?.avatar.orEmpty()
    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_your_profile), onBack = onBack) }) { padding ->
        Column(
            Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(20.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            ChatAvatar(
                myPk.ifEmpty { "me" }, name, 112.dp,
                version = (pending?.size ?: 0) + current.hashCode(),
                load = { pending ?: decodeDataUrl(current) },
            )
            Spacer(Modifier.height(12.dp))
            SelectionContainer { Text(repo.myAddressText(), fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall) }
            Spacer(Modifier.height(16.dp))
            if (available == false) {
                Text(stringResource(R.string.chat_no_publish), style = MaterialTheme.typography.bodyMedium)
                return@Column
            }
            OutlinedTextField(
                value = name,
                onValueChange = { name = it.take(ChatRepository.NAME_MAX) },
                label = { Text(stringResource(R.string.chat_display_name)) },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            Text(
                stringResource(R.string.chat_profile_help),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 6.dp),
            )
            Spacer(Modifier.height(16.dp))
            Button(enabled = !busy, modifier = Modifier.fillMaxWidth(), onClick = {
                busy = true
                repo.saveProfile(name, pending) { ok ->
                    busy = false
                    if (ok) pending = null
                }
            }) { Text(stringResource(R.string.chat_save)) }
            Spacer(Modifier.height(8.dp))
            OutlinedButton(onClick = pick, modifier = Modifier.fillMaxWidth()) { Text(stringResource(R.string.chat_change_picture)) }
            if (current.isNotEmpty()) {
                Spacer(Modifier.height(8.dp))
                OutlinedButton(enabled = !busy, modifier = Modifier.fillMaxWidth(), onClick = {
                    busy = true
                    repo.removeProfilePicture { busy = false }
                }) { Text(stringResource(R.string.chat_remove_picture)) }
            }
            Spacer(Modifier.height(8.dp))
            TextButton(onClick = { confirmStop = true }) {
                Text(stringResource(R.string.chat_stop_publishing), color = MaterialTheme.colorScheme.error)
            }
        }
    }
    if (confirmStop) {
        ConfirmDialog(
            stringResource(R.string.chat_confirm_stop_publishing),
            stringResource(R.string.chat_stop_publishing),
            onConfirm = { repo.clearProfile() },
            onDismiss = { confirmStop = false },
        )
    }
}

private fun ChatRepository.myAddressText(): String =
    profile.value?.address?.takeIf { it.isNotEmpty() } ?: myPk.value.takeIf { it.isNotEmpty() }?.let { "skychat://$it" } ?: "—"

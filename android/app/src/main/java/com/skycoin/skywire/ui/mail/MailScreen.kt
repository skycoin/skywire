package com.skycoin.skywire.ui.mail

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.content.Intent
import androidx.activity.compose.BackHandler
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.Reply
import androidx.compose.material.icons.automirrored.rounded.Send
import androidx.compose.material.icons.rounded.AttachFile
import androidx.compose.material.icons.rounded.CheckCircle
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.Contacts
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.Delete
import androidx.compose.material.icons.rounded.Download
import androidx.compose.material.icons.rounded.Edit
import androidx.compose.material.icons.rounded.Settings
import androidx.compose.material.icons.rounded.Share
import androidx.compose.material.icons.rounded.Warning
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExtendedFloatingActionButton
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
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
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.api.MailFolder
import com.skycoin.skywire.api.MailSendResult
import com.skycoin.skywire.api.MailStatus
import com.skycoin.skywire.api.MailSummary
import com.skycoin.skywire.core.CoreState
import com.skycoin.skywire.core.DeepLinks
import com.skycoin.skywire.core.SkymailAddress
import com.skycoin.skywire.core.VoiceCallWatcher
import com.skycoin.skywire.core.VoiceCalls
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SectionCard
import com.skycoin.skywire.ui.components.SkyTopBar
import java.time.OffsetDateTime
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/**
 * Skymail: the visor's own mailbox, e-mail between visors over Skywire. One
 * screen with four views (the folders, a message, a draft, the settings), so
 * back steps out of the inner one first.
 */
@Composable
fun MailScreen(onBack: () -> Unit, viewModel: MailViewModel = viewModel()) {
    val state by viewModel.uiState.collectAsState()
    val snackbar = remember { SnackbarHostState() }
    LaunchedEffect(state.notice) {
        state.notice?.let {
            snackbar.showSnackbar(it)
            viewModel.noticeShown()
        }
    }
    val pending by DeepLinks.pendingMail.collectAsState()
    LaunchedEffect(pending) { if (pending != null) viewModel.onPendingLink() }

    val inner = state.draft != null || state.open != null || state.settingsOpen
    val stepBack: () -> Unit = {
        when {
            state.draft != null -> viewModel.discardDraft()
            state.settingsOpen -> viewModel.closeSettings()
            state.open != null -> viewModel.closeMessage()
            else -> onBack()
        }
    }
    BackHandler(enabled = inner) { stepBack() }

    val listView = !inner && state.coreReady
    Scaffold(
        topBar = {
            SkyTopBar(
                title = when {
                    state.draft != null -> stringResource(R.string.mail_compose_title)
                    state.settingsOpen -> stringResource(R.string.mail_settings_title)
                    else -> stringResource(R.string.app_skymail)
                },
                onBack = stepBack,
                help = HelpTopic(R.string.help_mail_title, R.string.help_mail_body),
                actions = {
                    if (listView) {
                        IconButton(onClick = viewModel::openSettings) {
                            Icon(Icons.Rounded.Settings, contentDescription = stringResource(R.string.mail_settings_title))
                        }
                    }
                },
            )
        },
        snackbarHost = { SnackbarHost(snackbar) },
        floatingActionButton = {
            if (listView && state.status?.running == true) {
                ExtendedFloatingActionButton(
                    onClick = { viewModel.compose() },
                    icon = { Icon(Icons.Rounded.Edit, contentDescription = null) },
                    text = { Text(stringResource(R.string.mail_write)) },
                )
            }
        },
    ) { padding ->
        Box(Modifier.fillMaxSize().padding(padding)) {
            when {
                !state.coreReady -> CoreNotReady(state.coreState)
                state.draft != null -> ComposeView(state, viewModel)
                state.settingsOpen -> SettingsView(state, viewModel)
                state.open != null -> MessageView(state, viewModel)
                else -> FolderView(state, viewModel)
            }
        }
    }
}

@Composable
private fun CoreNotReady(core: CoreState) {
    Box(Modifier.fillMaxSize().padding(24.dp), contentAlignment = Alignment.Center) {
        Text(
            stringResource(if (core is CoreState.Stopped) R.string.socks_core_offline else R.string.socks_core_starting),
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

// --- the folders ---

@Composable
private fun FolderView(state: MailUiState, viewModel: MailViewModel) {
    val status = state.status
    if (status == null) {
        Box(Modifier.fillMaxSize(), contentAlignment = Alignment.Center) { CircularProgressIndicator() }
        return
    }
    val messages = state.messages
    LazyColumn(
        contentPadding = PaddingValues(start = 16.dp, end = 16.dp, top = 4.dp, bottom = 96.dp),
        verticalArrangement = Arrangement.spacedBy(8.dp),
    ) {
        item { if (status.running) AddressCard(state.myAddress) else MailboxOffCard(status, state.busy, viewModel) }
        if (status.running) {
            item { FolderTabs(state.folder, status.unread, viewModel::selectFolder) }
            when {
                messages == null -> item {
                    Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
                        CircularProgressIndicator()
                    }
                }
                messages.isEmpty() -> item {
                    Text(
                        stringResource(if (state.folder == MailFolder.SENT) R.string.mail_empty_sent else R.string.mail_empty_inbox),
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(vertical = 24.dp, horizontal = 4.dp),
                    )
                }
                else -> items(messages, key = { it.id }) { m ->
                    MailRow(m, sent = state.folder == MailFolder.SENT) { viewModel.open(state.folder, m) }
                }
            }
        }
    }
}

@Composable
private fun AddressCard(address: String?) {
    val context = LocalContext.current
    SectionCard {
        Text(stringResource(R.string.mail_your_address), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(6.dp))
        SelectionContainer {
            Text(
                address.orEmpty(),
                style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            )
        }
        Spacer(Modifier.height(6.dp))
        Text(
            stringResource(R.string.mail_address_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (address != null) {
            Spacer(Modifier.height(8.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                FilledTonalButton(onClick = { copy(context, address) }) {
                    Icon(Icons.Rounded.ContentCopy, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(stringResource(R.string.mail_copy))
                }
                FilledTonalButton(onClick = { share(context, address) }) {
                    Icon(Icons.Rounded.Share, contentDescription = null, modifier = Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(stringResource(R.string.mail_share))
                }
            }
        }
    }
}

@Composable
private fun MailboxOffCard(status: MailStatus, busy: Boolean, viewModel: MailViewModel) {
    SectionCard {
        Text(stringResource(R.string.mail_off_title), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(6.dp))
        Text(
            if (status.enabled && status.reason.isNotEmpty()) {
                stringResource(R.string.mail_not_running, status.reason)
            } else {
                stringResource(R.string.mail_off_body)
            },
            style = MaterialTheme.typography.bodyMedium,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (!status.enabled) {
            Spacer(Modifier.height(10.dp))
            Button(onClick = { viewModel.setEnabled(true) }, enabled = !busy) {
                Text(stringResource(R.string.mail_turn_on))
            }
        }
    }
}

@Composable
private fun FolderTabs(folder: String, unread: Int, onSelect: (String) -> Unit) {
    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(top = 4.dp)) {
        SegmentedButton(
            selected = folder == MailFolder.INBOX,
            onClick = { onSelect(MailFolder.INBOX) },
            shape = SegmentedButtonDefaults.itemShape(0, 2),
        ) {
            Text(if (unread > 0) stringResource(R.string.mail_inbox_unread, unread) else stringResource(R.string.mail_inbox))
        }
        SegmentedButton(
            selected = folder == MailFolder.SENT,
            onClick = { onSelect(MailFolder.SENT) },
            shape = SegmentedButtonDefaults.itemShape(1, 2),
        ) {
            Text(stringResource(R.string.mail_sent_folder))
        }
    }
}

@Composable
private fun MailRow(m: MailSummary, sent: Boolean, onClick: () -> Unit) {
    val unread = !sent && !m.seen
    Row(
        verticalAlignment = Alignment.CenterVertically,
        modifier = Modifier
            .fillMaxWidth()
            .clickable(onClick = onClick)
            .padding(vertical = 8.dp, horizontal = 4.dp),
    ) {
        Box(
            Modifier
                .size(8.dp)
                .background(
                    if (unread) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.surface,
                    CircleShape,
                ),
        )
        Spacer(Modifier.width(10.dp))
        Column(Modifier.weight(1f)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Text(
                    displayAddress(if (sent) m.to else m.from),
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = if (unread) FontWeight.Bold else FontWeight.Normal,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f, fill = false),
                )
                if (!sent && !m.fromVerified) {
                    Spacer(Modifier.width(6.dp))
                    Icon(
                        Icons.Rounded.Warning,
                        contentDescription = stringResource(R.string.mail_unverified),
                        tint = MaterialTheme.colorScheme.error,
                        modifier = Modifier.size(14.dp),
                    )
                }
            }
            Text(
                m.subject.ifEmpty { stringResource(R.string.mail_no_subject) },
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = if (unread) FontWeight.SemiBold else FontWeight.Normal,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
        }
        Spacer(Modifier.width(8.dp))
        Text(
            shortDate(m.date),
            style = MaterialTheme.typography.labelSmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
    HorizontalDivider()
}

// --- a message ---

@Composable
private fun MessageView(state: MailUiState, viewModel: MailViewModel) {
    val open = state.open ?: return
    val m = open.message
    var confirmDelete by remember { mutableStateOf(false) }
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
    ) {
        Text(
            (m?.subject ?: open.summary.subject).ifEmpty { stringResource(R.string.mail_no_subject) },
            style = MaterialTheme.typography.titleLarge,
        )
        Spacer(Modifier.height(10.dp))
        when {
            open.error != null -> Text(open.error, color = MaterialTheme.colorScheme.error)
            m == null -> CircularProgressIndicator()
            else -> {
                HeaderLine(R.string.mail_from, m.from)
                if (open.folder == MailFolder.INBOX) VerifiedLine(m.fromVerified, m.peerPk)
                HeaderLine(R.string.mail_to, m.to)
                if (m.cc.isNotEmpty()) HeaderLine(R.string.mail_cc, m.cc)
                HeaderLine(R.string.mail_date, m.date)
                Spacer(Modifier.height(12.dp))
                HorizontalDivider()
                Spacer(Modifier.height(12.dp))
                if (m.fromHtml) {
                    Text(
                        stringResource(R.string.mail_from_html),
                        style = MaterialTheme.typography.labelSmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.height(6.dp))
                }
                SelectionContainer { Text(m.text.trimEnd(), style = MaterialTheme.typography.bodyLarge) }
                if (m.attachments.isNotEmpty()) {
                    Spacer(Modifier.height(16.dp))
                    Text(stringResource(R.string.mail_attachments), style = MaterialTheme.typography.titleSmall)
                    m.attachments.forEachIndexed { i, a ->
                        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
                            Icon(Icons.Rounded.AttachFile, contentDescription = null, modifier = Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text(
                                "${a.name.ifEmpty { "attachment" }} · ${formatBytes(a.size)}",
                                style = MaterialTheme.typography.bodyMedium,
                                maxLines = 1,
                                overflow = TextOverflow.Ellipsis,
                                modifier = Modifier.weight(1f),
                            )
                            TextButton(onClick = { viewModel.saveAttachment(i) }) {
                                Icon(Icons.Rounded.Download, contentDescription = null, modifier = Modifier.size(18.dp))
                                Spacer(Modifier.width(4.dp))
                                Text(stringResource(R.string.mail_save))
                            }
                        }
                    }
                }
                Spacer(Modifier.height(20.dp))
                Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    if (open.folder == MailFolder.INBOX) {
                        Button(onClick = viewModel::reply) {
                            Icon(Icons.AutoMirrored.Rounded.Reply, contentDescription = null, modifier = Modifier.size(18.dp))
                            Spacer(Modifier.width(6.dp))
                            Text(stringResource(R.string.mail_reply))
                        }
                    }
                    OutlinedButton(onClick = { confirmDelete = true }, enabled = !state.busy) {
                        Icon(Icons.Rounded.Delete, contentDescription = null, modifier = Modifier.size(18.dp))
                        Spacer(Modifier.width(6.dp))
                        Text(stringResource(R.string.mail_delete))
                    }
                }
            }
        }
    }
    if (confirmDelete) {
        AlertDialog(
            onDismissRequest = { confirmDelete = false },
            text = { Text(stringResource(R.string.mail_delete_confirm)) },
            confirmButton = {
                TextButton(onClick = {
                    confirmDelete = false
                    viewModel.delete()
                }) { Text(stringResource(R.string.mail_delete)) }
            },
            dismissButton = {
                TextButton(onClick = { confirmDelete = false }) { Text(stringResource(android.R.string.cancel)) }
            },
        )
    }
}

@Composable
private fun HeaderLine(label: Int, value: String) {
    Row(Modifier.padding(vertical = 2.dp)) {
        Text(
            stringResource(label),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.width(56.dp),
        )
        SelectionContainer {
            Text(value, style = MaterialTheme.typography.bodySmall)
        }
    }
}

/** What the transport proved about the sender: the From line can be typed by anyone. */
@Composable
private fun VerifiedLine(verified: Boolean, peerPk: String) {
    Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.padding(start = 56.dp, bottom = 4.dp)) {
        Icon(
            if (verified) Icons.Rounded.CheckCircle else Icons.Rounded.Warning,
            contentDescription = null,
            tint = if (verified) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error,
            modifier = Modifier.size(14.dp),
        )
        Spacer(Modifier.width(4.dp))
        Text(
            if (verified) {
                stringResource(R.string.mail_verified_line)
            } else {
                stringResource(R.string.mail_unverified_line, VoiceCallWatcher.shortPk(peerPk))
            },
            style = MaterialTheme.typography.labelSmall,
            color = if (verified) MaterialTheme.colorScheme.onSurfaceVariant else MaterialTheme.colorScheme.error,
        )
    }
}

// --- a draft ---

@Composable
private fun ComposeView(state: MailUiState, viewModel: MailViewModel) {
    val draft = state.draft ?: return
    var showCc by rememberSaveable { mutableStateOf(draft.cc.isNotEmpty()) }
    var pickContact by remember { mutableStateOf(false) }
    val book by VoiceCalls.addressBook.collectAsState()
    val picker = rememberLauncherForActivityResult(ActivityResultContracts.OpenMultipleDocuments()) { uris ->
        viewModel.addAttachments(uris)
    }
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(10.dp),
    ) {
        Row(verticalAlignment = Alignment.CenterVertically) {
            OutlinedTextField(
                value = draft.to,
                onValueChange = { v -> viewModel.updateDraft { it.copy(to = v) } },
                label = { Text(stringResource(R.string.mail_to)) },
                supportingText = { Text(stringResource(R.string.mail_to_hint)) },
                modifier = Modifier.weight(1f),
            )
            if (book.isNotEmpty()) {
                Box {
                    IconButton(onClick = { pickContact = true }) {
                        Icon(Icons.Rounded.Contacts, contentDescription = stringResource(R.string.mail_contacts))
                    }
                    DropdownMenu(expanded = pickContact, onDismissRequest = { pickContact = false }) {
                        book.entries.sortedBy { it.value.lowercase() }.forEach { (pk, name) ->
                            DropdownMenuItem(
                                text = { Text(name) },
                                onClick = {
                                    pickContact = false
                                    val addr = SkymailAddress.of(pk) ?: return@DropdownMenuItem
                                    viewModel.updateDraft { d ->
                                        d.copy(to = listOf(d.to.trim().trimEnd(','), addr).filter { it.isNotEmpty() }.joinToString(", "))
                                    }
                                },
                            )
                        }
                    }
                }
            }
        }
        if (showCc) {
            OutlinedTextField(
                value = draft.cc,
                onValueChange = { v -> viewModel.updateDraft { it.copy(cc = v) } },
                label = { Text(stringResource(R.string.mail_cc)) },
                modifier = Modifier.fillMaxWidth(),
            )
        } else {
            TextButton(onClick = { showCc = true }) { Text(stringResource(R.string.mail_add_cc)) }
        }
        OutlinedTextField(
            value = draft.subject,
            onValueChange = { v -> viewModel.updateDraft { it.copy(subject = v) } },
            label = { Text(stringResource(R.string.mail_subject)) },
            singleLine = true,
            modifier = Modifier.fillMaxWidth(),
        )
        OutlinedTextField(
            value = draft.body,
            onValueChange = { v -> viewModel.updateDraft { it.copy(body = v) } },
            label = { Text(stringResource(R.string.mail_body)) },
            minLines = 6,
            modifier = Modifier.fillMaxWidth(),
        )
        draft.attachments.forEachIndexed { i, a ->
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(Icons.Rounded.AttachFile, contentDescription = null, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(6.dp))
                Text(
                    "${a.name} · ${formatBytes(a.bytes.size.toLong())}",
                    style = MaterialTheme.typography.bodyMedium,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                    modifier = Modifier.weight(1f),
                )
                IconButton(onClick = { viewModel.removeAttachment(i) }) {
                    Icon(Icons.Rounded.Close, contentDescription = stringResource(R.string.mail_remove))
                }
            }
        }
        OutlinedButton(onClick = { picker.launch(arrayOf("*/*")) }, enabled = !state.sending) {
            Icon(Icons.Rounded.AttachFile, contentDescription = null, modifier = Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text(stringResource(R.string.mail_attach))
        }
        state.lastSend?.let { SendResultCard(it) }
        Text(
            stringResource(R.string.mail_send_note),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Button(onClick = viewModel::send, enabled = !state.sending, modifier = Modifier.fillMaxWidth()) {
            if (state.sending) {
                CircularProgressIndicator(Modifier.size(18.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(8.dp))
                Text(stringResource(R.string.mail_sending))
            } else {
                Icon(Icons.AutoMirrored.Rounded.Send, contentDescription = null, modifier = Modifier.size(18.dp))
                Spacer(Modifier.width(8.dp))
                Text(stringResource(R.string.mail_send))
            }
        }
    }
}

@Composable
private fun SendResultCard(result: MailSendResult) {
    SectionCard {
        Text(stringResource(R.string.mail_result_title), style = MaterialTheme.typography.titleSmall)
        result.recipients.forEach { r ->
            Spacer(Modifier.height(6.dp))
            Text(displayAddress(r.rcpt), style = MaterialTheme.typography.bodyMedium)
            Text(
                if (r.error.isEmpty()) {
                    stringResource(R.string.mail_result_delivered, r.via)
                } else {
                    stringResource(R.string.mail_result_failed, r.error)
                },
                style = MaterialTheme.typography.bodySmall,
                color = if (r.error.isEmpty()) MaterialTheme.colorScheme.primary else MaterialTheme.colorScheme.error,
            )
        }
        if (result.recipients.isEmpty() && result.error.isNotEmpty()) {
            Spacer(Modifier.height(6.dp))
            Text(result.error, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
        }
    }
}

// --- settings ---

@Composable
private fun SettingsView(state: MailUiState, viewModel: MailViewModel) {
    val status = state.status ?: return
    var entry by rememberSaveable { mutableStateOf("") }
    var entryBad by remember { mutableStateOf(false) }
    Column(
        Modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(16.dp),
        verticalArrangement = Arrangement.spacedBy(12.dp),
    ) {
        SectionCard {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Column(Modifier.weight(1f)) {
                    Text(stringResource(R.string.mail_setting_on), style = MaterialTheme.typography.titleMedium)
                    Text(
                        stringResource(R.string.mail_setting_on_desc),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Switch(checked = status.enabled, onCheckedChange = viewModel::setEnabled, enabled = !state.busy)
            }
            if (status.enabled && !status.running && status.reason.isNotEmpty()) {
                Spacer(Modifier.height(6.dp))
                Text(
                    stringResource(R.string.mail_not_running, status.reason),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.error,
                )
            }
        }
        // The visor counts usage only while the mailbox runs.
        if (status.running) SectionCard {
            val total = status.limits.maxTotalSize
            Text(
                stringResource(R.string.mail_usage, formatBytes(status.usage), formatBytes(total)),
                style = MaterialTheme.typography.bodyMedium,
            )
            if (total > 0) {
                Spacer(Modifier.height(6.dp))
                LinearProgressIndicator(
                    progress = { (status.usage.toFloat() / total).coerceIn(0f, 1f) },
                    modifier = Modifier.fillMaxWidth(),
                )
            }
            Spacer(Modifier.height(6.dp))
            Text(
                stringResource(
                    R.string.mail_limits,
                    formatBytes(status.limits.maxMessageSize),
                    (status.limits.maxAgeNanos / NANOS_PER_DAY).toInt(),
                ),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
        SectionCard {
            Text(stringResource(R.string.mail_allowed_title), style = MaterialTheme.typography.titleMedium)
            Spacer(Modifier.height(4.dp))
            Text(
                stringResource(if (status.whitelist.isEmpty()) R.string.mail_allowed_everyone else R.string.mail_allowed_only),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            status.whitelist.forEach { pk ->
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        VoiceCalls.displayName(pk),
                        style = MaterialTheme.typography.bodyMedium,
                        modifier = Modifier.weight(1f),
                    )
                    TextButton(onClick = { viewModel.removeSender(pk) }, enabled = !state.busy) {
                        Text(stringResource(R.string.mail_remove))
                    }
                }
            }
            Spacer(Modifier.height(6.dp))
            OutlinedTextField(
                value = entry,
                onValueChange = {
                    entry = it
                    entryBad = false
                },
                label = { Text(stringResource(R.string.mail_allow_hint)) },
                isError = entryBad,
                supportingText = if (entryBad) {
                    { Text(stringResource(R.string.mail_allow_bad)) }
                } else {
                    null
                },
                singleLine = true,
                modifier = Modifier.fillMaxWidth(),
            )
            TextButton(
                onClick = {
                    if (viewModel.allowSender(entry)) entry = "" else entryBad = true
                },
                enabled = entry.isNotBlank() && !state.busy,
            ) { Text(stringResource(R.string.mail_allow_add)) }
        }
    }
}

// --- helpers ---

/**
 * An address short enough to read: the display name when there is one, a
 * SkyChat contact's name for the key, or the address with its key cut.
 */
private fun displayAddress(raw: String): String {
    val text = raw.trim()
    val name = text.substringBefore('<', "").trim().trim('"')
    if (name.isNotEmpty() && '<' in text) return name
    val addr = text.substringAfter('<').substringBefore('>').trim()
    SkymailAddress.pkOfAddress(addr)?.let { pk ->
        val named = VoiceCalls.displayName(pk)
        if (named != VoiceCallWatcher.shortPk(pk)) return named
    }
    val local = addr.substringBefore('@', addr)
    val domain = addr.substringAfter('@', "")
    if (domain.length <= 16) return addr
    return "$local@${domain.take(8)}…${domain.substring(domain.lastIndexOf('.'))}"
}

private fun shortDate(rfc3339: String): String = runCatching {
    val d = OffsetDateTime.parse(rfc3339)
    if (d.year < 1970) return "" // Go's zero time: no Date header
    d.toLocalDate().format(DateTimeFormatter.ofLocalizedDate(FormatStyle.SHORT))
}.getOrDefault("")

private fun copy(context: Context, text: String) {
    val clipboard = context.getSystemService(ClipboardManager::class.java)
    clipboard?.setPrimaryClip(ClipData.newPlainText("Skymail", text))
}

private fun share(context: Context, text: String) {
    val send = Intent(Intent.ACTION_SEND).setType("text/plain").putExtra(Intent.EXTRA_TEXT, text)
    context.startActivity(Intent.createChooser(send, null))
}

private const val NANOS_PER_DAY = 86_400_000_000_000L

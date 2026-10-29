package com.skycoin.skywire.ui.chat

import android.Manifest
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.MediaPlayer
import android.net.Uri
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.selection.toggleable
import androidx.compose.ui.semantics.Role
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
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
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.RingbackInfo
import com.skycoin.skywire.chat.clearRingback
import com.skycoin.skywire.chat.exportArchive
import com.skycoin.skywire.chat.humanSize
import com.skycoin.skywire.chat.importArchive
import com.skycoin.skywire.chat.ringbackInfo
import com.skycoin.skywire.chat.setApproval
import com.skycoin.skywire.chat.setBlocked
import com.skycoin.skywire.chat.setRingback
import com.skycoin.skywire.ui.components.SkyTopBar
import kotlinx.coroutines.launch

@OptIn(ExperimentalLayoutApi::class)
@Composable
fun ChatSettingsScreen(vm: ChatViewModel, onBack: () -> Unit) {
    val repo = vm.repo
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val notif by repo.store.notif.collectAsState()
    val privacy by repo.privacy.collectAsState()
    val groups by repo.groups.collectAsState()
    val voice by repo.voiceAvailable.collectAsState()

    var permTick by remember { mutableIntStateOf(0) }
    val needsPermission = Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU
    val allowed = remember(permTick) {
        !needsPermission || ContextCompat.checkSelfPermission(context, Manifest.permission.POST_NOTIFICATIONS) == PackageManager.PERMISSION_GRANTED
    }
    val askPermission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { permTick++ }

    var ring by remember { mutableStateOf<RingbackInfo?>(null) }
    var ringError by remember { mutableStateOf(false) }
    var ringStatus by remember { mutableStateOf<String?>(null) }
    var ringPlayer by remember { mutableStateOf<MediaPlayer?>(null) }
    suspend fun reloadRing() {
        val info = repo.ringbackInfo()
        ring = info
        ringError = info == null
    }
    LaunchedEffect(voice) { if (voice) reloadRing() }
    DisposableEffect(Unit) { onDispose { ringPlayer?.release() } }
    val pickRing = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri: Uri? ->
        uri ?: return@rememberLauncherForActivityResult
        ringStatus = context.getString(R.string.chat_ring_saving)
        scope.launch {
            val err = repo.setRingback(uri)
            ringStatus = if (err == null) context.getString(R.string.chat_ring_saved) else context.getString(R.string.chat_ring_save_failed, err)
            reloadRing()
        }
    }

    var xfer by remember { mutableStateOf<String?>(null) }
    val pickArchive = rememberLauncherForActivityResult(ActivityResultContracts.GetContent()) { uri: Uri? ->
        uri ?: return@rememberLauncherForActivityResult
        xfer = context.getString(R.string.chat_import_reading)
        scope.launch { xfer = repo.importArchive(uri) }
    }

    Scaffold(topBar = { SkyTopBar(title = stringResource(R.string.chat_settings), onBack = onBack) }) { padding ->
        Column(Modifier.fillMaxSize().padding(padding).verticalScroll(rememberScrollState()).padding(horizontal = 20.dp, vertical = 8.dp)) {
            Section(R.string.chat_notifications)
            Text(
                stringResource(if (allowed) R.string.chat_notif_allowed else R.string.chat_notif_not_allowed),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            if (!allowed) {
                TextButton(onClick = { askPermission.launch(Manifest.permission.POST_NOTIFICATIONS) }) {
                    Text(stringResource(R.string.chat_allow_notifications))
                }
            }
            SwitchRow(R.string.chat_notif_enable, R.string.chat_notif_enable_sub, notif.enabled) { on ->
                repo.store.updateNotif { it.copy(enabled = on) }
            }
            SwitchRow(R.string.chat_notif_preview, R.string.chat_notif_preview_sub, notif.preview, enabled = notif.enabled) { on ->
                repo.store.updateNotif { it.copy(preview = on) }
            }
            Text(stringResource(R.string.chat_muted_list), style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 12.dp))
            if (notif.muted.isEmpty()) {
                Text(stringResource(R.string.chat_no_muted), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
            for (key in notif.muted.sorted()) {
                val label = groups.firstOrNull { it.id == key }?.name?.ifEmpty { key } ?: repo.displayName(key)
                Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                    Text(label, Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                    TextButton(onClick = { repo.store.updateNotif { n -> n.copy(muted = n.muted - key) } }) { Text(stringResource(R.string.chat_unmute)) }
                }
            }

            if (voice) {
                Divider()
                Section(R.string.chat_ringback)
                Text(stringResource(R.string.chat_ringback_sub), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                Text(
                    when {
                        ringError -> stringResource(R.string.chat_ring_unreadable)
                        ring?.set == true -> stringResource(R.string.chat_ring_yours, humanSize(ring!!.size), ring!!.type)
                        else -> stringResource(R.string.chat_ring_plain)
                    },
                    style = MaterialTheme.typography.bodyMedium,
                    modifier = Modifier.padding(vertical = 6.dp),
                )
                FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                    OutlinedButton(onClick = { pickRing.launch("audio/*") }) { Text(stringResource(R.string.chat_ring_choose)) }
                    OutlinedButton(enabled = ring?.set == true, onClick = {
                        val p = ringPlayer
                        if (p != null) {
                            p.release()
                            ringPlayer = null
                            return@OutlinedButton
                        }
                        scope.launch {
                            val mp = MediaPlayer()
                            runCatching {
                                mp.setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_MEDIA).build())
                                mp.setDataSource(
                                    context,
                                    Uri.parse(repo.http.url("/voice/my-ringback?t=${System.currentTimeMillis()}")),
                                    mapOf("Authorization" to repo.http.authorization()),
                                )
                                mp.setOnCompletionListener {
                                    it.release()
                                    ringPlayer = null
                                }
                                mp.prepare()
                                mp.start()
                                ringPlayer = mp
                            }.onFailure {
                                mp.release()
                                ringStatus = context.getString(R.string.chat_ring_cannot_play)
                            }
                        }
                    }) { Text(stringResource(if (ringPlayer != null) R.string.chat_stop else R.string.chat_play)) }
                    OutlinedButton(enabled = ring?.set == true, onClick = {
                        scope.launch {
                            val err = repo.clearRingback()
                            ringStatus = if (err == null) context.getString(R.string.chat_ring_cleared) else context.getString(R.string.chat_ring_clear_failed, err)
                            reloadRing()
                        }
                    }) { Text(stringResource(R.string.chat_ring_plain_button)) }
                }
                ringStatus?.let { Text(it, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 4.dp)) }
            }

            privacy?.let { p ->
                Divider()
                Section(R.string.chat_privacy)
                SwitchRow(R.string.chat_approval, R.string.chat_approval_sub, p.approval) { repo.setApproval(it) }
                Text(stringResource(R.string.chat_blocked), style = MaterialTheme.typography.titleSmall, modifier = Modifier.padding(top = 12.dp))
                if (p.blocked.isEmpty()) {
                    Text(stringResource(R.string.chat_nobody_blocked), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
                }
                for (pk in p.blocked) {
                    Row(Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
                        Text(repo.displayName(pk), Modifier.weight(1f), maxLines = 1, overflow = TextOverflow.Ellipsis)
                        TextButton(onClick = { repo.setBlocked(pk, false) }) { Text(stringResource(R.string.chat_unblock)) }
                    }
                }
            }

            Divider()
            Section(R.string.chat_transfer)
            Text(stringResource(R.string.chat_transfer_help), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            Spacer(Modifier.height(8.dp))
            FlowRow(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
                OutlinedButton(onClick = {
                    xfer = context.getString(R.string.chat_export_preparing)
                    repo.exportArchive()
                }) { Text(stringResource(R.string.chat_export)) }
                OutlinedButton(onClick = { pickArchive.launch("application/json") }) { Text(stringResource(R.string.chat_import)) }
            }
            xfer?.let { Text(it, style = MaterialTheme.typography.bodySmall, modifier = Modifier.padding(top = 6.dp)) }
            Text(
                stringResource(R.string.chat_transfer_keys),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
                modifier = Modifier.padding(top = 8.dp, bottom = 24.dp),
            )
        }
    }
}

@Composable
private fun Section(title: Int) {
    Text(
        stringResource(title),
        style = MaterialTheme.typography.titleMedium,
        fontWeight = FontWeight.SemiBold,
        color = MaterialTheme.colorScheme.primary,
        modifier = Modifier.padding(top = 12.dp, bottom = 6.dp),
    )
}

@Composable
private fun Divider() {
    HorizontalDivider(Modifier.padding(vertical = 12.dp), color = MaterialTheme.colorScheme.outlineVariant)
}

@Composable
fun SwitchRow(title: Int, sub: Int?, checked: Boolean, enabled: Boolean = true, onChange: (Boolean) -> Unit) {
    Row(
        Modifier.fillMaxWidth().toggleable(value = checked, enabled = enabled, role = Role.Switch, onValueChange = onChange).padding(vertical = 6.dp),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        Column(Modifier.weight(1f)) {
            Text(stringResource(title), style = MaterialTheme.typography.bodyLarge)
            sub?.let { Text(stringResource(it), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant) }
        }
        Switch(checked = checked, onCheckedChange = null, enabled = enabled)
    }
}

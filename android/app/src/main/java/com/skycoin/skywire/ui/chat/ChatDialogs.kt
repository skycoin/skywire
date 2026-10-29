package com.skycoin.skywire.ui.chat

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.graphics.Bitmap
import android.net.Uri
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.PickVisualMediaRequest
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.Image
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.chat.ChatImages
import com.skycoin.skywire.chat.inviteLink
import com.skycoin.skywire.ui.wallet.QrImage
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext

/** An address to show as a QR code; [inviteGid] offers the invite link instead (admins). */
data class QrSpec(
    val address: String,
    val title: Int,
    val help: Int,
    val titleArg: String? = null,
    val inviteGid: String? = null,
)

fun copyText(context: Context, text: String) {
    val cm = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    cm.setPrimaryClip(ClipData.newPlainText("skychat", text))
}

@Composable
fun QrDialog(spec: QrSpec, vm: ChatViewModel, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(if (spec.titleArg != null) stringResource(spec.title, spec.titleArg) else stringResource(spec.title)) },
        text = {
            Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
                if (spec.address.length <= QR_MAX) {
                    QrImage(spec.address, sizeDp = 232)
                } else {
                    Text(stringResource(R.string.chat_qr_too_long), style = MaterialTheme.typography.bodySmall)
                }
                Spacer(Modifier.height(12.dp))
                Row(verticalAlignment = Alignment.CenterVertically) {
                    SelectionContainer(Modifier.weight(1f)) {
                        Text(spec.address, fontFamily = FontFamily.Monospace, style = MaterialTheme.typography.bodySmall)
                    }
                    IconButton(onClick = {
                        copyText(context, spec.address)
                        vm.repo.note(R.string.chat_address_copied)
                    }) { Icon(Icons.Rounded.ContentCopy, stringResource(R.string.chat_copy_address)) }
                }
                Spacer(Modifier.height(8.dp))
                Text(stringResource(spec.help), style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.chat_close)) } },
        dismissButton = spec.inviteGid?.let { gid ->
            {
                TextButton(onClick = {
                    scope.launch {
                        vm.repo.inviteLink(gid)?.let {
                            copyText(context, it)
                            vm.repo.note(R.string.chat_invite_copied)
                        }
                    }
                }) { Text(stringResource(R.string.chat_copy_invite_instead)) }
            }
        },
    )
}

/** Yes/no before something that cannot be undone. */
@Composable
fun ConfirmDialog(text: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit, danger: Boolean = true) {
    AlertDialog(
        onDismissRequest = onDismiss,
        text = { Text(text) },
        confirmButton = {
            TextButton(onClick = {
                onDismiss()
                onConfirm()
            }) {
                Text(confirm, color = if (danger) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.primary)
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.chat_cancel)) } },
    )
}

/** Which picture an avatar pick is for: the published one, a contact's local one, or a group's. */
enum class AvatarTarget(val maxBytes: Int, val caption: Int) {
    ME(32 * 1024, R.string.chat_avatar_caption_me),
    CONTACT(64 * 1024, R.string.chat_avatar_caption_contact),
    GROUP(32 * 1024, R.string.chat_avatar_caption_group),
}

/**
 * Pick → centre-crop to 256×256 → encode under the target's byte cap →
 * "Use this picture?" (index.html _pickAvatar / _cropToSquare / confirmAvatar).
 * Returns the launcher; [onPicked] gets the encoded bytes after the confirm.
 */
@Composable
fun rememberAvatarPicker(vm: ChatViewModel, target: AvatarTarget, onPicked: (ByteArray) -> Unit): () -> Unit {
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var staged by remember { mutableStateOf<Pair<ByteArray, Bitmap>?>(null) }
    val launcher = rememberLauncherForActivityResult(ActivityResultContracts.PickVisualMedia()) { uri: Uri? ->
        if (uri == null) return@rememberLauncherForActivityResult
        scope.launch {
            val result = withContext(Dispatchers.Default) {
                val size = runCatching { context.contentResolver.openAssetFileDescriptor(uri, "r")?.use { it.length } }.getOrNull() ?: 0L
                if (size > 12L * 1024 * 1024) return@withContext R.string.chat_avatar_too_large
                val bmp = ChatImages.decodeUri(context, uri, 1024) ?: return@withContext R.string.chat_avatar_unreadable
                val bytes = ChatImages.encodeAvatar(bmp, 256, target.maxBytes) ?: return@withContext R.string.chat_avatar_too_complex
                val preview = ChatImages.decode(bytes, 256) ?: return@withContext R.string.chat_avatar_unreadable
                bytes to preview
            }
            @Suppress("UNCHECKED_CAST")
            when (result) {
                is Int -> vm.repo.note(result, error = true)
                else -> staged = result as Pair<ByteArray, Bitmap>
            }
        }
    }
    staged?.let { (bytes, bmp) ->
        AlertDialog(
            onDismissRequest = { staged = null },
            title = { Text(stringResource(R.string.chat_avatar_confirm)) },
            text = {
                Column(horizontalAlignment = Alignment.CenterHorizontally, modifier = Modifier.fillMaxWidth()) {
                    Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.Center) {
                        Image(bmp.asImageBitmap(), null, Modifier.size(120.dp).clip(CircleShape))
                        Spacer(Modifier.width(16.dp))
                        Image(bmp.asImageBitmap(), null, Modifier.size(44.dp).clip(CircleShape))
                    }
                    Spacer(Modifier.height(12.dp))
                    Text(stringResource(target.caption, "256×256"), style = MaterialTheme.typography.bodySmall)
                }
            },
            confirmButton = {
                TextButton(onClick = {
                    staged = null
                    onPicked(bytes)
                }) { Text(stringResource(R.string.chat_use_it)) }
            },
            dismissButton = { TextButton(onClick = { staged = null }) { Text(stringResource(R.string.chat_cancel)) } },
        )
    }
    return { launcher.launch(PickVisualMediaRequest(ActivityResultContracts.PickVisualMedia.ImageOnly)) }
}

/** Past this a code is too dense for a phone camera at this size. */
private const val QR_MAX = 600

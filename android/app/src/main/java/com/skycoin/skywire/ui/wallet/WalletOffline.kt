package com.skycoin.skywire.ui.wallet

import android.content.Intent
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.text.selection.SelectionContainer
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import kotlinx.coroutines.launch

enum class OfflineMode { SIGN, BROADCAST }

/** A raw transaction to carry somewhere else: selectable, copyable, shareable. */
@Composable
fun RawTxDialog(title: String, body: String, hex: String, onDismiss: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    var copied by remember { mutableStateOf(false) }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column {
                Text(body, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
                HexBlock(hex)
                if (copied) {
                    Text(
                        stringResource(R.string.wallet_offline_copied),
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.primary,
                        modifier = Modifier.padding(top = 8.dp),
                    )
                }
            }
        },
        confirmButton = {
            Row(horizontalArrangement = Arrangement.spacedBy(4.dp)) {
                TextButton(onClick = {
                    val send = Intent(Intent.ACTION_SEND).apply {
                        type = "text/plain"
                        putExtra(Intent.EXTRA_TEXT, hex)
                    }
                    context.startActivity(Intent.createChooser(send, null))
                }) { Text(stringResource(R.string.wallet_share)) }
                TextButton(onClick = {
                    clipboard.setText(AnnotatedString(hex))
                    copied = true
                }) { Text(stringResource(R.string.wallet_copy), fontWeight = FontWeight.Bold) }
            }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.wallet_done)) } },
    )
}

@Composable
private fun HexBlock(hex: String) {
    SelectionContainer {
        Text(
            hex,
            style = MaterialTheme.typography.bodySmall,
            fontFamily = FontFamily.Monospace,
            modifier = Modifier
                .padding(top = 12.dp)
                .fillMaxWidth()
                .heightIn(max = 220.dp)
                .clip(RoundedCornerShape(10.dp))
                .background(MaterialTheme.colorScheme.surfaceContainerHighest)
                .verticalScroll(rememberScrollState())
                .padding(10.dp),
        )
    }
}

/**
 * Sign a pasted unsigned transaction with this wallet's keys, or broadcast a
 * pasted signed one. Signing asks for the screen lock like a send does, and
 * sends nothing: the signed transaction is shown to be carried back.
 */
@Composable
fun OfflineTxDialog(viewModel: WalletViewModel, mode: OfflineMode, onDismiss: () -> Unit) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    var input by remember { mutableStateOf("") }
    var busy by remember { mutableStateOf(false) }
    var error by remember { mutableStateOf<String?>(null) }
    var result by remember { mutableStateOf<String?>(null) }
    val bioTitle = stringResource(R.string.wallet_bio_send_title)
    val bioSubtitle = stringResource(R.string.wallet_bio_send_subtitle)

    val signed = result
    if (signed != null && mode == OfflineMode.SIGN) {
        RawTxDialog(
            title = stringResource(R.string.wallet_offline_signed_title),
            body = stringResource(R.string.wallet_offline_signed_body),
            hex = signed,
            onDismiss = onDismiss,
        )
        return
    }

    fun run() {
        busy = true
        error = null
        scope.launch {
            val r = if (mode == OfflineMode.SIGN) viewModel.signRaw(input) else viewModel.broadcastRaw(input)
            busy = false
            r.onSuccess { result = it }.onFailure { error = it.message }
        }
    }

    AlertDialog(
        onDismissRequest = onDismiss,
        title = {
            Text(
                stringResource(
                    if (mode == OfflineMode.SIGN) R.string.wallet_offline_sign_title else R.string.wallet_offline_broadcast_title,
                ),
            )
        },
        text = {
            Column {
                Text(
                    stringResource(
                        if (mode == OfflineMode.SIGN) R.string.wallet_offline_sign_body else R.string.wallet_offline_broadcast_body,
                    ),
                    style = MaterialTheme.typography.bodyMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                val sent = result
                if (sent != null) {
                    SelectionContainer {
                        Text(
                            stringResource(R.string.wallet_offline_sent, sent),
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.Bold,
                            modifier = Modifier.padding(top = 12.dp),
                        )
                    }
                } else {
                    OutlinedTextField(
                        value = input,
                        onValueChange = { input = it.trim(); error = null },
                        placeholder = { Text(stringResource(R.string.wallet_offline_tx_hint)) },
                        textStyle = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
                        minLines = 4,
                        maxLines = 8,
                        isError = error != null,
                        modifier = Modifier.fillMaxWidth().padding(top = 12.dp),
                    )
                    TextButton(onClick = { clipboard.getText()?.text?.trim()?.let { input = it; error = null } }) {
                        Text(stringResource(R.string.wallet_paste), fontWeight = FontWeight.Bold)
                    }
                }
                error?.let {
                    Text(it, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                }
            }
        },
        confirmButton = {
            if (result == null) {
                TextButton(
                    enabled = input.isNotEmpty() && !busy,
                    onClick = {
                        if (mode == OfflineMode.SIGN) {
                            confirmWithBiometrics(context = context, title = bioTitle, subtitle = bioSubtitle) { run() }
                        } else {
                            run()
                        }
                    },
                ) {
                    if (busy) {
                        CircularProgressIndicator(Modifier.padding(end = 8.dp).size(16.dp), strokeWidth = 2.dp)
                    }
                    Text(
                        stringResource(if (mode == OfflineMode.SIGN) R.string.wallet_offline_sign else R.string.wallet_offline_broadcast),
                        fontWeight = FontWeight.Bold,
                    )
                }
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text(stringResource(if (result == null) R.string.cancel else R.string.wallet_done))
            }
        },
    )
}

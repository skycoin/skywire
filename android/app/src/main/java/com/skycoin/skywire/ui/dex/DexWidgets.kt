package com.skycoin.skywire.ui.dex

import android.content.ClipData
import android.content.ClipboardManager
import android.content.Context
import android.text.format.DateUtils
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material.icons.rounded.QrCode2
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Card
import androidx.compose.material3.CardDefaults
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.Surface
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.LocalConfiguration
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.window.DialogProperties
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.isShortScreen
import com.skycoin.skywire.ui.theme.LocalDarkTheme
import com.skycoin.skywire.ui.theme.SkyAccents
import com.skycoin.skywire.ui.wallet.QrImage
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.FormatStyle

/** What a badge says and how loud it is. */
private enum class Tone { GOOD, BUSY, BAD, QUIET, INFO }

@Composable
private fun Badge(text: String, tone: Tone) {
    val (bg, fg) = when (tone) {
        Tone.GOOD -> dexGood().let { it.copy(alpha = 0.16f) to it }
        Tone.BUSY -> dexBusy().let { it.copy(alpha = 0.16f) to it }
        Tone.BAD -> MaterialTheme.colorScheme.error.copy(alpha = 0.16f) to MaterialTheme.colorScheme.error
        Tone.INFO -> MaterialTheme.colorScheme.primary.copy(alpha = 0.16f) to MaterialTheme.colorScheme.primary
        Tone.QUIET -> MaterialTheme.colorScheme.surfaceVariant to MaterialTheme.colorScheme.onSurfaceVariant
    }
    Text(
        text,
        style = MaterialTheme.typography.labelSmall,
        fontWeight = FontWeight.SemiBold,
        color = fg,
        modifier = Modifier.clip(RoundedCornerShape(6.dp)).background(bg).padding(horizontal = 8.dp, vertical = 3.dp),
    )
}

/** Green as text: the accent holds on navy, but falls under 3:1 on white, so light mode takes a darker shade. */
@Composable
fun dexGood(): Color = if (LocalDarkTheme.current) SkyAccents.success else GOOD_ON_LIGHT

/** Amber as text, for the same reason. */
@Composable
fun dexBusy(): Color = if (LocalDarkTheme.current) SkyAccents.warning else BUSY_ON_LIGHT

private val GOOD_ON_LIGHT = Color(0xFF15803D)
private val BUSY_ON_LIGHT = Color(0xFFB45309)

/** An order or history status (Orders.jsx statusMap). Unknown states show as the market wrote them. */
@Composable
fun StatusBadge(status: String) {
    val (label, tone) = when (status) {
        "pending_payment" -> R.string.dex_status_pending_payment to Tone.BUSY
        "paid" -> R.string.dex_status_paid to Tone.INFO
        "confirmed" -> R.string.dex_status_confirmed to Tone.INFO
        "active" -> R.string.dex_status_active to Tone.INFO
        "completed" -> R.string.dex_status_completed to Tone.GOOD
        "cancelled", "canceled" -> R.string.dex_status_cancelled to Tone.BAD
        "expired" -> R.string.dex_status_expired to Tone.QUIET
        "pending" -> R.string.dex_status_pending to Tone.BUSY
        else -> null to Tone.QUIET
    }
    Badge(if (label != null) stringResource(label) else status, tone)
}

@Composable
fun TypeBadge(type: String) {
    if (type == "buy") Badge(stringResource(R.string.dex_type_buy), Tone.GOOD) else Badge(stringResource(R.string.dex_type_sell), Tone.INFO)
}

/** Once a buyer picks a confirmed listing's product, the seller is committed (MyListings.jsx). */
@Composable
fun ProductStateBadge(listing: com.skycoin.skywire.api.DexListing) {
    if (listing.status != "confirmed" || listing.productStatus.isEmpty() || listing.productStatus == "active") return
    when (listing.productStatus) {
        "frozen" -> Badge(stringResource(R.string.dex_product_frozen), Tone.BUSY)
        "sold" -> Badge(stringResource(R.string.dex_product_sold), Tone.GOOD)
        "cancelled", "canceled" -> Badge(stringResource(R.string.dex_status_cancelled), Tone.BAD)
        else -> Badge(listing.productStatus, Tone.QUIET)
    }
}

/** Pending deposit → Confirmed → Listed as product. A confirmed listing is already on the market. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun LifecycleChips(status: String) {
    val reached = if (status == "pending") 0 else 2
    val labels = listOf(R.string.dex_step_pending, R.string.dex_step_confirmed, R.string.dex_step_product)
    FlowRow(horizontalArrangement = Arrangement.spacedBy(4.dp), verticalArrangement = Arrangement.spacedBy(4.dp)) {
        labels.forEachIndexed { i, label ->
            Badge(stringResource(label), if (i < reached) Tone.GOOD else if (i == reached) Tone.INFO else Tone.QUIET)
            if (i < labels.lastIndex) Text("→", style = MaterialTheme.typography.labelSmall, color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
    }
}

@Composable
fun ConfirmationsBar(confirmations: Int, required: Int) {
    val need = if (required > 0) required else DEFAULT_REQUIRED_CONFIRMATIONS
    Column(Modifier.fillMaxWidth()) {
        Row(Modifier.fillMaxWidth(), horizontalArrangement = Arrangement.SpaceBetween) {
            Text(stringResource(R.string.dex_confirmations), style = MaterialTheme.typography.labelSmall)
            Text(stringResource(R.string.dex_fraction, confirmations, need), style = MaterialTheme.typography.labelSmall)
        }
        Spacer(Modifier.height(4.dp))
        LinearProgressIndicator(
            progress = { (confirmations.toFloat() / need).coerceIn(0f, 1f) },
            modifier = Modifier.fillMaxWidth().height(6.dp).clip(RoundedCornerShape(3.dp)),
        )
    }
}

/** A banner across the top of a tab: onboarding, or a market with nothing to sell. */
@Composable
fun DexBanner(text: String, warn: Boolean, action: String? = null, onAction: () -> Unit = {}) {
    val tint = if (warn) SkyAccents.warning else MaterialTheme.colorScheme.primary
    Surface(
        color = tint.copy(alpha = 0.12f),
        shape = RoundedCornerShape(12.dp),
        modifier = Modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(14.dp)) {
            Text(text, style = MaterialTheme.typography.bodyMedium)
            if (action != null) {
                TextButton(onClick = onAction, modifier = Modifier.align(Alignment.End)) { Text(action) }
            }
        }
    }
}

/** A card holding one record or one form; content defaults to ink, not the muted variant. */
@Composable
fun DexCard(modifier: Modifier = Modifier, content: @Composable () -> Unit) {
    Card(
        colors = CardDefaults.cardColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
        modifier = modifier.fillMaxWidth(),
    ) {
        Column(Modifier.padding(16.dp)) { content() }
    }
}

/** One labelled value inside a card. */
@Composable
fun DexField(label: String, value: String, mono: Boolean = false) {
    Row(Modifier.fillMaxWidth().padding(vertical = 2.dp), verticalAlignment = Alignment.Top) {
        Text(
            label,
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
            modifier = Modifier.width(120.dp),
        )
        Text(
            value,
            style = MaterialTheme.typography.bodySmall.let { if (mono) it.copy(fontFamily = FontFamily.Monospace) else it },
            modifier = Modifier.weight(1f),
        )
    }
}

/**
 * Where to send coins: the address in full, with Copy and a QR a wallet can
 * scan. Losing this address, or retyping it wrong, is how funds get lost.
 */
@Composable
fun AddressBlock(address: String, amount: Double, coin: String, qrTitle: String, qrHint: String?, onCopied: (String) -> Unit) {
    val context = LocalContext.current
    var qr by remember { mutableStateOf(false) }
    Surface(color = MaterialTheme.colorScheme.surface, shape = RoundedCornerShape(10.dp), modifier = Modifier.fillMaxWidth()) {
        Text(
            address,
            style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace),
            modifier = Modifier.padding(10.dp),
        )
    }
    Spacer(Modifier.height(8.dp))
    Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
        val copied = stringResource(R.string.copied_to_clipboard)
        OutlinedButton(onClick = { copyText(context, address); onCopied(copied) }) {
            Icon(Icons.Rounded.ContentCopy, null, Modifier.size(18.dp))
            Spacer(Modifier.width(6.dp))
            Text(stringResource(R.string.dex_copy_address))
        }
        OutlinedButton(onClick = { qr = true }) {
            Icon(Icons.Rounded.QrCode2, stringResource(R.string.dex_show_qr), Modifier.size(18.dp))
        }
    }
    if (qr) PaymentQrDialog(address, amount, coin, qrTitle, qrHint) { qr = false }
}

/**
 * A full-size QR for a phone wallet (QrButton.jsx). With a known URI scheme it
 * carries the amount too; the switch falls back to a bare address for wallets
 * that do not understand the link. Dark on white in both themes: inverted
 * codes scan badly.
 */
@Composable
fun PaymentQrDialog(address: String, amount: Double, coin: String, title: String, hint: String?, onDismiss: () -> Unit) {
    val context = LocalContext.current
    val uri = paymentUri(address, amount, coin)
    var withAmount by remember { mutableStateOf(true) }
    val value = if (uri != null && withAmount) uri else address
    // Said in the dialog, not in a snackbar: the dialog is its own window and would cover one.
    var copied by remember { mutableStateOf(false) }
    val screenHeight = LocalConfiguration.current.screenHeightDp
    val compact = isShortScreen()
    AlertDialog(
        onDismissRequest = onDismiss,
        // Side by side needs more than the platform's portrait dialog width.
        properties = DialogProperties(usePlatformDefaultWidth = !compact),
        modifier = if (compact) Modifier.widthIn(max = 600.dp).fillMaxWidth() else Modifier,
        title = { Text(title) },
        text = {
            if (compact) {
                // In landscape a 220 dp code above the text is cut off, and a cut-off
                // code does not scan: it goes beside the text, sized to the height.
                Row(verticalAlignment = Alignment.Top) {
                    QrImage(value, sizeDp = (screenHeight - 200).coerceIn(140, 220))
                    Spacer(Modifier.width(16.dp))
                    Column(Modifier.verticalScroll(rememberScrollState())) {
                        SendExactly(amount, coin)
                        QrDetails(address, coin, uri, withAmount, hint, copied, onCopied = { copied = true }) { withAmount = it }
                    }
                }
            } else {
                Column(Modifier.verticalScroll(rememberScrollState()), horizontalAlignment = Alignment.CenterHorizontally) {
                    SendExactly(amount, coin)
                    QrImage(value, sizeDp = 220)
                    QrDetails(address, coin, uri, withAmount, hint, copied, onCopied = { copied = true }) { withAmount = it }
                }
            }
        },
        confirmButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.dex_close)) } },
        dismissButton = { TextButton(onClick = { copyText(context, address); copied = true }) { Text(stringResource(R.string.dex_copy_address)) } },
    )
}

@Composable
private fun SendExactly(amount: Double, coin: String) {
    if (amount <= 0 || coin.isEmpty()) return
    Text(stringResource(R.string.dex_send_exactly, dexNumber(amount), coin), style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.SemiBold)
    Spacer(Modifier.height(10.dp))
}

/** The link switch, the address in full, and how to use the code. */
@Composable
private fun QrDetails(address: String, coin: String, uri: String?, withAmount: Boolean, hint: String?, copied: Boolean, onCopied: () -> Unit, onWithAmount: (Boolean) -> Unit) {
    val context = LocalContext.current
    if (uri != null) {
        Spacer(Modifier.height(8.dp))
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Text(stringResource(R.string.dex_qr_include_amount, coin), style = MaterialTheme.typography.bodySmall, modifier = Modifier.weight(1f))
            Switch(checked = withAmount, onCheckedChange = onWithAmount)
        }
    }
    Spacer(Modifier.height(8.dp))
    Text(address, style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace))
    if (copied) {
        Text(
            stringResource(R.string.copied_to_clipboard),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.primary,
            modifier = Modifier.padding(top = 4.dp),
        )
    }
    Spacer(Modifier.height(8.dp))
    val base = if (uri != null && withAmount) stringResource(R.string.dex_qr_hint_link, coin) else stringResource(R.string.dex_qr_hint_plain)
    Text(
        if (hint != null) "$base $hint" else base,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
    )
    if (uri != null && withAmount) {
        TextButton(onClick = { copyText(context, uri); onCopied() }) { Text(stringResource(R.string.dex_copy_link)) }
    }
}

@Composable
fun DexConfirm(text: String, confirm: String, onConfirm: () -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        text = { Text(text) },
        confirmButton = {
            TextButton(onClick = { onDismiss(); onConfirm() }) { Text(confirm, color = MaterialTheme.colorScheme.error) }
        },
        dismissButton = { TextButton(onClick = onDismiss) { Text(stringResource(R.string.dex_keep)) } },
    )
}

fun copyText(context: Context, text: String) {
    val cm = context.getSystemService(Context.CLIPBOARD_SERVICE) as ClipboardManager
    cm.setPrimaryClip(ClipData.newPlainText("skydex", text))
}

/** The market sends RFC 3339 times; shown in the phone's zone and the app's language. */
@Composable
fun dexDateTime(iso: String): String {
    val locale = LocalConfiguration.current.locales[0]
    val instant = parseInstant(iso) ?: return iso.ifEmpty { "—" }
    return DateTimeFormatter.ofLocalizedDateTime(FormatStyle.MEDIUM, FormatStyle.SHORT).withLocale(locale)
        .format(instant.atZone(ZoneId.systemDefault()))
}

@Composable
fun dexTime(iso: String): String {
    val locale = LocalConfiguration.current.locales[0]
    val instant = parseInstant(iso) ?: return iso
    return DateTimeFormatter.ofLocalizedTime(FormatStyle.SHORT).withLocale(locale).format(instant.atZone(ZoneId.systemDefault()))
}

/** "5 minutes ago", in the app's language. */
fun dexAgo(ms: Long, now: Long = System.currentTimeMillis()): String =
    if (ms <= 0) "" else DateUtils.getRelativeTimeSpanString(ms, now, DateUtils.MINUTE_IN_MILLIS).toString()

fun parseInstant(iso: String): Instant? = runCatching { Instant.parse(iso) }.getOrNull()
    ?: runCatching { java.time.OffsetDateTime.parse(iso).toInstant() }.getOrNull()

/** A quiet line of supporting text under a section. */
@Composable
fun DexHint(text: String, modifier: Modifier = Modifier) {
    Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.onSurfaceVariant, modifier = modifier)
}

/** The red line a failed market call leaves on the tab. */
@Composable
fun DexError(text: String) {
    Text(text, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
}

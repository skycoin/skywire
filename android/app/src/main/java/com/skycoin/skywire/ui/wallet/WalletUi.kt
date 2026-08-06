package com.skycoin.skywire.ui.wallet

import android.graphics.Bitmap
import androidx.compose.foundation.Image
import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ArrowDownward
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.remember
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.asImageBitmap
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextOverflow
import androidx.compose.ui.unit.dp
import com.google.zxing.BarcodeFormat
import com.google.zxing.EncodeHintType
import com.google.zxing.qrcode.QRCodeWriter
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.Biometrics
import com.skycoin.skywire.ui.components.CONNECTED_GREEN
import com.skycoin.skywire.ui.components.PENDING_AMBER
import com.skycoin.skywire.ui.components.findFragmentActivity
import com.skycoin.skywire.wallet.CachedTx
import com.skycoin.skywire.wallet.CoinSpec
import com.skycoin.wallet.Amounts
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneId
import java.time.format.DateTimeFormatter

/** "2GgFvq…7uQ" — how every address is shown outside copy/share. */
fun shortAddress(address: String, head: Int = 8, tail: Int = 6): String =
    if (address.length <= head + tail + 1) address
    else address.take(head) + "…" + address.takeLast(tail)

fun CoinSpec.amountText(units: ULong): String =
    Amounts.format(units, exponent, displayDecimals)

fun hoursText(hours: ULong): String = Amounts.groupThousands(hours.toString())

/** Fee line for a history row / detail: burned hours or BTC. */
@Composable
fun feeText(coin: CoinSpec, fee: ULong?): String = when {
    fee == null -> "—"
    coin.kind == com.skycoin.skywire.wallet.CoinKind.BTC ->
        "${Amounts.format(fee, 8, 8)} BTC"
    else -> stringResource(R.string.wallet_fee_hours_value, hoursText(fee))
}

/** The status color triple every transaction row and pill uses. */
@Composable
fun txColor(tx: CachedTx): Color = when {
    !tx.confirmed -> PENDING_AMBER
    tx.incoming -> CONNECTED_GREEN
    else -> MaterialTheme.colorScheme.onSurface
}

@Composable
fun dayLabel(timestamp: Long): String {
    val date = Instant.ofEpochSecond(timestamp).atZone(ZoneId.systemDefault()).toLocalDate()
    val today = LocalDate.now()
    return when (date) {
        today -> stringResource(R.string.wallet_day_today)
        today.minusDays(1) -> stringResource(R.string.wallet_day_yesterday)
        else -> date.format(DateTimeFormatter.ofPattern("d MMMM"))
    }
}

fun timeLabel(timestamp: Long): String =
    Instant.ofEpochSecond(timestamp).atZone(ZoneId.systemDefault())
        .format(DateTimeFormatter.ofPattern("HH:mm"))

/** One transaction row — recent activity and full history share it. */
@Composable
fun TxRow(
    coin: CoinSpec,
    tx: CachedTx,
    showStatus: Boolean,
    topDivider: Boolean,
    onClick: () -> Unit,
) {
    Column {
        if (topDivider) {
            androidx.compose.material3.HorizontalDivider(
                modifier = Modifier.padding(horizontal = 16.dp),
                color = MaterialTheme.colorScheme.surfaceContainerHighest,
            )
        }
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onClick)
                .padding(horizontal = 16.dp, vertical = 13.dp),
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(13.dp),
        ) {
            Box(
                modifier = Modifier
                    .size(36.dp)
                    .clip(CircleShape)
                    .background(MaterialTheme.colorScheme.surfaceContainerHighest),
                contentAlignment = Alignment.Center,
            ) {
                Icon(
                    Icons.Outlined.ArrowDownward,
                    contentDescription = null,
                    modifier = Modifier
                        .size(16.dp)
                        .rotate(if (tx.incoming) 0f else 180f),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Column(Modifier.weight(1f)) {
                Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(7.dp)) {
                    Text(
                        stringResource(if (tx.incoming) R.string.wallet_received_verb else R.string.wallet_sent_verb),
                        style = MaterialTheme.typography.bodyLarge,
                        fontWeight = FontWeight.Bold,
                    )
                    if (showStatus) {
                        Box(
                            Modifier
                                .size(6.dp)
                                .clip(CircleShape)
                                .background(txColor(tx)),
                        )
                        Text(
                            stringResource(if (tx.confirmed) R.string.wallet_tx_confirmed else R.string.wallet_filter_pending),
                            style = MaterialTheme.typography.labelSmall,
                            color = txColor(tx),
                        )
                    }
                }
                Text(
                    text = tx.party?.let {
                        stringResource(
                            if (tx.incoming) R.string.wallet_from_line else R.string.wallet_to_line,
                            shortAddress(it, 6, 4),
                            coin.ticker,
                        )
                    } ?: stringResource(R.string.wallet_self_line, coin.ticker),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
            Column(horizontalAlignment = Alignment.End) {
                Text(
                    text = (if (tx.incoming) "+" else "−") + coin.amountText(tx.amount),
                    style = MaterialTheme.typography.bodyLarge,
                    fontWeight = FontWeight.Bold,
                    color = txColor(tx),
                )
                Text(
                    timeLabel(tx.timestamp),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        }
    }
}

/** Receive-address QR. White quiet zone in both themes — scanners want it. */
@Composable
fun QrImage(content: String, sizeDp: Int = 220) {
    val bitmap = remember(content) {
        val hints = mapOf(EncodeHintType.MARGIN to 1)
        val px = 512
        val matrix = QRCodeWriter().encode(content, BarcodeFormat.QR_CODE, px, px, hints)
        val pixels = IntArray(px * px) { i ->
            if (matrix[i % px, i / px]) 0xFF000000.toInt() else 0xFFFFFFFF.toInt()
        }
        Bitmap.createBitmap(pixels, px, px, Bitmap.Config.ARGB_8888)
    }
    Image(
        bitmap = bitmap.asImageBitmap(),
        contentDescription = content,
        modifier = Modifier
            .size(sizeDp.dp)
            .clip(RoundedCornerShape(12.dp)),
    )
}

/**
 * Ask for the phone's own credential, then run — or run directly on a phone
 * with nothing enrolled to ask with. The Settings screen set this pattern;
 * the wallet's sends and reveals follow it.
 */
fun confirmWithBiometrics(
    context: android.content.Context,
    title: String,
    subtitle: String,
    onDenied: (String?) -> Unit = {},
    onConfirmed: () -> Unit,
) {
    val activity = context.findFragmentActivity()
    if (activity == null || !Biometrics.canAuthenticate(context)) {
        onConfirmed()
        return
    }
    Biometrics.prompt(activity, title, subtitle) { success, error ->
        if (success) onConfirmed() else onDenied(error)
    }
}

package com.skycoin.skywire.ui.wallet

import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.net.Uri
import androidx.annotation.DrawableRes
import com.skycoin.skywire.R
import com.skycoin.skywire.wallet.CoinSpec
import java.io.File
import java.util.UUID

/**
 * Badge artwork for the coin list. The four built-ins ship with their real
 * logos (bundled PNGs from the CC0 cryptocurrency-icons set — no network
 * fetch at render time); a user-added coin carries an image its owner
 * picked from the phone at creation, stored under [CoinSpec.icon], and
 * ticker letters when they picked none.
 */
@DrawableRes
fun builtInCoinLogo(coinId: String): Int? = when (coinId) {
    CoinSpec.SKY.id -> R.drawable.coin_sky
    CoinSpec.BTC.id -> R.drawable.coin_btc
    CoinSpec.ETH.id -> R.drawable.coin_eth
    CoinSpec.USDT.id -> R.drawable.coin_usdt
    else -> null
}

/** Where picked coin icons live: app-private, survives across sessions. */
fun coinIconFile(context: Context, name: String): File =
    File(File(context.filesDir, "coin_icons"), name)

/**
 * Copy a picked image into app storage as the coin's badge: center-cropped
 * square, scaled down to badge size, PNG. Copying matters — the photo
 * picker's grant on the original Uri does not outlive this process, so the
 * badge has to be our own file. Returns the stored file's name.
 */
fun importCoinIconFrom(context: Context, uri: Uri): String {
    val source = context.contentResolver.openInputStream(uri)?.use {
        BitmapFactory.decodeStream(it)
    } ?: throw IllegalArgumentException(
        context.getString(R.string.wallet_add_coin_icon_unreadable),
    )
    val side = minOf(source.width, source.height)
    val square = Bitmap.createBitmap(
        source,
        (source.width - side) / 2,
        (source.height - side) / 2,
        side,
        side,
    )
    val scaled = if (side > ICON_SIDE_PX) {
        Bitmap.createScaledBitmap(square, ICON_SIDE_PX, ICON_SIDE_PX, true)
    } else {
        square
    }
    val dir = File(context.filesDir, "coin_icons").apply { mkdirs() }
    val file = File(dir, "coin-${UUID.randomUUID().toString().take(8)}.png")
    file.outputStream().use { out ->
        scaled.compress(Bitmap.CompressFormat.PNG, 100, out)
    }
    return file.name
}

/** 192px covers the largest badge (46dp) on up to ~4x density screens. */
private const val ICON_SIDE_PX = 192

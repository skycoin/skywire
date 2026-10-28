package com.skycoin.skywire.ui.dex

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyListScope
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.Add
import androidx.compose.material.icons.rounded.ArrowDownward
import androidx.compose.material.icons.rounded.Close
import androidx.compose.material.icons.rounded.ContentCopy
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.ExposedDropdownMenuAnchorType
import androidx.compose.material3.ExposedDropdownMenuBox
import androidx.compose.material3.ExposedDropdownMenuDefaults
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
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
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.api.DexCreatedListing
import com.skycoin.skywire.api.DexListing
import com.skycoin.skywire.api.DexOrder
import com.skycoin.skywire.api.DexProduct
import com.skycoin.skywire.api.DexPurchase
import com.skycoin.skywire.dex.DexLocal
import com.skycoin.skywire.dex.SKY

private val TAB_PADDING = PaddingValues(start = 16.dp, end = 16.dp, top = 12.dp, bottom = 32.dp)

private fun LazyListScope.errorLine(state: DexUiState) {
    state.actionError?.let { item(key = "action-error") { DexError(it) } }
    state.tradeError?.takeIf { it != state.actionError }?.let { item(key = "error") { DexError(it) } }
}

private fun LazyListScope.loading(key: String) {
    item(key = key) {
        Box(Modifier.fillMaxWidth().padding(24.dp), contentAlignment = Alignment.Center) {
            CircularProgressIndicator(Modifier.size(24.dp), strokeWidth = 2.dp)
        }
    }
}

@Composable
private fun SectionTitle(text: String) {
    Text(text, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
}

// --- Market ---

@Composable
fun MarketTab(state: DexUiState, local: DexLocal, vm: DexViewModel) {
    var formOpen by rememberSaveable { mutableStateOf(false) }
    val canSell = state.sellCoins.isNotEmpty()
    LazyColumn(contentPadding = TAB_PADDING, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item(key = "head") {
            Row(verticalAlignment = Alignment.CenterVertically) {
                SectionTitle(stringResource(R.string.dex_available_products))
                Spacer(Modifier.weight(1f))
                if (formOpen) {
                    OutlinedButton(onClick = { formOpen = false }) { Text(stringResource(R.string.cancel)) }
                } else {
                    Button(
                        onClick = { if (local.registered) formOpen = true else vm.selectTab(DexTab.SETTINGS) },
                        enabled = canSell,
                    ) {
                        Icon(Icons.Rounded.Add, null, Modifier.size(18.dp))
                        Spacer(Modifier.width(4.dp))
                        Text(stringResource(R.string.dex_new_sell_order))
                    }
                }
            }
        }
        errorLine(state)
        state.deposit?.let { d -> item(key = "deposit") { DepositCard(d, vm) } }
        state.payment?.let { p -> item(key = "payment") { PaymentCard(p, vm) } }
        if (formOpen && canSell) item(key = "form") { NewOrderForm(state, vm) { formOpen = false } }
        val products = state.products
        when {
            products == null -> loading("products-loading")
            products.isEmpty() -> item(key = "products-empty") {
                DexHint(stringResource(if (state.ready == false) R.string.dex_no_coins_configured else R.string.dex_no_products))
            }
            else -> items(products, key = { "p-" + it.id }) { ProductCard(it, state.working) { vm.buy(it) } }
        }
    }
}

@Composable
private fun ProductCard(product: DexProduct, working: Boolean, onBuy: () -> Unit) {
    DexCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("${dexNumber(product.amount)} ${product.sellCoin}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                Text("${dexNumber(product.price)} ${product.paymentCurrency}", style = MaterialTheme.typography.bodyLarge, color = MaterialTheme.colorScheme.primary)
                Spacer(Modifier.height(2.dp))
                DexHint(stringResource(R.string.dex_seller, shortKey(product.sellerPubKey)))
            }
            Button(onClick = onBuy, enabled = !working) { Text(stringResource(R.string.dex_buy)) }
        }
    }
}

@Composable
private fun InstructionCard(title: String, onDismiss: () -> Unit, content: @Composable () -> Unit) {
    DexCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(title, style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold, modifier = Modifier.weight(1f))
            IconButton(onClick = onDismiss) { Icon(Icons.Rounded.Close, stringResource(R.string.dex_close)) }
        }
        content()
    }
}

/** Where to send the deposit, kept on screen until dismissed (Market.jsx). */
@Composable
private fun DepositCard(d: DexCreatedListing, vm: DexViewModel) {
    InstructionCard(stringResource(R.string.dex_fund_title), vm::dismissDeposit) {
        val amount = dexNumber(d.expectedAmount)
        Text(
            if (d.expiresAt.isNotEmpty()) {
                stringResource(R.string.dex_fund_body_until, amount, d.sellCoin, dexNumber(d.amount), dexNumber(d.commission), dexTime(d.expiresAt))
            } else {
                stringResource(R.string.dex_fund_body, amount, d.sellCoin, dexNumber(d.amount), dexNumber(d.commission))
            },
            style = MaterialTheme.typography.bodyMedium,
        )
        Spacer(Modifier.height(10.dp))
        AddressBlock(d.marketWallet, d.expectedAmount, d.sellCoin, stringResource(R.string.dex_fund_title), stringResource(R.string.dex_hint_escrow), vm::notice)
        Spacer(Modifier.height(8.dp))
        DexHint(stringResource(R.string.dex_fund_footer))
    }
}

/** What to pay and to whom, kept on screen until dismissed (Market.jsx). */
@Composable
private fun PaymentCard(p: DexPurchase, vm: DexViewModel) {
    InstructionCard(stringResource(R.string.dex_pay_title), vm::dismissPayment) {
        val amount = dexNumber(p.amount)
        val pay = dexNumber(p.expectedPaymentAmount)
        Text(
            if (p.expiresAt.isNotEmpty()) {
                stringResource(R.string.dex_pay_body_until, amount, p.sellCoin, pay, p.paymentCurrency, dexTime(p.expiresAt))
            } else {
                stringResource(R.string.dex_pay_body, amount, p.sellCoin, pay, p.paymentCurrency)
            },
            style = MaterialTheme.typography.bodyMedium,
        )
        Spacer(Modifier.height(10.dp))
        AddressBlock(p.sellerWallet, p.expectedPaymentAmount, p.paymentCurrency, stringResource(R.string.dex_pay_seller), stringResource(R.string.dex_hint_exact), vm::notice)
        Spacer(Modifier.height(8.dp))
        DexHint(stringResource(R.string.dex_pay_footer, p.sellCoin))
    }
}

/** You sell / You get, as the page laid the trade out. */
@Composable
private fun NewOrderForm(state: DexUiState, vm: DexViewModel, onDone: () -> Unit) {
    var amount by rememberSaveable { mutableStateOf("") }
    var price by rememberSaveable { mutableStateOf("") }
    var sellCoin by rememberSaveable { mutableStateOf(state.sellCoins.firstOrNull().orEmpty()) }
    // You cannot be paid in the coin you sell; any other fibercoin is fine.
    val payOptions = (state.currencies + state.sellCoins.filter { it != sellCoin }).distinct()
    var payCoin by rememberSaveable { mutableStateOf(payOptions.firstOrNull().orEmpty()) }
    LaunchedEffect(state.sellCoins) { if (sellCoin !in state.sellCoins) sellCoin = state.sellCoins.firstOrNull().orEmpty() }
    LaunchedEffect(payOptions) { if (payCoin !in payOptions) payCoin = payOptions.firstOrNull().orEmpty() }

    val amountValue = parseDecimal(amount)
    val priceValue = parseDecimal(price)
    val complete = amountValue != null && amountValue > 0 && priceValue != null && priceValue > 0 && sellCoin.isNotEmpty() && payCoin.isNotEmpty()

    DexCard {
        Text(stringResource(R.string.dex_new_sell_order), style = MaterialTheme.typography.titleSmall, fontWeight = FontWeight.SemiBold)
        Spacer(Modifier.height(12.dp))
        TradeLeg(stringResource(R.string.dex_you_sell), amount, { amount = it }, sellCoin, state.sellCoins) { sellCoin = it }
        Box(Modifier.fillMaxWidth().padding(vertical = 4.dp), contentAlignment = Alignment.Center) {
            Icon(Icons.Rounded.ArrowDownward, null, tint = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        TradeLeg(stringResource(R.string.dex_you_get), price, { price = it }, payCoin, payOptions) { payCoin = it }
        Spacer(Modifier.height(12.dp))
        DexHint(
            if (complete) {
                stringResource(R.string.dex_trade_summary, dexNumber(amountValue), sellCoin, dexNumber(priceValue), payCoin)
            } else {
                stringResource(R.string.dex_trade_hint)
            },
        )
        Spacer(Modifier.height(12.dp))
        Button(
            onClick = {
                vm.createListing(sellCoin, amountValue!!, priceValue!!, payCoin) {
                    // Market.jsx clears the amounts once the listing exists.
                    amount = ""
                    price = ""
                    onDone()
                }
            },
            enabled = complete && !state.working,
            modifier = Modifier.fillMaxWidth(),
        ) { Text(stringResource(R.string.dex_create_sell_order)) }
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun TradeLeg(label: String, value: String, onValue: (String) -> Unit, coin: String, coins: List<String>, onCoin: (String) -> Unit) {
    var open by remember { mutableStateOf(false) }
    Text(label, style = MaterialTheme.typography.labelMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
    Spacer(Modifier.height(4.dp))
    Row(verticalAlignment = Alignment.CenterVertically) {
        OutlinedTextField(
            value = value,
            onValueChange = { v -> onValue(v.filter { it.isDigit() || it == '.' || it == ',' }) },
            placeholder = { Text("0.00") },
            singleLine = true,
            keyboardOptions = KeyboardOptions(keyboardType = KeyboardType.Decimal),
            modifier = Modifier.weight(1f),
        )
        Spacer(Modifier.width(8.dp))
        ExposedDropdownMenuBox(expanded = open, onExpandedChange = { open = it && coins.isNotEmpty() }, modifier = Modifier.width(128.dp)) {
            OutlinedTextField(
                value = coin.ifEmpty { "—" },
                onValueChange = {},
                readOnly = true,
                singleLine = true,
                trailingIcon = { ExposedDropdownMenuDefaults.TrailingIcon(expanded = open) },
                modifier = Modifier.menuAnchor(ExposedDropdownMenuAnchorType.PrimaryNotEditable),
            )
            ExposedDropdownMenu(expanded = open, onDismissRequest = { open = false }) {
                coins.forEach { c ->
                    DropdownMenuItem(text = { Text(c) }, onClick = { onCoin(c); open = false })
                }
            }
        }
    }
}

// --- My Orders ---

@Composable
fun OrdersTab(state: DexUiState, vm: DexViewModel) {
    var confirm by remember { mutableStateOf<DexOrder?>(null) }
    LazyColumn(contentPadding = TAB_PADDING, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        errorLine(state)
        val orders = state.orders
        when {
            orders == null -> loading("orders-loading")
            orders.isEmpty() -> item(key = "orders-empty") { DexHint(stringResource(R.string.dex_no_orders)) }
            // A self-trade comes back twice under one id, once as the buy and once as the sell.
            else -> items(orders, key = { "o-" + it.type + "-" + it.id }) { order ->
                OrderCard(order, state, vm) { confirm = order }
            }
        }
    }
    confirm?.let { order ->
        DexConfirm(stringResource(R.string.dex_cancel_buy_confirm), stringResource(R.string.dex_cancel_buy), { vm.cancelOrder(order) }) { confirm = null }
    }
}

@Composable
private fun OrderCard(order: DexOrder, state: DexUiState, vm: DexViewModel, onCancel: () -> Unit) {
    val live = state.statuses[order.id]
    val status = live?.status?.ifEmpty { null } ?: order.status
    DexCard {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            TypeBadge(order.type)
            StatusBadge(status)
            Spacer(Modifier.weight(1f))
            Text(shortId(order.id), style = MaterialTheme.typography.labelSmall.copy(fontFamily = FontFamily.Monospace), color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Spacer(Modifier.height(8.dp))
        Text("${dexNumber(order.amount)} ${order.sellCoin}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
        Text("${dexNumber(order.price)} ${order.paymentCurrency}", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary)

        if (order.type == "buy" && status == "pending_payment" && order.sellerWallet.isNotEmpty()) {
            Spacer(Modifier.height(10.dp))
            Text(
                stringResource(R.string.dex_pay_exactly, dexNumber(order.expectedPaymentAmount), order.paymentCurrency),
                style = MaterialTheme.typography.bodyMedium,
                fontWeight = FontWeight.SemiBold,
            )
            Spacer(Modifier.height(6.dp))
            AddressBlock(order.sellerWallet, order.expectedPaymentAmount, order.paymentCurrency, stringResource(R.string.dex_pay_seller), stringResource(R.string.dex_hint_exact), vm::notice)
        }

        if (order.type == "buy") {
            Spacer(Modifier.height(10.dp))
            when {
                status == "completed" -> Text(stringResource(R.string.dex_delivered), style = MaterialTheme.typography.bodySmall, color = com.skycoin.skywire.ui.theme.SkyAccents.success)
                status == "pending_payment" -> DexHint(stringResource(R.string.dex_awaiting_payment))
                live != null && status in IN_FLIGHT_STATUSES -> ConfirmationsBar(live.confirmations, live.requiredConfirmations)
            }
        }
        Spacer(Modifier.height(8.dp))
        live?.paymentTxHash?.takeIf { it.isNotEmpty() }?.let { DexField(stringResource(R.string.dex_payment_tx), shortId(it), mono = true) }
        if (order.createdAt.isNotEmpty()) DexField(stringResource(R.string.dex_created), dexDateTime(order.createdAt))

        if (order.type == "buy" && status == "pending_payment") {
            Spacer(Modifier.height(8.dp))
            val busy = state.busyId == order.id
            OutlinedButton(onClick = onCancel, enabled = !busy) {
                Text(stringResource(if (busy) R.string.dex_canceling else R.string.dex_cancel_buy), color = MaterialTheme.colorScheme.error)
            }
        }
    }
}

// --- My Listings ---

@Composable
fun ListingsTab(state: DexUiState, vm: DexViewModel) {
    var confirm by remember { mutableStateOf<DexListing?>(null) }
    LazyColumn(contentPadding = TAB_PADDING, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        errorLine(state)
        val listings = state.listings
        when {
            listings == null -> loading("listings-loading")
            listings.isEmpty() -> item(key = "listings-empty") { DexHint(stringResource(R.string.dex_no_listings)) }
            else -> {
                items(listings, key = { "l-" + it.id }) { l -> ListingCard(l, state, vm) { confirm = l } }
                if (listings.any { it.status == "pending" }) {
                    item(key = "listings-note") { DexBanner(stringResource(R.string.dex_listings_pending_note), warn = false) }
                }
            }
        }
    }
    confirm?.let { l ->
        val text = if (l.status == "pending") {
            stringResource(R.string.dex_cancel_listing_confirm)
        } else {
            stringResource(R.string.dex_cancel_offer_confirm, l.sellCoin.ifEmpty { SKY })
        }
        DexConfirm(text, stringResource(R.string.dex_cancel_offer), { vm.cancelListing(l) }) { confirm = null }
    }
}

@Composable
private fun ListingCard(l: DexListing, state: DexUiState, vm: DexViewModel, onCancel: () -> Unit) {
    DexCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Column(Modifier.weight(1f)) {
                Text("${dexNumber(l.amount)} ${l.sellCoin}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
                Text("${dexNumber(l.price)} ${l.paymentCurrency}", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary)
            }
            Text(shortId(l.id), style = MaterialTheme.typography.labelSmall.copy(fontFamily = FontFamily.Monospace), color = MaterialTheme.colorScheme.onSurfaceVariant)
        }
        Spacer(Modifier.height(8.dp))
        LifecycleChips(l.status)
        Spacer(Modifier.height(4.dp))
        ProductStateBadge(l)
        Spacer(Modifier.height(8.dp))
        if (l.status == "pending") {
            val fee = l.expectedAmount - l.amount
            val deposit = "${dexNumber(l.expectedAmount)} ${l.sellCoin}" +
                if (fee > 0) " (" + stringResource(R.string.dex_amount_plus_fee, dexNumber(l.amount), String.format(java.util.Locale.US, "%.3f", fee)) + ")" else ""
            DexField(stringResource(R.string.dex_deposit), deposit)
            if (l.expiresAt.isNotEmpty()) DexField(stringResource(R.string.dex_expires), dexTime(l.expiresAt))
            if (l.marketWallet.isNotEmpty()) {
                Spacer(Modifier.height(6.dp))
                AddressBlock(l.marketWallet, l.expectedAmount, l.sellCoin, stringResource(R.string.dex_fund_title), stringResource(R.string.dex_hint_escrow), vm::notice)
            }
        } else {
            DexField(stringResource(R.string.dex_deposit), stringResource(R.string.dex_received))
        }
        if (l.txHash.isNotEmpty()) DexField(stringResource(R.string.dex_deposit_tx), shortId(l.txHash), mono = true)
        Spacer(Modifier.height(8.dp))
        when {
            l.cancelable -> {
                val busy = state.busyId == l.id
                OutlinedButton(onClick = onCancel, enabled = !busy) {
                    Text(stringResource(if (busy) R.string.dex_canceling else R.string.dex_cancel_offer), color = MaterialTheme.colorScheme.error)
                }
            }
            l.status == "confirmed" && l.productStatus == "frozen" -> DexHint(stringResource(R.string.dex_buyer_paying))
            l.status == "confirmed" && l.productStatus == "sold" -> DexHint(stringResource(R.string.dex_product_sold))
        }
    }
}

// --- History ---

@Composable
fun HistoryTab(local: DexLocal, vm: DexViewModel) {
    val rows = remember(local.history) { local.history.sortedByDescending { parseInstant(it.createdAt)?.toEpochMilli() ?: it.savedAt } }
    LazyColumn(contentPadding = TAB_PADDING, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        if (rows.isEmpty()) {
            item(key = "history-empty") { DexHint(stringResource(R.string.dex_no_history)) }
        } else {
            items(rows, key = { "h-" + it.type + "-" + it.id }) { o -> HistoryCard(o) { vm.removeHistory(o) } }
            item(key = "history-hint") { DexHint(stringResource(R.string.dex_history_hint)) }
        }
    }
}

@Composable
private fun HistoryCard(o: DexOrder, onRemove: () -> Unit) {
    DexCard {
        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(6.dp)) {
            TypeBadge(o.type)
            StatusBadge(o.status)
            Spacer(Modifier.weight(1f))
            IconButton(onClick = onRemove, modifier = Modifier.size(32.dp)) {
                Icon(Icons.Rounded.Close, stringResource(R.string.dex_remove_from_history), Modifier.size(18.dp))
            }
        }
        Text("${dexNumber(o.amount)} ${o.sellCoin}", style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.Bold)
        Text("${dexNumber(o.price)} ${o.paymentCurrency}", style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.primary)
        Spacer(Modifier.height(6.dp))
        val market = o.marketName.ifEmpty { if (o.marketPk.isNotEmpty()) shortKey(o.marketPk) else "—" }
        DexField(stringResource(R.string.dex_market), market)
        DexField(stringResource(R.string.dex_date), if (o.createdAt.isNotEmpty()) dexDateTime(o.createdAt) else "—")
    }
}

// --- Settings ---

@Composable
fun SettingsTab(state: DexUiState, local: DexLocal, vm: DexViewModel) {
    val context = LocalContext.current
    var sky by rememberSaveable { mutableStateOf(local.wallets[SKY].orEmpty()) }
    val payouts = remember { mutableStateMapOf<String, String>() }
    LaunchedEffect(state.currencies) {
        state.currencies.forEach { c -> if (c !in payouts) payouts[c] = local.wallets[c].orEmpty() }
    }
    var confirmClear by remember { mutableStateOf(false) }
    LazyColumn(contentPadding = TAB_PADDING, verticalArrangement = Arrangement.spacedBy(12.dp)) {
        item(key = "wallets") {
            DexCard {
                SectionTitle(stringResource(R.string.dex_wallets_title))
                Spacer(Modifier.height(6.dp))
                DexHint(stringResource(R.string.dex_wallets_body))
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = sky,
                    onValueChange = { sky = it },
                    label = { Text(stringResource(R.string.dex_sky_address)) },
                    placeholder = { Text(stringResource(R.string.dex_sky_address_hint)) },
                    singleLine = true,
                    textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                    modifier = Modifier.fillMaxWidth(),
                )
                state.currencies.forEach { cur ->
                    Spacer(Modifier.height(8.dp))
                    OutlinedTextField(
                        value = payouts[cur].orEmpty(),
                        onValueChange = { payouts[cur] = it },
                        label = { Text(stringResource(R.string.dex_payout_address, cur)) },
                        placeholder = { Text(stringResource(R.string.dex_payout_hint, cur)) },
                        singleLine = true,
                        textStyle = MaterialTheme.typography.bodyMedium.copy(fontFamily = FontFamily.Monospace),
                        modifier = Modifier.fillMaxWidth(),
                    )
                }
                if (state.currencies.isEmpty() && state.ready != null) {
                    Spacer(Modifier.height(8.dp))
                    DexHint(stringResource(R.string.dex_no_payment_coins))
                }
                state.walletsMessage?.let { m ->
                    Spacer(Modifier.height(10.dp))
                    Text(
                        m.text,
                        style = MaterialTheme.typography.bodySmall,
                        color = if (m.ok) com.skycoin.skywire.ui.theme.SkyAccents.success else MaterialTheme.colorScheme.error,
                    )
                }
                Spacer(Modifier.height(12.dp))
                Button(onClick = { vm.saveWallets(sky, payouts.toMap()) }, enabled = !state.savingWallets, modifier = Modifier.fillMaxWidth()) {
                    Text(stringResource(if (state.savingWallets) R.string.dex_saving else R.string.dex_save_addresses))
                }
            }
        }
        item(key = "connection") {
            DexCard {
                SectionTitle(stringResource(R.string.dex_connection_title))
                Spacer(Modifier.height(8.dp))
                val pk = state.market?.marketPk.orEmpty()
                Text(pk, style = MaterialTheme.typography.bodySmall.copy(fontFamily = FontFamily.Monospace))
                Spacer(Modifier.height(8.dp))
                val copied = stringResource(R.string.copied_to_clipboard)
                OutlinedButton(onClick = { copyText(context, pk); vm.notice(copied) }, enabled = pk.isNotEmpty()) {
                    Icon(Icons.Rounded.ContentCopy, null, Modifier.size(18.dp))
                    Spacer(Modifier.width(6.dp))
                    Text(stringResource(R.string.dex_copy_key))
                }
                Spacer(Modifier.height(6.dp))
                DexHint(stringResource(R.string.dex_switch_hint))
            }
        }
        // "Clear history" sits here, away from the list it wipes, as the phone layout of the page had it.
        if (local.history.isNotEmpty()) {
            item(key = "history") {
                DexCard {
                    SectionTitle(stringResource(R.string.dex_history_title))
                    Spacer(Modifier.height(6.dp))
                    DexHint(stringResource(R.string.dex_history_body))
                    Spacer(Modifier.height(10.dp))
                    FilledTonalButton(onClick = { confirmClear = true }) { Text(stringResource(R.string.dex_clear_history)) }
                }
            }
        }
    }
    if (confirmClear) {
        DexConfirm(stringResource(R.string.dex_clear_history_confirm), stringResource(R.string.dex_clear_history), vm::clearHistory) { confirmClear = false }
    }
}

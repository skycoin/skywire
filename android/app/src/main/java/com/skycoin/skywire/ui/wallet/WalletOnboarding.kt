package com.skycoin.skywire.ui.wallet

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.outlined.ErrorOutline
import androidx.compose.material.icons.outlined.ScreenshotMonitor
import androidx.compose.material3.Button
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.Icon
import androidx.compose.material3.LinearProgressIndicator
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
import androidx.compose.material3.SegmentedButton
import androidx.compose.material3.SegmentedButtonDefaults
import androidx.compose.material3.SingleChoiceSegmentedButtonRow
import androidx.compose.material3.SnackbarHost
import androidx.compose.material3.SnackbarHostState
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateMapOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.runtime.toMutableStateList
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.input.PasswordVisualTransformation
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.SecureWindow
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.wallet.CoinKind
import com.skycoin.wallet.Bip39
import com.skycoin.wallet.Derivation

/** Seed backup: the numbered grid, screenshots off, no copy anywhere. */
@Composable
fun WalletSeedScreen(
    viewModel: WalletViewModel,
    onContinue: () -> Unit,
    onBack: () -> Unit,
) {
    SecureWindow()
    val state by viewModel.uiState.collectAsState()
    val words = state.draft.seed?.split(" ") ?: emptyList()
    val screenLock = rememberScreenLock()

    Scaffold(topBar = { SkyTopBar(stringResource(R.string.wallet_seed_title), onBack = onBack) }) { padding ->
        if (!screenLock) {
            ScreenLockNeeded(Modifier.padding(padding))
            return@Scaffold
        }
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            Text(
                stringResource(R.string.wallet_seed_body),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Row(
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 16.dp)
                    .clip(RoundedCornerShape(12.dp))
                    .background(MaterialTheme.colorScheme.surfaceContainerHighest)
                    .padding(horizontal = 14.dp, vertical = 12.dp),
                verticalAlignment = Alignment.CenterVertically,
                horizontalArrangement = Arrangement.spacedBy(8.dp),
            ) {
                Icon(
                    Icons.Outlined.ScreenshotMonitor, null,
                    Modifier.size(16.dp),
                    tint = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Text(
                    stringResource(R.string.wallet_seed_secure),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            // A new length is a new phrase: the words shown are always the ones the wallet gets.
            SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(top = 16.dp)) {
                listOf(12, 24).forEachIndexed { i, n ->
                    SegmentedButton(
                        selected = words.size == n,
                        onClick = { if (words.size != n) viewModel.startCreate(n, state.draft.derivation, state.draft.passphrase) },
                        shape = SegmentedButtonDefaults.itemShape(i, 2),
                    ) {
                        Text(stringResource(if (n == 24) R.string.wallet_seed_words_24 else R.string.wallet_seed_words_12))
                    }
                }
            }
            DerivationChoice(viewModel)
            Spacer(Modifier.height(20.dp))
            SeedGrid(words)
            val passphraseOk = PassphraseFields(viewModel, confirm = true)
            Button(
                onClick = onContinue,
                enabled = passphraseOk,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 28.dp, bottom = 24.dp)
                    .height(52.dp),
            ) {
                Text(stringResource(R.string.wallet_seed_continue), fontWeight = FontWeight.Bold)
            }
        }
    }
}

/**
 * Two kinds of wallet, as skycoin-web offers them: for fiber coins the classic
 * deterministic one or BIP 44, for Bitcoin native segwit or legacy P2PKH. The
 * phrase is the same; the addresses are not, so a restore has to name the kind
 * the wallet was made as.
 */
@Composable
fun DerivationChoice(viewModel: WalletViewModel) {
    val state by viewModel.uiState.collectAsState()
    val kind = state.draft.coin.kind
    if (kind != CoinKind.SKY_FIBER && kind != CoinKind.BTC) return
    val btc = kind == CoinKind.BTC
    val selected = state.draft.derivation
    Text(
        stringResource(R.string.wallet_derivation_title).uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 18.dp),
    )
    SingleChoiceSegmentedButtonRow(Modifier.fillMaxWidth().padding(top = 8.dp)) {
        listOf(Derivation.STANDARD, Derivation.BIP44).forEachIndexed { i, d ->
            SegmentedButton(
                selected = selected == d,
                onClick = { viewModel.setDraftDerivation(d) },
                shape = SegmentedButtonDefaults.itemShape(i, 2),
            ) {
                Text(
                    stringResource(
                        when {
                            btc && d == Derivation.BIP44 -> R.string.wallet_derivation_btc_legacy
                            btc -> R.string.wallet_derivation_btc_segwit
                            d == Derivation.BIP44 -> R.string.wallet_derivation_bip44
                            else -> R.string.wallet_derivation_standard
                        },
                    ),
                )
            }
        }
    }
    Text(
        stringResource(if (btc) R.string.wallet_derivation_note_btc else R.string.wallet_derivation_note),
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 8.dp),
    )
}

/**
 * The optional BIP 39 passphrase, as skycoin-web offers it. Every wallet but
 * the classic fiber one runs the phrase through BIP 39, and there it changes
 * every key. Creating asks for it twice, since a typo would lose the wallet.
 * Returns whether what is typed can be used.
 */
@Composable
fun PassphraseFields(viewModel: WalletViewModel, confirm: Boolean): Boolean {
    val state by viewModel.uiState.collectAsState()
    val draft = state.draft
    if (draft.coin.kind == CoinKind.SKY_FIBER && draft.derivation == Derivation.STANDARD) return true
    var again by remember { mutableStateOf("") }
    val mismatch = confirm && draft.passphrase != again
    Text(
        stringResource(R.string.wallet_passphrase_title).uppercase(),
        style = MaterialTheme.typography.labelSmall,
        color = MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 22.dp),
    )
    OutlinedTextField(
        value = draft.passphrase,
        onValueChange = viewModel::setDraftPassphrase,
        placeholder = { Text(stringResource(R.string.wallet_passphrase_hint)) },
        singleLine = true,
        visualTransformation = PasswordVisualTransformation(),
        modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
        shape = RoundedCornerShape(12.dp),
    )
    if (confirm && draft.passphrase.isNotEmpty()) {
        OutlinedTextField(
            value = again,
            onValueChange = { again = it },
            placeholder = { Text(stringResource(R.string.wallet_passphrase_confirm)) },
            singleLine = true,
            isError = mismatch && again.isNotEmpty(),
            visualTransformation = PasswordVisualTransformation(),
            modifier = Modifier.fillMaxWidth().padding(top = 8.dp),
            shape = RoundedCornerShape(12.dp),
        )
    }
    Text(
        stringResource(if (mismatch && again.isNotEmpty()) R.string.wallet_passphrase_mismatch else R.string.wallet_passphrase_note),
        style = MaterialTheme.typography.bodySmall,
        color = if (mismatch && again.isNotEmpty()) MaterialTheme.colorScheme.error else MaterialTheme.colorScheme.onSurfaceVariant,
        modifier = Modifier.padding(top = 8.dp),
    )
    return !mismatch
}

/** The two-column numbered word grid — backup and reveal share it. */
@Composable
fun SeedGrid(words: List<String>) {
    Column(verticalArrangement = Arrangement.spacedBy(10.dp)) {
        words.chunked(2).forEachIndexed { rowIndex, pair ->
            Row(horizontalArrangement = Arrangement.spacedBy(10.dp)) {
                pair.forEachIndexed { colIndex, word ->
                    Row(
                        modifier = Modifier
                            .weight(1f)
                            .clip(RoundedCornerShape(12.dp))
                            .background(MaterialTheme.colorScheme.surfaceVariant)
                            .padding(horizontal = 14.dp, vertical = 13.dp),
                        verticalAlignment = Alignment.CenterVertically,
                        horizontalArrangement = Arrangement.spacedBy(10.dp),
                    ) {
                        Text(
                            "${rowIndex * 2 + colIndex + 1}",
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onSurfaceVariant,
                            modifier = Modifier.width(16.dp),
                        )
                        Text(word, style = MaterialTheme.typography.bodyLarge, fontWeight = FontWeight.Bold)
                    }
                }
                if (pair.size == 1) Spacer(Modifier.weight(1f))
            }
        }
    }
}

/** The three-word quiz. Wrong fields name their position; matching activates. */
@Composable
fun WalletVerifyScreen(
    viewModel: WalletViewModel,
    onActivated: () -> Unit,
    onShowSeed: () -> Unit,
    onBack: () -> Unit,
) {
    SecureWindow()
    val state by viewModel.uiState.collectAsState()
    val positions = state.draft.quizPositions
    val answers = remember { mutableStateMapOf<Int, String>() }
    val errors = remember { mutableStateMapOf<Int, String>() }
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_verify_title), onBack = onBack) },
    ) { padding ->
        val context = androidx.compose.ui.platform.LocalContext.current
        Column(
            modifier = Modifier
                .padding(padding)
                .verticalScroll(rememberScrollState())
                .padding(horizontal = 20.dp),
        ) {
            Text(
                stringResource(R.string.wallet_verify_body),
                style = MaterialTheme.typography.bodyLarge,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(26.dp))
            positions.forEach { pos ->
                Column(Modifier.padding(bottom = 14.dp)) {
                    Text(
                        stringResource(R.string.wallet_verify_word, pos),
                        style = MaterialTheme.typography.labelLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                        modifier = Modifier.padding(bottom = 7.dp),
                    )
                    OutlinedTextField(
                        value = answers[pos] ?: "",
                        onValueChange = {
                            answers[pos] = it
                            errors.remove(pos)
                        },
                        placeholder = { Text(stringResource(R.string.wallet_verify_hint)) },
                        singleLine = true,
                        isError = errors.containsKey(pos),
                        modifier = Modifier.fillMaxWidth(),
                        shape = RoundedCornerShape(12.dp),
                    )
                    errors[pos]?.let { err ->
                        Row(
                            modifier = Modifier.padding(top = 7.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(6.dp),
                        ) {
                            Icon(
                                Icons.Outlined.ErrorOutline, null,
                                Modifier.size(14.dp),
                                tint = MaterialTheme.colorScheme.error,
                            )
                            Text(err, style = MaterialTheme.typography.bodySmall, color = MaterialTheme.colorScheme.error)
                        }
                    }
                }
            }
            Button(
                onClick = {
                    errors.clear()
                    val wrong = viewModel.submitQuiz(answers, onActivated = onActivated)
                    wrong.keys.forEach { pos ->
                        val typed = answers[pos]?.trim().orEmpty()
                        errors[pos] = if (typed.isEmpty()) {
                            context.getString(R.string.wallet_verify_missing, pos)
                        } else {
                            context.getString(R.string.wallet_verify_wrong, pos)
                        }
                    }
                },
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 16.dp)
                    .height(52.dp),
            ) {
                Text(stringResource(R.string.wallet_verify_activate), fontWeight = FontWeight.Bold)
            }
            TextButton(
                onClick = onShowSeed,
                modifier = Modifier
                    .fillMaxWidth()
                    .padding(top = 6.dp, bottom = 24.dp),
            ) {
                Text(stringResource(R.string.wallet_verify_again), fontWeight = FontWeight.Bold)
            }
        }
    }
}

/** Restore: word-by-word with wordlist suggestions, or one paste of the phrase. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun WalletRestoreScreen(
    viewModel: WalletViewModel,
    onRestored: () -> Unit,
    onBack: () -> Unit,
) {
    SecureWindow()
    val state by viewModel.uiState.collectAsState()
    val words = remember { mutableListOf<String>().toMutableStateList() }
    var input by remember { mutableStateOf("") }
    var error by remember { mutableStateOf<String?>(null) }
    var legacyOffer by remember { mutableStateOf(false) }
    val clipboard = LocalClipboardManager.current
    val context = androidx.compose.ui.platform.LocalContext.current
    val snackbar = remember { SnackbarHostState() }
    val screenLock = rememberScreenLock()

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }

    val target = if (words.size > 12) 24 else 12
    // Until a 13th word settles it, the phrase may be either length.
    val targetLabel = if (target == 24) "24" else "12/24"

    fun addWord(word: String) {
        val w = word.trim().lowercase()
        if (w.isEmpty()) return
        if (words.size < 24) {
            words.add(w)
            input = ""
            error = null
            legacyOffer = false
        }
    }

    fun finish(force: Boolean = false) {
        if (words.size != 12 && words.size != 24) {
            error = context.getString(R.string.wallet_restore_short, words.size)
            return
        }
        val phrase = words.joinToString(" ")
        if (!Bip39.validate(phrase)) {
            // A 24-word phrase of list words without a checksum is how the
            // older wallets wrote seeds — their seeds were arbitrary strings,
            // and refusing them strands those wallets. Offer the restore with
            // the typo risk said plainly; 12-word phrases were never written
            // without a checksum, so for them the checksum is the check.
            // Only the fiber chains derive from the phrase itself.
            val legacy = words.size == 24 && Bip39.legacyWords(phrase) &&
                state.draft.coin.kind == CoinKind.SKY_FIBER && state.draft.derivation == Derivation.STANDARD
            if (legacy && force) {
                legacyOffer = false
                viewModel.restoreWallet(phrase, legacy = true, onDone = onRestored)
                return
            }
            if (legacy) {
                error = context.getString(R.string.wallet_restore_legacy)
                legacyOffer = true
                return
            }
            error = context.getString(R.string.wallet_restore_checksum)
            legacyOffer = false
            return
        }
        legacyOffer = false
        viewModel.restoreWallet(phrase, onDone = onRestored)
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_restore_title), onBack = onBack) },
    ) { padding ->
        if (!screenLock) {
            ScreenLockNeeded(Modifier.padding(padding))
            return@Scaffold
        }
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, bottom = 24.dp),
        ) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        stringResource(R.string.wallet_restore_progress, (words.size + 1).coerceAtMost(target), targetLabel),
                        style = MaterialTheme.typography.bodyLarge,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                    Spacer(Modifier.weight(1f))
                    TextButton(onClick = {
                        val text = clipboard.getText()?.text.orEmpty()
                        val pasted = text.trim().lowercase().split(Regex("\\s+")).filter { it.isNotEmpty() }
                        if (pasted.size in listOf(12, 24)) {
                            words.clear()
                            words.addAll(pasted)
                            error = null
                            legacyOffer = false
                        } else if (pasted.isNotEmpty()) {
                            error = context.getString(R.string.wallet_restore_short, pasted.size)
                        }
                    }) {
                        Text(stringResource(R.string.wallet_restore_paste), fontWeight = FontWeight.Bold)
                    }
                }
                LinearProgressIndicator(
                    progress = { words.size / target.toFloat() },
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 6.dp)
                        .height(3.dp),
                )
                DerivationChoice(viewModel)
                PassphraseFields(viewModel, confirm = false)
            }

            error?.let { err ->
                item {
                    Row(
                        modifier = Modifier
                            .fillMaxWidth()
                            .padding(top = 16.dp)
                            .clip(RoundedCornerShape(12.dp))
                            .background(MaterialTheme.colorScheme.errorContainer)
                            .padding(horizontal = 14.dp, vertical = 13.dp),
                        horizontalArrangement = Arrangement.spacedBy(9.dp),
                    ) {
                        Icon(
                            Icons.Outlined.ErrorOutline, null,
                            Modifier.size(16.dp),
                            tint = MaterialTheme.colorScheme.onErrorContainer,
                        )
                        Text(
                            err,
                            style = MaterialTheme.typography.bodySmall,
                            color = MaterialTheme.colorScheme.onErrorContainer,
                        )
                    }
                }
            }

            item {
                // Entered words as numbered chips; tapping one takes it out.
                FlowRow(
                    modifier = Modifier.padding(top = 18.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                    verticalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    words.forEachIndexed { i, w ->
                        Row(
                            modifier = Modifier
                                .clip(RoundedCornerShape(20.dp))
                                .background(MaterialTheme.colorScheme.surfaceVariant)
                                .clickable {
                                    words.removeAt(i)
                                    legacyOffer = false
                                }
                                .padding(horizontal = 12.dp, vertical = 8.dp),
                            verticalAlignment = Alignment.CenterVertically,
                            horizontalArrangement = Arrangement.spacedBy(7.dp),
                        ) {
                            Text(
                                "${i + 1}",
                                style = MaterialTheme.typography.labelSmall,
                                color = MaterialTheme.colorScheme.onSurfaceVariant,
                            )
                            Text(w, style = MaterialTheme.typography.bodyMedium, fontWeight = FontWeight.Bold)
                        }
                    }
                }
            }

            item {
                OutlinedTextField(
                    value = input,
                    onValueChange = { new ->
                        if (new.endsWith(" ") && new.isNotBlank()) addWord(new)
                        else { input = new; error = null; legacyOffer = false }
                    },
                    placeholder = { Text(stringResource(R.string.wallet_restore_hint)) },
                    singleLine = true,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 16.dp),
                    shape = RoundedCornerShape(12.dp),
                )
                val suggestions = if (input.isNotBlank()) Bip39.suggestions(input, 4) else emptyList()
                Row(
                    modifier = Modifier.padding(top = 12.dp),
                    horizontalArrangement = Arrangement.spacedBy(8.dp),
                ) {
                    suggestions.forEach { s ->
                        Text(
                            s,
                            style = MaterialTheme.typography.bodyMedium,
                            fontWeight = FontWeight.Bold,
                            color = MaterialTheme.colorScheme.primary,
                            modifier = Modifier
                                .clip(RoundedCornerShape(18.dp))
                                .background(MaterialTheme.colorScheme.secondaryContainer)
                                .clickable { addWord(s) }
                                .padding(horizontal = 14.dp, vertical = 9.dp),
                        )
                    }
                }
                Text(
                    stringResource(R.string.wallet_restore_wordlist_note),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    modifier = Modifier.padding(top = 14.dp),
                )
                Button(
                    onClick = { finish(force = legacyOffer) },
                    enabled = !state.restoring,
                    modifier = Modifier
                        .fillMaxWidth()
                        .padding(top = 26.dp)
                        .height(52.dp),
                ) {
                    if (state.restoring) {
                        CircularProgressIndicator(Modifier.size(16.dp), strokeWidth = 2.dp)
                        Spacer(Modifier.width(10.dp))
                        Text(stringResource(R.string.wallet_restore_scanning), fontWeight = FontWeight.Bold)
                    } else {
                        Text(
                            stringResource(
                                // The second press of the same button is the
                                // confirmation: the offer above it says what is
                                // being agreed to.
                                if (legacyOffer) R.string.wallet_restore_anyway
                                else R.string.wallet_restore_action,
                            ),
                            fontWeight = FontWeight.Bold,
                        )
                    }
                }
            }
        }
    }
}

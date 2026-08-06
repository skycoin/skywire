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
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.wallet.Bip39

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

    Scaffold(topBar = { SkyTopBar(stringResource(R.string.wallet_seed_title), onBack = onBack) }) { padding ->
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
            Spacer(Modifier.height(20.dp))
            SeedGrid(words)
            Button(
                onClick = onContinue,
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
    val clipboard = LocalClipboardManager.current
    val context = androidx.compose.ui.platform.LocalContext.current
    val snackbar = remember { SnackbarHostState() }

    LaunchedEffect(state.message) {
        state.message?.let { snackbar.showSnackbar(it); viewModel.messageShown() }
    }

    val target = if (words.size > 12) 24 else 12

    fun addWord(word: String) {
        val w = word.trim().lowercase()
        if (w.isEmpty()) return
        if (words.size < 24) {
            words.add(w)
            input = ""
            error = null
        }
    }

    fun finish() {
        if (words.size != 12 && words.size != 24) {
            error = context.getString(R.string.wallet_restore_short, words.size)
            return
        }
        val phrase = words.joinToString(" ")
        if (!Bip39.validate(phrase)) {
            error = context.getString(R.string.wallet_restore_checksum)
            return
        }
        viewModel.restoreWallet(phrase, onDone = onRestored)
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = { SkyTopBar(stringResource(R.string.wallet_restore_title), onBack = onBack) },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(start = 20.dp, end = 20.dp, bottom = 24.dp),
        ) {
            item {
                Row(verticalAlignment = Alignment.CenterVertically) {
                    Text(
                        stringResource(R.string.wallet_restore_progress, (words.size + 1).coerceAtMost(target), target),
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
                                .clickable { words.removeAt(i) }
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
                        else { input = new; error = null }
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
                    onClick = { finish() },
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
                        Text(stringResource(R.string.wallet_restore_action), fontWeight = FontWeight.Bold)
                    }
                }
            }
        }
    }
}

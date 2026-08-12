package com.skycoin.skywire.ui.settings

import android.content.Intent
import android.provider.Settings
import android.widget.Toast
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
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
import androidx.compose.foundation.text.KeyboardOptions
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.KeyboardArrowRight
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CircularProgressIndicator
import androidx.compose.material3.FilterChip
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.OutlinedTextField
import androidx.compose.material3.Scaffold
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
import androidx.compose.runtime.rememberCoroutineScope
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.LocalClipboardManager
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.text.input.ImeAction
import androidx.compose.ui.text.rememberTextMeasurer
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.core.AppLanguage
import com.skycoin.skywire.core.ThemeMode
import com.skycoin.skywire.ui.components.Biometrics
import com.skycoin.skywire.ui.components.InfoRow
import com.skycoin.skywire.ui.components.SecureWindow
import com.skycoin.skywire.ui.components.SectionCard
import com.skycoin.skywire.ui.components.HelpTopic
import com.skycoin.skywire.ui.components.SkyTopBar
import com.skycoin.skywire.ui.components.findFragmentActivity
import com.skycoin.skywire.ui.components.shortPk
import kotlinx.coroutines.launch

/**
 * Settings: who this visor is, how to get its config off the phone, what
 * guards the app, and where the logs are collected.
 *
 * The identity operations are the reason this screen is careful. Both of them
 * end the identity this phone has — replacing the key is not "editing a
 * setting", it is becoming a different visor — so both are asked twice, with
 * the consequence spelled out rather than implied by the word *destructive*.
 * The key handling itself is never done here: the core binary validates the
 * key and derives its public half, and the confirmation quotes what it said.
 *
 * One rule for the buttons, so a card's actions never look like two different
 * kinds of thing: everything a card offers is a [FilledTonalButton], whatever
 * its emphasis, and [TextButton] is left to the dialogs, where the platform
 * expects it. A text button inside a card reads as a hyperlink in a paragraph,
 * which is not what "Open security settings" or "Not now" are.
 */
@Composable
fun SettingsScreen(
    onBack: () -> Unit,
    onOpenDiagnostics: () -> Unit,
    viewModel: SettingsViewModel = viewModel(),
) {
    val state by viewModel.uiState.collectAsState()
    val context = LocalContext.current
    val scope = rememberCoroutineScope()
    val snackbar = remember { SnackbarHostState() }
    var dialog by remember { mutableStateOf<SettingsDialog?>(null) }

    // What the phone can actually check against. Re-read on every composition
    // of this screen, because the answer changes outside the app: enrolling a
    // fingerprint or setting a PIN happens in system settings.
    LaunchedEffect(Unit) {
        viewModel.setBiometricsAvailable(Biometrics.canAuthenticate(context))
    }

    LaunchedEffect(state.message) {
        state.message?.let { message ->
            snackbar.showSnackbar(message)
            viewModel.messageShown()
        }
    }

    val exportPicker = rememberLauncherForActivityResult(
        ActivityResultContracts.CreateDocument("application/json"),
    ) { uri -> uri?.let(viewModel::exportConfig) }

    /** Ask, then run — or run anyway on a phone with nothing to ask with. */
    val confirmBiometrically: (Int, () -> Unit) -> Unit = { titleRes, onConfirmed ->
        val activity = context.findFragmentActivity()
        if (activity == null || !Biometrics.canAuthenticate(context)) {
            // A device with no screen lock and no enrolled biometric has no
            // check to offer. Refusing the action would be a lockout with no
            // security gained — the warning that precedes it is the gate.
            onConfirmed()
        } else {
            Biometrics.prompt(
                activity,
                title = context.getString(titleRes),
                subtitle = context.getString(R.string.settings_biometric_subtitle),
            ) { success, error ->
                if (success) onConfirmed() else error?.let(viewModel::report)
            }
        }
    }

    Scaffold(
        snackbarHost = { SnackbarHost(snackbar) },
        topBar = {
            SkyTopBar(
                title = stringResource(R.string.tab_settings),
                onBack = onBack,
                help = HelpTopic(R.string.help_settings_title, R.string.help_settings_body),
            )
        },
    ) { padding ->
        LazyColumn(
            modifier = Modifier.padding(padding),
            contentPadding = PaddingValues(horizontal = 20.dp, vertical = 16.dp),
            verticalArrangement = Arrangement.spacedBy(16.dp),
        ) {
            item {
                IdentityCard(
                    state = state,
                    onReplace = { dialog = SettingsDialog.EnterSecretKey },
                    onReset = { dialog = SettingsDialog.NewIdentityWarning },
                )
            }
            item {
                ConfigCard(
                    state = state,
                    onExport = { dialog = SettingsDialog.ExportWarning },
                    onToggleEncryption = { wanted ->
                        // Turning it ON is not destructive and needs no
                        // ceremony. Turning it OFF puts the secret key back on
                        // the disk in the clear, which is a security decision
                        // being reversed — so it is confirmed the same way the
                        // app lock's own reversal is.
                        if (wanted) {
                            viewModel.setConfigEncrypted(true)
                        } else {
                            confirmBiometrically(R.string.settings_encrypt_disable_prompt) {
                                viewModel.setConfigEncrypted(false)
                            }
                        }
                    },
                )
            }
            item {
                PublicAutoconnectCard(
                    state = state,
                    onToggle = viewModel::setPublicAutoconnect,
                )
            }
            item {
                RemoteManagementCard(
                    state = state,
                    onGrant = { dialog = SettingsDialog.EnterRemotePk },
                    onRevoke = viewModel::revokeRemoteManagement,
                )
            }
            item {
                AppLockCard(
                    state = state,
                    onToggle = { wanted ->
                        confirmBiometrically(
                            if (wanted) R.string.settings_lock_enable_prompt
                            else R.string.settings_lock_disable_prompt,
                        ) { viewModel.setAppLock(wanted) }
                    },
                    onOpenSecuritySettings = {
                        runCatching {
                            context.startActivity(Intent(Settings.ACTION_SECURITY_SETTINGS))
                        }.onFailure { viewModel.report(context.getString(R.string.settings_no_security_screen)) }
                    },
                )
            }
            item {
                BatteryCard(
                    state = state,
                    onGrant = viewModel::requestBatteryExemption,
                    onDismiss = viewModel::dismissBatteryPrompt,
                )
            }
            item { ThemeCard(state, viewModel::setThemeMode) }
            item {
                LanguageCard(state) { language ->
                    // Below API 33 nothing applies the choice on its own — the
                    // Activity has to be built again on it. See AppLocale.
                    if (viewModel.setLanguage(language)) {
                        context.findFragmentActivity()?.recreate()
                    }
                }
            }
            item { DiagnosticsRow(onOpenDiagnostics) }
            item { AboutCard(state) }
        }
    }

    // --- the dialogs, in the order each flow walks them ---

    when (val open = dialog) {
        null -> Unit

        SettingsDialog.EnterSecretKey -> SecretKeyDialog(
            busy = state.busy,
            onDismiss = { dialog = null },
            onSubmit = { entered ->
                scope.launch {
                    val pk = viewModel.publicKeyOf(entered)
                    when {
                        pk == null -> Unit // the failure is already on its way to the snackbar
                        viewModel.isCurrentKey(pk) -> {
                            viewModel.report(context.getString(R.string.settings_sk_unchanged))
                            dialog = null
                        }
                        // Nothing to lose yet: with no config on disk this is
                        // not replacing an identity, it is choosing the first
                        // one. The final confirmation still stands.
                        !state.hasIdentity -> dialog = SettingsDialog.ReplaceFinal(entered, pk)
                        else -> dialog = SettingsDialog.ReplaceWarning(entered, pk)
                    }
                }
            },
        )

        SettingsDialog.EnterRemotePk -> RemotePkDialog(
            onDismiss = { dialog = null },
            onSubmit = { entered ->
                viewModel.grantRemoteManagement(entered)
                dialog = null
            },
        )

        is SettingsDialog.ReplaceWarning -> DestructiveDialog(
            title = stringResource(R.string.settings_replace_title),
            body = stringResource(R.string.settings_identity_loss, shortPk(state.publicKey)),
            extra = stringResource(R.string.settings_replace_new_pk, shortPk(open.publicKey)),
            confirm = stringResource(R.string.settings_continue),
            onDismiss = { dialog = null },
            onConfirm = { dialog = SettingsDialog.ReplaceFinal(open.secretKey, open.publicKey) },
        )

        is SettingsDialog.ReplaceFinal -> DestructiveDialog(
            title = stringResource(R.string.settings_replace_final_title),
            body = stringResource(R.string.settings_replace_final_body, shortPk(open.publicKey)),
            confirm = stringResource(R.string.settings_replace_action),
            onDismiss = { dialog = null },
            onConfirm = {
                viewModel.replaceSecretKey(open.secretKey)
                dialog = null
            },
        )

        SettingsDialog.NewIdentityWarning -> DestructiveDialog(
            title = stringResource(R.string.settings_new_title),
            body = stringResource(R.string.settings_identity_loss, shortPk(state.publicKey)),
            extra = stringResource(R.string.settings_new_extra),
            confirm = stringResource(R.string.settings_continue),
            onDismiss = { dialog = null },
            onConfirm = { dialog = SettingsDialog.NewIdentityFinal },
        )

        SettingsDialog.NewIdentityFinal -> DestructiveDialog(
            title = stringResource(R.string.settings_new_final_title),
            body = stringResource(R.string.settings_new_final_body),
            confirm = stringResource(R.string.settings_new_action),
            onDismiss = { dialog = null },
            onConfirm = {
                viewModel.newIdentity()
                dialog = null
            },
        )

        SettingsDialog.ExportWarning -> DestructiveDialog(
            title = stringResource(R.string.settings_export_title),
            body = stringResource(R.string.settings_export_warning),
            confirm = stringResource(R.string.settings_continue),
            onDismiss = { dialog = null },
            onConfirm = {
                dialog = null
                confirmBiometrically(R.string.settings_export_prompt) {
                    exportPicker.launch(EXPORT_FILE_NAME)
                }
            },
        )
    }
}

/** Which dialog is open, and what it is carrying. */
private sealed interface SettingsDialog {
    data object EnterSecretKey : SettingsDialog
    data object EnterRemotePk : SettingsDialog
    data class ReplaceWarning(val secretKey: String, val publicKey: String) : SettingsDialog
    data class ReplaceFinal(val secretKey: String, val publicKey: String) : SettingsDialog
    data object NewIdentityWarning : SettingsDialog
    data object NewIdentityFinal : SettingsDialog
    data object ExportWarning : SettingsDialog
}

// --- cards ---

@Composable
private fun IdentityCard(
    state: SettingsUiState,
    onReplace: () -> Unit,
    onReset: () -> Unit,
) {
    val clipboard = LocalClipboardManager.current
    val context = LocalContext.current
    val copied = stringResource(R.string.copied_to_clipboard)

    SectionCard {
        Text(stringResource(R.string.settings_identity), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.settings_identity_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))

        if (state.hasIdentity) {
            InfoRow(
                label = stringResource(R.string.visor_public_key),
                value = shortPk(state.publicKey),
                mono = true,
                modifier = Modifier.clickable {
                    clipboard.setText(AnnotatedString(state.publicKey))
                    Toast.makeText(context, copied, Toast.LENGTH_SHORT).show()
                },
            )
        } else {
            Text(
                stringResource(R.string.settings_identity_none),
                style = MaterialTheme.typography.bodyMedium,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }

        Spacer(Modifier.height(12.dp))
        if (state.busy) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                CircularProgressIndicator(Modifier.size(14.dp), strokeWidth = 2.dp)
                Spacer(Modifier.width(10.dp))
                Text(
                    stringResource(R.string.settings_identity_working),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
        } else {
            val replaceLabel = stringResource(R.string.settings_replace_sk)
            val resetLabel = stringResource(R.string.settings_new_config)
            // Equal halves gave the longer label half a row to sit in, which is
            // less than it needs — it wrapped to two lines and left its
            // neighbour a short button beside a tall one. Weighting by the
            // measured label widths instead gives each one the share of the row
            // its text actually asks for: both stay on one line, and the two
            // together still fill the width with only the gutter between them.
            val measurer = rememberTextMeasurer()
            val labelStyle = MaterialTheme.typography.labelLarge
            val replaceWidth = remember(replaceLabel, labelStyle) {
                measurer.measure(replaceLabel, labelStyle).size.width.toFloat()
            }
            val resetWidth = remember(resetLabel, labelStyle) {
                measurer.measure(resetLabel, labelStyle).size.width.toFloat()
            }
            Row(
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                modifier = Modifier.fillMaxWidth(),
            ) {
                // Trimmed from the 24dp default: the padding is what decides
                // whether a proportional share is wide enough for its label.
                val padding = PaddingValues(horizontal = 12.dp, vertical = 8.dp)
                FilledTonalButton(
                    onClick = onReplace,
                    contentPadding = padding,
                    modifier = Modifier.weight(replaceWidth),
                ) {
                    Text(replaceLabel, maxLines = 1)
                }
                FilledTonalButton(
                    onClick = onReset,
                    enabled = state.hasIdentity,
                    contentPadding = padding,
                    modifier = Modifier.weight(resetWidth),
                ) {
                    Text(resetLabel, maxLines = 1)
                }
            }
        }
    }
}

@Composable
private fun ConfigCard(
    state: SettingsUiState,
    onExport: () -> Unit,
    onToggleEncryption: (Boolean) -> Unit,
) {
    SectionCard {
        Text(stringResource(R.string.settings_config), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.settings_config_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        FilledTonalButton(onClick = onExport, enabled = state.hasIdentity && !state.busy) {
            Text(stringResource(R.string.settings_export))
        }

        // Encryption at rest sits in this card rather than its own, because it
        // is a property of the file the card is already about.
        Spacer(Modifier.height(16.dp))
        HorizontalDivider()
        Spacer(Modifier.height(16.dp))
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Column(Modifier.weight(1f)) {
                Text(
                    stringResource(R.string.settings_encrypt),
                    style = MaterialTheme.typography.titleSmall,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    stringResource(R.string.settings_encrypt_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(12.dp))
            Switch(
                checked = state.configEncrypted,
                onCheckedChange = onToggleEncryption,
                enabled = !state.busy,
            )
        }
        if (state.configEncrypted) {
            Spacer(Modifier.height(10.dp))
            Text(
                stringResource(R.string.settings_encrypt_warning),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

/**
 * Automatic transports to public visors.
 *
 * Off on this build — see [com.skycoin.skywire.core.PublicAutoconnect] for
 * why a phone opts out by default. Worth surfacing rather than leaving buried
 * in the config: it is the switch that decides whether this visor holds any
 * transports of its own, and a visor holding none cannot dial a route through
 * intermediates at all, which is what a route length above one hop asks for.
 */
@Composable
private fun PublicAutoconnectCard(
    state: SettingsUiState,
    onToggle: (Boolean) -> Unit,
) {
    SectionCard {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Column(Modifier.weight(1f)) {
                Text(
                    stringResource(R.string.settings_autoconnect),
                    style = MaterialTheme.typography.titleMedium,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    stringResource(R.string.settings_autoconnect_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(12.dp))
            Switch(checked = state.publicAutoconnect, onCheckedChange = onToggle)
        }
        Spacer(Modifier.height(10.dp))
        Text(
            stringResource(R.string.settings_autoconnect_restart),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/**
 * The remote-management grant — see [com.skycoin.skywire.core.RemoteManagement].
 *
 * Rendered as a grant rather than a toggle: what is being switched on is not
 * a feature of this phone but another machine's full control of it, so the
 * card always shows *which* key holds that control, and revoking it is one
 * tap with no key to retype.
 */
@Composable
private fun RemoteManagementCard(
    state: SettingsUiState,
    onGrant: () -> Unit,
    onRevoke: () -> Unit,
) {
    SectionCard {
        Text(
            stringResource(R.string.settings_remote),
            style = MaterialTheme.typography.titleMedium,
        )
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.settings_remote_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        // Both of these are buttons, not text links. Handing another machine
        // control of this one — and taking it back — is the same weight of
        // action as exporting the config or allowing background work, and it
        // is styled like them rather than like a hyperlink in a paragraph.
        if (state.remoteManagementPk.isEmpty()) {
            FilledTonalButton(onClick = onGrant) {
                Text(stringResource(R.string.settings_remote_grant))
            }
        } else {
            Row(
                verticalAlignment = Alignment.CenterVertically,
                modifier = Modifier.fillMaxWidth(),
            ) {
                Text(
                    shortPk(state.remoteManagementPk),
                    style = MaterialTheme.typography.bodyMedium.copy(
                        fontFamily = FontFamily.Monospace,
                    ),
                    modifier = Modifier.weight(1f),
                )
                Spacer(Modifier.width(12.dp))
                FilledTonalButton(onClick = onRevoke) {
                    Text(stringResource(R.string.settings_remote_revoke))
                }
            }
        }
        Spacer(Modifier.height(6.dp))
        Text(
            stringResource(R.string.settings_remote_restart),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
    }
}

/** Paste-a-key dialog for [RemoteManagementCard]. A public key, so no [SecureWindow]. */
@Composable
private fun RemotePkDialog(
    onDismiss: () -> Unit,
    onSubmit: (String) -> Unit,
) {
    var text by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.settings_remote_dialog_title)) },
        text = {
            Column {
                Text(
                    stringResource(R.string.settings_remote_dialog_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it.trim() },
                    singleLine = false,
                    maxLines = 3,
                    label = { Text(stringResource(R.string.settings_remote_label)) },
                    textStyle = MaterialTheme.typography.bodyMedium.copy(
                        fontFamily = FontFamily.Monospace,
                    ),
                    keyboardOptions = KeyboardOptions(
                        imeAction = ImeAction.Done,
                    ),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onSubmit(text) },
                enabled = text.isNotEmpty(),
            ) {
                Text(stringResource(R.string.settings_remote_grant_confirm))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
        },
    )
}

@Composable
private fun AppLockCard(
    state: SettingsUiState,
    onToggle: (Boolean) -> Unit,
    onOpenSecuritySettings: () -> Unit,
) {
    SectionCard {
        Row(verticalAlignment = Alignment.CenterVertically, modifier = Modifier.fillMaxWidth()) {
            Column(Modifier.weight(1f)) {
                Text(
                    stringResource(R.string.settings_app_lock),
                    style = MaterialTheme.typography.titleMedium,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    stringResource(R.string.settings_app_lock_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Spacer(Modifier.width(12.dp))
            Switch(
                checked = state.appLockEnabled,
                onCheckedChange = onToggle,
                enabled = state.biometricsAvailable,
            )
        }
        if (!state.biometricsAvailable) {
            Spacer(Modifier.height(10.dp))
            Text(
                stringResource(R.string.settings_app_lock_unavailable),
                style = MaterialTheme.typography.bodySmall,
                color = MaterialTheme.colorScheme.onSurfaceVariant,
            )
            Spacer(Modifier.height(12.dp))
            FilledTonalButton(onClick = onOpenSecuritySettings) {
                Text(stringResource(R.string.settings_open_security))
            }
        }
    }
}

@Composable
private fun ThemeCard(state: SettingsUiState, onPick: (ThemeMode) -> Unit) {
    SectionCard {
        Text(stringResource(R.string.settings_theme), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(12.dp))
        Row(horizontalArrangement = Arrangement.spacedBy(8.dp)) {
            ThemeMode.entries.forEach { mode ->
                FilterChip(
                    selected = state.themeMode == mode,
                    onClick = { onPick(mode) },
                    label = { Text(stringResource(themeLabel(mode))) },
                )
            }
        }
    }
}

private fun themeLabel(mode: ThemeMode): Int = when (mode) {
    ThemeMode.SYSTEM -> R.string.settings_theme_system
    ThemeMode.LIGHT -> R.string.settings_theme_light
    ThemeMode.DARK -> R.string.settings_theme_dark
}

/**
 * The interface language, built from [AppLanguage.entries] so that shipping a
 * translation is a resource folder and an enum constant — this screen needs no
 * edit for the second one.
 *
 * Each language is labelled in its own script. The user most likely to come
 * looking is the one who cannot read the language currently on screen.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
private fun LanguageCard(state: SettingsUiState, onPick: (AppLanguage) -> Unit) {
    SectionCard {
        Text(stringResource(R.string.settings_language), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(R.string.settings_language_hint),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        Spacer(Modifier.height(12.dp))
        // Wrapping: language names are as long as their own script makes them,
        // and the list grows with every translation added.
        FlowRow(
            horizontalArrangement = Arrangement.spacedBy(8.dp),
            verticalArrangement = Arrangement.spacedBy(8.dp),
        ) {
            AppLanguage.entries.forEach { language ->
                FilterChip(
                    selected = state.language == language,
                    onClick = { onPick(language) },
                    label = { Text(stringResource(languageLabel(language))) },
                )
            }
        }
    }
}

private fun languageLabel(language: AppLanguage): Int = when (language) {
    AppLanguage.SYSTEM -> R.string.settings_language_system
    AppLanguage.ENGLISH -> R.string.settings_language_en
    AppLanguage.CHINESE_SIMPLIFIED -> R.string.settings_language_zh_cn
}

@Composable
private fun DiagnosticsRow(onOpen: () -> Unit) {
    SectionCard {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            modifier = Modifier
                .fillMaxWidth()
                .clickable(onClick = onOpen),
        ) {
            Column(Modifier.weight(1f)) {
                Text(
                    stringResource(R.string.settings_diagnostics),
                    style = MaterialTheme.typography.titleMedium,
                )
                Spacer(Modifier.height(4.dp))
                Text(
                    stringResource(R.string.settings_diagnostics_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }
            Icon(
                Icons.AutoMirrored.Filled.KeyboardArrowRight,
                contentDescription = null,
                tint = MaterialTheme.colorScheme.onSurfaceVariant,
            )
        }
    }
}

@Composable
private fun AboutCard(state: SettingsUiState) {
    SectionCard {
        Text(stringResource(R.string.settings_about), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(8.dp))
        InfoRow(
            label = stringResource(R.string.settings_app_version),
            value = state.appVersion.ifEmpty { "—" },
        )
        InfoRow(
            label = stringResource(R.string.settings_core_version),
            value = state.coreVersion.ifEmpty { "—" },
        )
    }
}

// --- dialogs ---

/** Paste the key. Nothing is validated here — the core binary is asked. */
/**
 * Doze, and what to do about it. Shown in both states rather than only when
 * something is wrong: "the system may pause Skywire in the background" is
 * worth knowing even once it has been dealt with, and a card that vanishes on
 * success leaves a user who granted it wondering whether it took.
 *
 * The "Not now" button is what stops this being a nag — it silences the Home
 * prompt too. The card itself stays, because Settings is where you go looking.
 */
@Composable
private fun BatteryCard(
    state: SettingsUiState,
    onGrant: () -> Unit,
    onDismiss: () -> Unit,
) {
    SectionCard {
        Text(stringResource(R.string.settings_battery), style = MaterialTheme.typography.titleMedium)
        Spacer(Modifier.height(4.dp))
        Text(
            stringResource(
                if (state.batteryExempt) R.string.settings_battery_exempt_hint
                else R.string.settings_battery_hint,
            ),
            style = MaterialTheme.typography.bodySmall,
            color = MaterialTheme.colorScheme.onSurfaceVariant,
        )
        if (!state.batteryExempt) {
            Spacer(Modifier.height(12.dp))
            Row(horizontalArrangement = Arrangement.spacedBy(12.dp)) {
                FilledTonalButton(onClick = onGrant) {
                    Text(stringResource(R.string.settings_battery_allow))
                }
                if (!state.batteryPromptDismissed) {
                    FilledTonalButton(onClick = onDismiss) {
                        Text(stringResource(R.string.settings_battery_not_now))
                    }
                }
            }
        }
    }
}

@Composable
private fun SecretKeyDialog(
    busy: Boolean,
    onDismiss: () -> Unit,
    onSubmit: (String) -> Unit,
) {
    // The field below holds a visor secret key in the clear. That is the same
    // class of secret as the wallet's twelve words — it *is* the identity,
    // it cannot be reissued, and anyone who reads it owns this visor — so it
    // gets the same screenshot and recents-thumbnail block the seed screens
    // have always had. It did not, until this audit.
    SecureWindow()
    var text by remember { mutableStateOf("") }
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(R.string.settings_replace_sk)) },
        text = {
            Column {
                Text(
                    stringResource(R.string.settings_sk_hint),
                    style = MaterialTheme.typography.bodySmall,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
                Spacer(Modifier.height(12.dp))
                OutlinedTextField(
                    value = text,
                    onValueChange = { text = it.trim() },
                    singleLine = false,
                    maxLines = 3,
                    label = { Text(stringResource(R.string.settings_sk_label)) },
                    textStyle = MaterialTheme.typography.bodyMedium.copy(
                        fontFamily = FontFamily.Monospace,
                    ),
                    keyboardOptions = KeyboardOptions(
                        imeAction = ImeAction.Done,
                    ),
                    modifier = Modifier.fillMaxWidth(),
                )
            }
        },
        confirmButton = {
            TextButton(
                onClick = { onSubmit(text) },
                enabled = text.isNotEmpty() && !busy,
            ) {
                Text(stringResource(R.string.settings_continue))
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
        },
    )
}

/**
 * One step of a two-step confirmation. Both steps look the same on purpose —
 * the second is not a formality to click through, it is the same question
 * asked once the user has read what the first one said.
 */
@Composable
private fun DestructiveDialog(
    title: String,
    body: String,
    confirm: String,
    onDismiss: () -> Unit,
    onConfirm: () -> Unit,
    extra: String? = null,
) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(title) },
        text = {
            Column {
                Text(body, style = MaterialTheme.typography.bodyMedium)
                extra?.let {
                    Spacer(Modifier.height(12.dp))
                    Text(
                        it,
                        style = MaterialTheme.typography.bodyMedium,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
        },
        confirmButton = {
            TextButton(onClick = onConfirm) {
                Text(confirm, color = MaterialTheme.colorScheme.error)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.cancel)) }
        },
    )
}

private const val EXPORT_FILE_NAME = "skywire-config.json"

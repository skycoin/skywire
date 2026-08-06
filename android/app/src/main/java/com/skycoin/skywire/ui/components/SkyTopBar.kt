package com.skycoin.skywire.ui.components

import androidx.annotation.StringRes
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.filled.ArrowBack
import androidx.compose.material.icons.automirrored.outlined.HelpOutline
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.CenterAlignedTopAppBar
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.R

/**
 * What the ? in a screen's header explains: what this screen is for, in a few
 * sentences, read once and then never again. String resources rather than
 * free text, so the copy lives in strings.xml with everything else the user
 * reads.
 *
 * The screens that used to carry a `Logs` action say where the logs went in
 * here instead — see [SkyTopBar] for why the action was removed.
 */
data class HelpTopic(
    @StringRes val title: Int,
    @StringRes val body: Int,
)

/**
 * The app's top bar, and the same one on every screen that has a title: a
 * circular back button at the left, the name centred, a circled ? at the
 * right wherever the screen has something worth saying about itself.
 *
 * The two round buttons are the same size and weight on purpose. A bar whose
 * right-hand side is whatever actions a screen happened to want is a bar
 * whose title is never quite centred and never quite in the same place twice;
 * with a matched pair the title sits still as the user moves between screens.
 *
 * The ? took the place of a per-screen `Logs` action on SkyChat, SkySOCKS,
 * SkyVPN and SkyDEX. A log viewer is not something wanted from the screen
 * being used — it is wanted when something is wrong, and then all of them are
 * wanted at once, which is the list Settings ▸ Diagnostics already keeps.
 * Fleet keeps its own per-visor Logs button: that is a remote machine's feed
 * arriving over dmsg, and Diagnostics only knows about this phone.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SkyTopBar(
    title: String,
    onBack: (() -> Unit)? = null,
    help: HelpTopic? = null,
    actions: @Composable androidx.compose.foundation.layout.RowScope.() -> Unit = {},
) {
    var helpOpen by remember { mutableStateOf(false) }

    CenterAlignedTopAppBar(
        title = {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                fontWeight = FontWeight.Bold,
            )
        },
        navigationIcon = {
            if (onBack != null) {
                RoundBarButton(
                    onClick = onBack,
                    modifier = Modifier.padding(start = 8.dp),
                ) {
                    Icon(
                        Icons.AutoMirrored.Filled.ArrowBack,
                        contentDescription = stringResource(R.string.back),
                    )
                }
            }
        },
        actions = {
            actions()
            if (help != null) {
                RoundBarButton(
                    onClick = { helpOpen = true },
                    modifier = Modifier.padding(end = 8.dp),
                ) {
                    Icon(
                        Icons.AutoMirrored.Outlined.HelpOutline,
                        contentDescription = stringResource(R.string.help_open),
                    )
                }
            }
        },
        colors = TopAppBarDefaults.topAppBarColors(
            containerColor = MaterialTheme.colorScheme.background,
            titleContentColor = MaterialTheme.colorScheme.onBackground,
        ),
    )

    if (help != null && helpOpen) {
        HelpDialog(topic = help, onDismiss = { helpOpen = false })
    }
}

/** The one button shape in the bar: a 40dp tonal circle. */
@Composable
private fun RoundBarButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    FilledTonalIconButton(
        onClick = onClick,
        modifier = modifier.size(40.dp),
        colors = IconButtonDefaults.filledTonalIconButtonColors(
            containerColor = MaterialTheme.colorScheme.surfaceVariant,
            contentColor = MaterialTheme.colorScheme.onSurface,
        ),
        content = { content() },
    )
}

/**
 * The ? sheet. Scrolls, because the longest of these runs past a short phone
 * held in landscape, and a help text that cannot be reached the end of is
 * worse than no help text.
 */
@Composable
private fun HelpDialog(topic: HelpTopic, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        title = { Text(stringResource(topic.title)) },
        text = {
            Text(
                text = stringResource(topic.body),
                style = MaterialTheme.typography.bodyMedium,
                modifier = Modifier.verticalScroll(rememberScrollState()),
            )
        },
        confirmButton = {
            TextButton(onClick = onDismiss) { Text(stringResource(R.string.help_close)) }
        },
    )
}

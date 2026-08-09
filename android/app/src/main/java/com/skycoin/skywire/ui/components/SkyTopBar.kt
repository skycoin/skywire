package com.skycoin.skywire.ui.components

import androidx.annotation.StringRes
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.RowScope
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.statusBarsPadding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.automirrored.rounded.ArrowBack
import androidx.compose.material.icons.automirrored.rounded.HelpOutline
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.FilledTonalIconButton
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButtonDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.style.TextOverflow
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
 * The app's header, the same one on every screen that has a title: a rounded-
 * square back button at the left, the name beside it — left-aligned, with an
 * optional one-line [subtitle] under it for the screen's living fact ("6
 * installed · 1 running") — and a matching ? button at the right wherever the
 * screen has something worth saying about itself.
 *
 * The two square buttons are the same size and weight on purpose: with a
 * matched pair the title column sits still as the user moves between screens,
 * whatever a screen happens to put on the right.
 *
 * The ? took the place of a per-screen `Logs` action on SkyChat, SkySOCKS,
 * SkyVPN and SkyDEX. A log viewer is not something wanted from the screen
 * being used — it is wanted when something is wrong, and then all of them are
 * wanted at once, which is the list Settings ▸ Diagnostics already keeps.
 * Fleet keeps its own per-visor Logs button: that is a remote machine's feed
 * arriving over dmsg, and Diagnostics only knows about this phone.
 */
@Composable
fun SkyTopBar(
    title: String,
    subtitle: String? = null,
    onBack: (() -> Unit)? = null,
    help: HelpTopic? = null,
    actions: @Composable RowScope.() -> Unit = {},
) {
    var helpOpen by remember { mutableStateOf(false) }

    Row(
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(12.dp),
        modifier = Modifier
            .fillMaxWidth()
            .background(MaterialTheme.colorScheme.background)
            .statusBarsPadding()
            .padding(horizontal = 16.dp, vertical = 10.dp),
    ) {
        if (onBack != null) {
            SquareBarButton(onClick = onBack) {
                Icon(
                    Icons.AutoMirrored.Rounded.ArrowBack,
                    contentDescription = stringResource(R.string.back),
                )
            }
        }
        Column(Modifier.weight(1f)) {
            Text(
                text = title,
                style = MaterialTheme.typography.titleLarge,
                maxLines = 1,
                overflow = TextOverflow.Ellipsis,
            )
            if (subtitle != null) {
                Text(
                    text = subtitle,
                    style = MaterialTheme.typography.labelMedium,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                    maxLines = 1,
                    overflow = TextOverflow.Ellipsis,
                )
            }
        }
        actions()
        if (help != null) {
            SquareBarButton(onClick = { helpOpen = true }) {
                Icon(
                    Icons.AutoMirrored.Rounded.HelpOutline,
                    contentDescription = stringResource(R.string.help_open),
                )
            }
        }
    }

    if (help != null && helpOpen) {
        HelpDialog(topic = help, onDismiss = { helpOpen = false })
    }
}

/** The one button shape in the bar: a 42dp tonal rounded square. */
@Composable
fun SquareBarButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    FilledTonalIconButton(
        onClick = onClick,
        shape = MaterialTheme.shapes.medium,
        modifier = modifier.size(42.dp),
        colors = IconButtonDefaults.filledTonalIconButtonColors(
            containerColor = MaterialTheme.colorScheme.secondaryContainer,
            contentColor = MaterialTheme.colorScheme.onSecondaryContainer,
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

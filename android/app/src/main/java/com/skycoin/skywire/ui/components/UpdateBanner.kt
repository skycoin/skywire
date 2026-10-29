package com.skycoin.skywire.ui.components

import androidx.compose.animation.AnimatedContent
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.animation.togetherWith
import androidx.compose.foundation.BorderStroke
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.rounded.SystemUpdate
import androidx.compose.material3.Button
import androidx.compose.material3.FilledTonalButton
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.LiveRegionMode
import androidx.compose.ui.semantics.liveRegion
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import com.skycoin.skywire.BuildConfig
import com.skycoin.skywire.R
import com.skycoin.skywire.core.UpdateNotice

/**
 * The one-time "update available" banner, drawn over the top of the current
 * screen. AnimatedContent rather than AnimatedVisibility so the card keeps its
 * text while it slides away after [offer] has gone null.
 */
@Composable
fun UpdateBanner(
    offer: UpdateNotice.Offer?,
    onUpdate: (UpdateNotice.Offer) -> Unit,
    onLater: (UpdateNotice.Offer) -> Unit,
    modifier: Modifier = Modifier,
) {
    AnimatedContent(
        targetState = offer,
        transitionSpec = {
            (slideInVertically { -it } + fadeIn()) togetherWith (slideOutVertically { -it } + fadeOut())
        },
        contentAlignment = Alignment.TopCenter,
        label = "update-banner",
        modifier = modifier,
    ) { shown ->
        if (shown != null) BannerCard(shown, onUpdate, onLater)
    }
}

@Composable
private fun BannerCard(
    offer: UpdateNotice.Offer,
    onUpdate: (UpdateNotice.Offer) -> Unit,
    onLater: (UpdateNotice.Offer) -> Unit,
) {
    val body = when (offer.source) {
        UpdateNotice.Source.DIRECT ->
            stringResource(R.string.update_notice_body, offer.version, BuildConfig.VERSION_NAME)
        UpdateNotice.Source.FDROID ->
            stringResource(R.string.update_notice_body_store, offer.version, "F-Droid", BuildConfig.VERSION_NAME)
        UpdateNotice.Source.PLAY ->
            stringResource(R.string.update_notice_body_store, offer.version, "Google Play", BuildConfig.VERSION_NAME)
    }
    Surface(
        shape = MaterialTheme.shapes.large,
        color = MaterialTheme.colorScheme.surfaceContainerHigh,
        border = BorderStroke(1.dp, MaterialTheme.colorScheme.outlineVariant),
        shadowElevation = 8.dp,
        modifier = Modifier
            .fillMaxWidth()
            .padding(horizontal = 12.dp, vertical = 8.dp)
            .semantics { liveRegion = LiveRegionMode.Polite },
    ) {
        Column(Modifier.padding(16.dp)) {
            Row(verticalAlignment = Alignment.CenterVertically) {
                Icon(
                    Icons.Rounded.SystemUpdate,
                    contentDescription = null,
                    tint = MaterialTheme.colorScheme.primary,
                )
                Spacer(Modifier.width(12.dp))
                Column {
                    Text(
                        stringResource(R.string.update_notice_title),
                        style = MaterialTheme.typography.titleSmall,
                    )
                    Text(
                        body,
                        style = MaterialTheme.typography.bodySmall,
                        color = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
            }
            Spacer(Modifier.height(12.dp))
            Row(
                horizontalArrangement = Arrangement.spacedBy(12.dp),
                modifier = Modifier.align(Alignment.End),
            ) {
                FilledTonalButton(onClick = { onLater(offer) }) {
                    Text(stringResource(R.string.update_notice_later))
                }
                Button(onClick = { onUpdate(offer) }) {
                    Text(stringResource(R.string.update_notice_update))
                }
            }
        }
    }
}

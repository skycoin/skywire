package com.skycoin.skywire.ui.call

import androidx.activity.compose.BackHandler
import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material.icons.Icons
import androidx.compose.material.icons.filled.Call
import androidx.compose.material.icons.filled.CallEnd
import androidx.compose.material.icons.filled.Mic
import androidx.compose.material.icons.filled.MicOff
import androidx.compose.material.icons.filled.Person
import androidx.compose.material.icons.filled.VolumeUp
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Surface
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.collectAsState
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.text.font.FontFamily
import androidx.compose.ui.unit.dp
import androidx.lifecycle.viewmodel.compose.viewModel
import com.skycoin.skywire.R
import com.skycoin.skywire.ui.theme.SkyAccents

/**
 * The call, full screen — in either direction, on every tab.
 *
 * Placing one, being rung, and being in one are the same screen because they
 * are the same event seen at three moments; the controls are what changes.
 * It covers the Chat tab too: the embedded page has its own banner and panel,
 * but a call is not something to notice inside a conversation list, it is what
 * the phone is doing.
 */
@Composable
fun CallScreen(viewModel: CallViewModel = viewModel()) {
    val state by viewModel.uiState.collectAsState()
    val peer = state.peer ?: return

    // Back must not dismiss a call. The way out is Decline or Hang up — a
    // swipe that silently left a live call running behind the UI would be a
    // call the user cannot get back to.
    BackHandler(enabled = true) { }

    Surface(
        modifier = Modifier.fillMaxSize(),
        color = MaterialTheme.colorScheme.surface,
    ) {
        Column(
            modifier = Modifier
                .fillMaxSize()
                .padding(horizontal = 32.dp, vertical = 48.dp),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.SpaceBetween,
        ) {
            Column(horizontalAlignment = Alignment.CenterHorizontally) {
                Spacer(Modifier.height(24.dp))
                Box(
                    modifier = Modifier
                        .size(112.dp)
                        .clip(CircleShape)
                        .background(MaterialTheme.colorScheme.surfaceVariant),
                    contentAlignment = Alignment.Center,
                ) {
                    Icon(
                        Icons.Default.Person,
                        contentDescription = null,
                        modifier = Modifier.size(56.dp),
                        tint = MaterialTheme.colorScheme.onSurfaceVariant,
                    )
                }
                Spacer(Modifier.height(20.dp))
                Text(
                    text = peer,
                    style = MaterialTheme.typography.titleLarge,
                    fontFamily = FontFamily.Monospace,
                )
                Spacer(Modifier.height(8.dp))
                Text(
                    text = when {
                        state.connected -> state.elapsed
                        state.dialing -> stringResource(R.string.call_dialing)
                        else -> stringResource(R.string.call_incoming_title)
                    },
                    style = MaterialTheme.typography.bodyLarge,
                    color = MaterialTheme.colorScheme.onSurfaceVariant,
                )
            }

            when {
                state.connected -> ConnectedControls(
                    micMuted = state.micMuted,
                    speakerphone = state.speakerphone,
                    onToggleMic = viewModel::toggleMic,
                    onToggleSpeaker = viewModel::toggleSpeakerphone,
                    onHangUp = viewModel::hangUp,
                )
                // Placing a call: the only thing to offer is giving up on it,
                // which cancels the invite rather than closing a session.
                state.dialing -> DialingControls(onCancel = viewModel::hangUp)
                else -> RingingControls(
                    onDecline = viewModel::decline,
                    onAnswer = viewModel::answer,
                )
            }
        }
    }
}

@Composable
private fun RingingControls(onDecline: () -> Unit, onAnswer: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceEvenly,
    ) {
        CallButton(
            icon = Icons.Default.CallEnd,
            label = stringResource(R.string.call_decline),
            background = DeclineRed,
            onClick = onDecline,
        )
        CallButton(
            icon = Icons.Default.Call,
            label = stringResource(R.string.call_answer),
            background = AnswerGreen,
            onClick = onAnswer,
        )
    }
}

@Composable
private fun DialingControls(onCancel: () -> Unit) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.Center,
    ) {
        CallButton(
            icon = Icons.Default.CallEnd,
            label = stringResource(R.string.call_hang_up),
            background = DeclineRed,
            onClick = onCancel,
        )
    }
}

@Composable
private fun ConnectedControls(
    micMuted: Boolean,
    speakerphone: Boolean,
    onToggleMic: () -> Unit,
    onToggleSpeaker: () -> Unit,
    onHangUp: () -> Unit,
) {
    Row(
        modifier = Modifier.fillMaxWidth(),
        horizontalArrangement = Arrangement.SpaceEvenly,
        verticalAlignment = Alignment.CenterVertically,
    ) {
        CallButton(
            icon = if (micMuted) Icons.Default.MicOff else Icons.Default.Mic,
            label = stringResource(
                if (micMuted) R.string.call_unmute else R.string.call_mute,
            ),
            background = if (micMuted) MaterialTheme.colorScheme.primary
            else MaterialTheme.colorScheme.surfaceVariant,
            onClick = onToggleMic,
        )
        CallButton(
            icon = Icons.Default.CallEnd,
            label = stringResource(R.string.call_hang_up),
            background = DeclineRed,
            onClick = onHangUp,
        )
        CallButton(
            icon = Icons.Default.VolumeUp,
            label = stringResource(R.string.call_speaker),
            background = if (speakerphone) MaterialTheme.colorScheme.primary
            else MaterialTheme.colorScheme.surfaceVariant,
            onClick = onToggleSpeaker,
        )
    }
}

@Composable
private fun CallButton(
    icon: androidx.compose.ui.graphics.vector.ImageVector,
    label: String,
    background: Color,
    onClick: () -> Unit,
) {
    Column(horizontalAlignment = Alignment.CenterHorizontally) {
        Surface(
            shape = CircleShape,
            color = background,
            onClick = onClick,
            modifier = Modifier.size(72.dp),
        ) {
            Box(contentAlignment = Alignment.Center) {
                Icon(
                    icon,
                    contentDescription = label,
                    modifier = Modifier.size(32.dp),
                    tint = Color.White,
                )
            }
        }
        Spacer(Modifier.height(8.dp))
        Text(label, style = MaterialTheme.typography.labelMedium)
    }
}

// Answer/decline keep their conventional colours in both themes: on a call
// screen these two buttons are read by colour before they are read at all.
// The green is the palette's success accent; the red stays its own — the
// theme's error role shifts between themes and these two must not.
private val AnswerGreen = SkyAccents.success
private val DeclineRed = Color(0xFFD93B34)

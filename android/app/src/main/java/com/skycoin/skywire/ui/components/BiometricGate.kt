package com.skycoin.skywire.ui.components

import androidx.compose.runtime.Composable

/**
 * Placeholder biometric gate so the navigation structure is final from day one.
 * The real BiometricPrompt flow (BIOMETRIC_STRONG | DEVICE_CREDENTIAL, launch +
 * return-from-background with a grace period) replaces the body with the
 * settings/app-lock work; the wallet reuses the same helper for seed reveal
 * and send confirmation.
 */
@Composable
fun BiometricGate(content: @Composable () -> Unit) {
    content()
}

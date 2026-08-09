package com.skycoin.skywire.ui.components

import android.content.Context
import android.content.ContextWrapper
import android.os.Build
import androidx.biometric.BiometricManager
import androidx.biometric.BiometricPrompt
import androidx.core.content.ContextCompat
import androidx.fragment.app.FragmentActivity

/**
 * The one biometric prompt, shared by everything that has to ask.
 *
 * Two callers today — the app lock on launch and return-from-background, and
 * the config export, which writes the secret key to a file the user picks —
 * and the wallet's seed reveal and send confirmation join them later. There is
 * one prompt because there is one question: *is this the person who set this
 * phone up?* The answer is the phone's own, not ours: no key material and no
 * secret of ours is bound to it.
 */
object Biometrics {

    /**
     * Fingerprint, face, iris — whatever the phone has — with the device PIN,
     * pattern or password as the fallback, which is what makes the lock usable
     * on a device with no enrolled biometric at all.
     *
     * The pairing is not free to choose. `BIOMETRIC_STRONG or
     * DEVICE_CREDENTIAL` is rejected outright on API 28-29 (the builder throws
     * "Authenticator combination is unsupported on API n"), and the weak class
     * is the combination androidx's own deprecated `setDeviceCredentialAllowed`
     * resolves to there. Weak means "the phone's own bar for unlocking itself"
     * — which is exactly the bar this asks for.
     */
    private fun allowed(): Int {
        val biometric = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
            BiometricManager.Authenticators.BIOMETRIC_STRONG
        } else {
            BiometricManager.Authenticators.BIOMETRIC_WEAK
        }
        return biometric or BiometricManager.Authenticators.DEVICE_CREDENTIAL
    }

    /**
     * Whether this phone can answer the question at all. False on a device
     * with no screen lock and no enrolled biometric — there is nothing to
     * check against, and no prompt to show.
     */
    fun canAuthenticate(context: Context): Boolean =
        BiometricManager.from(context).canAuthenticate(allowed()) ==
            BiometricManager.BIOMETRIC_SUCCESS

    /**
     * Ask. [onResult] is called exactly once, on the main thread, with true
     * only for a genuine success.
     *
     * A failed *attempt* (a finger the sensor did not recognise) is not a
     * result — the prompt stays up and lets the user try again — so it is not
     * reported here. What is reported is the terminal error, cancellation
     * included, with the system's own wording where there is any: "Too many
     * attempts. Try again later." says more than anything this app could
     * substitute for it.
     */
    fun prompt(
        activity: FragmentActivity,
        title: String,
        subtitle: String? = null,
        description: String? = null,
        onResult: (success: Boolean, error: String?) -> Unit,
    ) {
        val info = BiometricPrompt.PromptInfo.Builder()
            .setTitle(title)
            .apply {
                subtitle?.let(::setSubtitle)
                description?.let(::setDescription)
            }
            .setAllowedAuthenticators(allowed())
            // No negative button: the builder rejects one when device
            // credentials are allowed, because the credential fallback IS the
            // second button.
            .setConfirmationRequired(false)
            .build()

        val prompt = BiometricPrompt(
            activity,
            ContextCompat.getMainExecutor(activity),
            object : BiometricPrompt.AuthenticationCallback() {
                override fun onAuthenticationSucceeded(result: BiometricPrompt.AuthenticationResult) {
                    onResult(true, null)
                }

                override fun onAuthenticationError(code: Int, message: CharSequence) {
                    onResult(false, message.toString())
                }
            },
        )
        prompt.authenticate(info)
    }
}

/**
 * The [FragmentActivity] behind a composable's context, which is what
 * [BiometricPrompt] takes. Null only if a composable is ever hosted outside an
 * Activity — a preview — where asking is meaningless anyway.
 */
fun Context.findFragmentActivity(): FragmentActivity? {
    var context = this
    while (context is ContextWrapper) {
        if (context is FragmentActivity) return context
        context = context.baseContext
    }
    return null
}

package com.skycoin.skywire.core

import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/**
 * Optional encryption of `skywire-config.json` while the core is not running.
 *
 * **What it is for.** That file holds `sk`, the visor's secret key — the whole
 * of this phone's identity on the network. It lives in app-private storage, so
 * on a healthy device no other app can read it; the threat this closes is the
 * device itself being examined. A phone that is off, or unlocked once and then
 * imaged, gives up app-private files to anyone who can defeat file-based
 * encryption at rest, and the identity goes with them. Sealed, the config is
 * AES-256-GCM ciphertext under a key that AndroidKeyStore will not export —
 * so a copy of the filesystem is not a copy of the key.
 *
 * **Why it is optional and off by default.** It buys nothing against a
 * running, unlocked phone (the plaintext exists while the visor runs), it
 * cannot be recovered if the keystore is wiped — a factory reset, or in some
 * OEM cases removing the screen lock — and the honest summary of that trade is
 * "more protection against a seized phone, one more way to lose the identity
 * on a working one". That is the user's call, not a default.
 *
 * **The shape of it.** Two files, never both meaningful at once:
 *  - `skywire-config.json` — plaintext. Exists while the core runs, because
 *    the visor opens the path it was given and rewrites it at runtime.
 *  - `skywire-config.json.enc` — ciphertext. Exists while the core is stopped
 *    and the feature is on.
 *
 * [unseal] before anything reads or runs the config, [seal] once the visor has
 * exited. Sealing at that point rather than at generation is what lets the
 * visor's own rewrites survive: whatever it left behind is what gets encrypted.
 *
 * **The dangerous failure this must not have.** [ConfigManager.ensureConfig]
 * treats a missing config file as "first run" and generates a fresh identity.
 * A sealed config plus a caller who forgot to unseal would therefore look
 * exactly like a new phone, and the user's key would be replaced without a
 * word. Every entry point that touches the config unseals first, and
 * [sealedExists] is checked before any generation, so the failure mode is a
 * refusal rather than a silent new identity.
 */
class ConfigVault(private val paths: SkywirePaths) {

    /** [AppPreferences] key holding the user's choice. */
    companion object {
        const val PREF_KEY = "config_encrypted"

        /** Off until asked for — see the class doc for why. */
        const val DEFAULT = false

        private const val KEYSTORE = "AndroidKeyStore"
        private const val KEY_ALIAS = "skywire_config_at_rest"
        private const val TRANSFORM = "AES/GCM/NoPadding"
        private const val IV_BYTES = 12
    }

    /** True when a sealed config is on disk, whatever the preference says. */
    fun sealedExists(): Boolean = paths.sealedConfigFile.exists()

    /** True when the config is sealed and there is no plaintext beside it. */
    fun isSealed(): Boolean = sealedExists() && !paths.configFile.exists()

    /**
     * Make the plaintext config available, decrypting it if it is sealed.
     * Idempotent, and a no-op when nothing is sealed — which is every call on
     * a phone that never turned this on.
     *
     * A stale plaintext alongside a sealed file means the app died while the
     * core was running. The plaintext is the newer of the two (the visor may
     * have rewritten it), so it wins and the ciphertext is left to be replaced
     * by the next [seal].
     */
    suspend fun unseal(): Result<Unit> = withContext(Dispatchers.IO) {
        if (!sealedExists()) return@withContext Result.success(Unit)
        if (paths.configFile.exists()) return@withContext Result.success(Unit)
        runCatching {
            val plain = decrypt(paths.sealedConfigFile.readBytes())
                ?: error(
                    "the sealed config cannot be opened — this phone's keystore key is gone " +
                        "(a factory reset, or a screen lock that was removed and re-added). " +
                        "The identity in it is unrecoverable; a new one can be generated.",
                )
            writePrivate(paths.configFile, plain)
            paths.sealedConfigFile.delete()
            Unit
        }
    }

    /**
     * Encrypt the plaintext config and remove it, if [enabled]. Called once
     * the visor has exited, so it captures any rewrite the visor performed.
     *
     * Ordering is deliberate: write the ciphertext, verify it reads back, and
     * only then delete the plaintext. A power cut in the middle leaves both
     * files, which [unseal] resolves in favour of the plaintext — the outcome
     * is a config that is briefly not encrypted, never a config that is gone.
     */
    suspend fun seal(enabled: Boolean): Result<Unit> = withContext(Dispatchers.IO) {
        if (!enabled || !paths.configFile.exists()) return@withContext Result.success(Unit)
        runCatching {
            val plain = paths.configFile.readBytes()
            writePrivate(paths.sealedConfigFile, encrypt(plain))
            check(decrypt(paths.sealedConfigFile.readBytes()) != null) {
                "sealed config failed to read back; leaving the plaintext in place"
            }
            paths.configFile.delete()
            Unit
        }
    }

    /**
     * The config as text, from whichever form is on disk. Lets Settings export
     * a config that is currently sealed without unsealing it onto the disk
     * first — the plaintext exists only in this string.
     */
    suspend fun readText(): String = withContext(Dispatchers.IO) { readTextSync() }

    /**
     * Blocking twin of [readText], for the two synchronous readers that
     * predate this class — the public key on the identity screen and the
     * redacted copy in a diagnostics bundle. Both must keep working with the
     * config sealed and the core down, which is exactly when the identity
     * screen is being looked at. One AES-GCM open over a few kilobytes is not
     * work worth a coroutine.
     */
    fun readTextSync(): String {
        if (paths.configFile.exists()) return paths.configFile.readText()
        val sealed = paths.sealedConfigFile
        if (!sealed.exists()) error("no config on disk")
        return String(
            decrypt(sealed.readBytes()) ?: error("the sealed config cannot be opened"),
            Charsets.UTF_8,
        )
    }

    /**
     * Apply a change to the preference. Turning it **on** seals immediately
     * when the core is down, and otherwise waits for the visor to exit — the
     * running visor holds the file open and rewrites it. Turning it **off**
     * always unseals immediately, because leaving a user who just switched
     * encryption off with an encrypted config would be the opposite of what
     * they asked for.
     */
    suspend fun applyPreference(enabled: Boolean, coreRunning: Boolean): Result<Unit> =
        if (enabled) {
            if (coreRunning) Result.success(Unit) else seal(true)
        } else {
            unseal()
        }

    // --- AndroidKeyStore AES-GCM, over bytes rather than strings ---
    //
    // Same construction as SecretStore and WalletSeedStore, its own alias: a
    // config, a password and a recovery phrase must not share a blast radius.
    // Deliberately NOT setUserAuthenticationRequired — the core starts from a
    // boot-completed revival and from the notification, neither of which can
    // show a biometric prompt.

    private fun key(): SecretKey {
        val ks = KeyStore.getInstance(KEYSTORE).apply { load(null) }
        (ks.getKey(KEY_ALIAS, null) as? SecretKey)?.let { return it }
        val gen = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, KEYSTORE)
        gen.init(
            KeyGenParameterSpec.Builder(
                KEY_ALIAS,
                KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT,
            )
                .setBlockModes(KeyProperties.BLOCK_MODE_GCM)
                .setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                .setKeySize(256)
                .build(),
        )
        return gen.generateKey()
    }

    /** `iv || ciphertext` — the IV is generated by the cipher, never reused. */
    private fun encrypt(plain: ByteArray): ByteArray {
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        return cipher.iv + cipher.doFinal(plain)
    }

    private fun decrypt(blob: ByteArray): ByteArray? = runCatching {
        require(blob.size > IV_BYTES)
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(
            Cipher.DECRYPT_MODE,
            key(),
            GCMParameterSpec(128, blob, 0, IV_BYTES),
        )
        cipher.doFinal(blob, IV_BYTES, blob.size - IV_BYTES)
    }.getOrNull()

    /**
     * Write owner-only. App-private storage is already 700, so this is the
     * second lock on the same door — and the one that still holds if the file
     * is ever moved somewhere less strict.
     */
    private fun writePrivate(file: File, bytes: ByteArray) {
        file.writeBytes(bytes)
        runCatching {
            file.setReadable(false, false)
            file.setReadable(true, true)
            file.setWritable(false, false)
            file.setWritable(true, true)
        }
    }
}

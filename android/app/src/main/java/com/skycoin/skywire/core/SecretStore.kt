package com.skycoin.skywire.core

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import android.util.Base64
import androidx.datastore.preferences.core.edit
import androidx.datastore.preferences.core.stringPreferencesKey
import androidx.datastore.preferences.preferencesDataStore
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.sync.Mutex
import kotlinx.coroutines.sync.withLock
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

private val Context.coreDataStore by preferencesDataStore(name = "core")

/**
 * The local-API password: generated once, kept only on this device.
 * The plaintext never touches disk — DataStore holds an AES-GCM ciphertext
 * whose key lives in AndroidKeyStore (non-exportable). allowBackup=false
 * keeps even the ciphertext out of cloud backups.
 */
class SecretStore(private val context: Context) {

    private val mutex = Mutex()

    suspend fun apiPassword(): String = mutex.withLock {
        val prefs = context.coreDataStore.data.first()
        prefs[KEY_API_PASSWORD]?.let { stored ->
            decrypt(stored)?.let { return it }
            // Undecryptable (keystore wiped): fall through and rotate. The
            // visor's users.db is then stale too — ConfigManager resets it
            // when authentication bootstrap fails.
        }
        val fresh = generatePassword()
        context.coreDataStore.edit { it[KEY_API_PASSWORD] = encrypt(fresh) }
        fresh
    }

    /**
     * Random password satisfying the API's policy: 6–64 chars, at least one
     * upper/lower/digit/special, ASCII, every rune ≥ '!' (no spaces).
     */
    private fun generatePassword(): String {
        val upper = "ABCDEFGHJKLMNPQRSTUVWXYZ"
        val lower = "abcdefghijkmnopqrstuvwxyz"
        val digit = "23456789"
        val special = "!@#$%^&*()-_=+[]{}<>.,?/"
        val all = upper + lower + digit + special
        val rnd = SecureRandom()
        val body = (1..20).map { all[rnd.nextInt(all.length)] }
        val guaranteed = listOf(
            upper[rnd.nextInt(upper.length)],
            lower[rnd.nextInt(lower.length)],
            digit[rnd.nextInt(digit.length)],
            special[rnd.nextInt(special.length)],
        )
        return (body + guaranteed).shuffled(rnd.asKotlinRandom()).joinToString("")
    }

    // --- AndroidKeyStore AES-GCM ---

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

    private fun encrypt(plain: String): String {
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(Cipher.ENCRYPT_MODE, key())
        val ct = cipher.doFinal(plain.toByteArray(Charsets.UTF_8))
        return Base64.encodeToString(cipher.iv, Base64.NO_WRAP) + ":" +
            Base64.encodeToString(ct, Base64.NO_WRAP)
    }

    private fun decrypt(stored: String): String? = runCatching {
        val (ivB64, ctB64) = stored.split(":", limit = 2).also { require(it.size == 2) }
        val cipher = Cipher.getInstance(TRANSFORM)
        cipher.init(
            Cipher.DECRYPT_MODE,
            key(),
            GCMParameterSpec(128, Base64.decode(ivB64, Base64.NO_WRAP)),
        )
        String(cipher.doFinal(Base64.decode(ctB64, Base64.NO_WRAP)), Charsets.UTF_8)
    }.getOrNull()

    private fun SecureRandom.asKotlinRandom(): kotlin.random.Random =
        object : kotlin.random.Random() {
            override fun nextBits(bitCount: Int): Int =
                this@asKotlinRandom.nextInt() ushr (32 - bitCount)
        }

    private companion object {
        const val KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "skywire_api_password"
        const val TRANSFORM = "AES/GCM/NoPadding"
        val KEY_API_PASSWORD = stringPreferencesKey("api_password")
    }
}

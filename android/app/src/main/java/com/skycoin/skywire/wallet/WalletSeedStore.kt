package com.skycoin.skywire.wallet

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
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

private val Context.walletDataStore by preferencesDataStore(name = "wallet")

/**
 * Recovery phrases, sealed at rest. Same construction as [com.skycoin.skywire.core.SecretStore]
 * — AES-256-GCM under a non-exportable AndroidKeyStore key — but a separate
 * key alias and a separate store: coins and service passwords must not share
 * a blast radius. The phrase exists in plaintext only in memory, on its way
 * to derivation or signing; every send and every reveal is additionally
 * gated by the biometric confirm at the UI layer. allowBackup=false keeps
 * the ciphertext out of cloud backups.
 *
 * Deliberately NOT setUserAuthenticationRequired: address derivation (new
 * wallet, fresh receive address) legitimately runs without a prompt, and a
 * keystore-enforced prompt per decryption would also silently break the
 * moment the user removes their screen lock — losing the seed with it.
 */
class WalletSeedStore(private val context: Context) {

    private val mutex = Mutex()

    /** Also holds wallet/coin registry entries — see [WalletRepository]. */
    internal val store get() = context.walletDataStore

    suspend fun putSeed(walletId: String, mnemonic: String) {
        mutex.withLock {
            store.edit { it[seedKey(walletId)] = encrypt(mnemonic) }
        }
    }

    /** Null only when the keystore was wiped under us or the id is unknown. */
    suspend fun seed(walletId: String): String? = mutex.withLock {
        store.data.first()[seedKey(walletId)]?.let { decrypt(it) }
    }

    suspend fun deleteSeed(walletId: String) {
        mutex.withLock {
            store.edit { it.remove(seedKey(walletId)) }
        }
    }

    private fun seedKey(walletId: String) = stringPreferencesKey("seed_$walletId")

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

    private companion object {
        const val KEYSTORE = "AndroidKeyStore"
        const val KEY_ALIAS = "skywire_wallet_seed"
        const val TRANSFORM = "AES/GCM/NoPadding"
    }
}

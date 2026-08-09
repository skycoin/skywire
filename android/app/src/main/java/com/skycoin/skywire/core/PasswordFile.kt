package com.skycoin.skywire.core

import java.security.MessageDigest
import java.security.SecureRandom

/**
 * The on-disk credential format the gated app surfaces read: a single line of
 * `"<hex salt>:<hex sha256(password || salt)>"`, 16-byte salt — the same
 * hashing the hypervisor's user store uses, so one writer serves every gate.
 *
 * Both skychat (`--password-file`) and skydex-client (`--password-file`) read
 * exactly this; the format lives here rather than in either profile so the
 * security-relevant half is written once and read twice.
 */
object PasswordFile {

    /** The on-disk form of [password], with a fresh salt. */
    fun record(password: String): String {
        val salt = ByteArray(SALT_LEN).also { SecureRandom().nextBytes(it) }
        return salt.toHex() + ":" + hash(password, salt).toHex()
    }

    /**
     * Whether an existing [record] still stands for [password] — re-hashed
     * with the record's own salt. The check is what lets the file be left
     * alone on a normal launch and rewritten when the stored secret has
     * rotated (a wiped keystore), instead of the app 401-ing against its own
     * gate.
     */
    fun matches(record: String, password: String): Boolean {
        val (saltHex, hashHex) = record.trim().split(":", limit = 2)
            .takeIf { it.size == 2 } ?: return false
        val salt = runCatching { saltHex.fromHex() }.getOrNull() ?: return false
        return hash(password, salt).toHex() == hashHex.lowercase()
    }

    private fun hash(password: String, salt: ByteArray): ByteArray =
        MessageDigest.getInstance("SHA-256")
            .digest(password.toByteArray(Charsets.UTF_8) + salt)

    private fun ByteArray.toHex(): String = joinToString("") { "%02x".format(it) }

    private fun String.fromHex(): ByteArray = ByteArray(length / 2) { i ->
        substring(i * 2, i * 2 + 2).toInt(16).toByte()
    }

    private const val SALT_LEN = 16
}

package com.skycoin.wallet

import com.skycoin.wallet.skycoin.SkyFiberWalletCore
import com.skycoin.wallet.skycoin.SkycoinBip44
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.jsonArray
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import okhttp3.OkHttpClient
import kotlin.test.Test
import kotlin.test.assertEquals

/**
 * BIP 44 Skycoin wallets against skycoin's own wallet files
 * (src/wallet/testdata): the same addresses and secrets on both chains, and
 * the same chain xpubs, with and without a seed passphrase.
 */
class SkycoinBip44Test {

    private val core = SkyFiberWalletCore("http://127.0.0.1:1", OkHttpClient())

    private fun wallet(name: String): JsonObject =
        Json.parseToJsonElement(SkycoinVectors.read("src/wallet/testdata/$name")).jsonObject

    private fun check(name: String, accountXpub: String) {
        val w = wallet(name)
        val meta = w["meta"]!!.jsonObject
        val keys = KeySource(
            meta["seed"]!!.jsonPrimitive.content,
            meta["seedPassphrase"]!!.jsonPrimitive.content,
            Derivation.BIP44,
        )
        val chains = w["accounts"]!!.jsonArray[0].jsonObject["chains"]!!.jsonArray.associate {
            it.jsonObject["chain"]!!.jsonPrimitive.content to it.jsonObject
        }
        fun entries(chain: String, field: String) =
            chains[chain]!!["entries"]!!.jsonArray.map { it.jsonObject[field]!!.jsonPrimitive.content }

        val external = entries("external", "address")
        val change = entries("change", "address")
        val book = core.deriveAddresses(keys, external.size, change.size)
        assertEquals(external, book.receive, name)
        assertEquals(change, book.change, name)

        val account = SkycoinBip44.accountKey(keys.mnemonic, keys.passphrase)
        assertEquals(entries("external", "secret"), external.indices.map { SkycoinBip44.key(account, 0, it).key.toHex() }, name)

        val xpubs = core.extendedPublicKeys(keys)!!
        assertEquals(chains["external"]!!["public_key"]!!.jsonPrimitive.content, xpubs.receive, name)
        assertEquals(chains["change"]!!["public_key"]!!.jsonPrimitive.content, xpubs.change, name)
        assertEquals(accountXpub, xpubs.account, name)
    }

    // The account xpubs are skycoin's bip32 public key of each file's account private_key.
    @Test
    fun withoutPassphrase() = check(
        "test6-bip44.wlt",
        "xpub6DRSVk6Cx1SsjYUmQU5y6zsePxrJti9KPCV8rTDNvvZogXa7ffoe5f6P4a8fXrqMazt3zMe8t3qk8qHYYPm55bRToPHDa4rq1zBAPqAbT4f",
    )

    @Test
    fun withPassphrase() = check(
        "test6-passphrase-bip44.wlt",
        "xpub6CZypHAXcJcEKgHvDjJQPCSFCT7QaADpLfnpwGcfbuHEbwcki4WNibbb1MfxacYJ3sAWfp5kAZRZvmim7YpWiwoAmE8tFyMQVZr4YU5nk33",
    )

    @Test
    fun theClassicWalletHasNoXpub() {
        assertEquals(null, core.extendedPublicKeys(KeySource("into raccoon purity buzz shock arch artefact stadium bless witness used odor")))
    }
}

package com.skycoin.skywire

import com.skycoin.skywire.core.SkymailAddress
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test

/** Pairs printed by `skywire cli mail` for two real visors. */
class SkymailAddressTest {

    private val phone = "03710d1ddb29f9efbd183e7a41ef6f68df2792f96803a3094ea2e12e9f878eaf22"
    private val phoneLabel = "anyq2ho3fh467piyhz5ed33pndpspexznab2gckoulqs5h4hr2xse"
    private val pc = "029bb12dd09e8a26821cd5828c7704fd30c3e1c60b314a90ce957f74ee1cc34bd2"
    private val pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"

    @Test
    fun labelMatchesTheVisor() {
        assertEquals(phoneLabel, SkymailAddress.label(phone))
        assertEquals(pcLabel, SkymailAddress.label(pc.uppercase()))
        assertEquals("mail@$pcLabel.dmsg", SkymailAddress.of(pc))
    }

    @Test
    fun labelDecodesBack() {
        assertEquals(phone, SkymailAddress.pkOf(phoneLabel))
        assertEquals(pc, SkymailAddress.pkOfAddress("Bob@${pcLabel.uppercase()}.SKYNET"))
        assertEquals(pc, SkymailAddress.pkOfAddress("bob@host.$pcLabel.skynet"))
    }

    @Test
    fun recipientsAcceptKeysAndAddresses() {
        assertEquals("mail@$pcLabel.dmsg", SkymailAddress.recipient(pc))
        assertEquals("mail@$pcLabel.dmsg", SkymailAddress.recipient("skychat://$pc/"))
        assertEquals("bob@$pcLabel.skynet", SkymailAddress.recipient(" bob@$pcLabel.skynet "))
        assertNull(SkymailAddress.recipient("bob@example.com"))
        assertNull(SkymailAddress.recipient("not a key"))
        assertNull(SkymailAddress.label(pc.dropLast(2)))
    }
}

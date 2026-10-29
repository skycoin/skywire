package com.skycoin.skywire

import com.skycoin.skywire.api.BookContact
import com.skycoin.skywire.api.BookPayment
import com.skycoin.skywire.core.PhoneContacts
import com.skycoin.skywire.core.SkymailAddress
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotEquals
import org.junit.Test

class PhoneContactsTest {

    private val pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private val pc = SkymailAddress.pkOf(pcLabel)!!

    private fun rows(c: BookContact) = PhoneContacts.rows(c, { "chat $it" }, { "mail $it" })

    @Test
    fun skymailIsNeverAnEmailRow() {
        val c = BookContact(
            uid = "a",
            name = "PC",
            keys = listOf(pc),
            mail = listOf("me@$pcLabel.skynet", "pc@example.com"),
            payments = listOf(BookPayment("skycoin", "2jBbGx")),
            notes = "desk",
        )
        assertEquals(
            listOf(
                mapOf("mimetype" to "vnd.android.cursor.item/name", "data1" to "PC"),
                mapOf("mimetype" to PhoneContacts.MIME_CHAT, "data1" to pc, "data2" to "SkyChat", "data3" to "chat $pc"),
                mapOf(
                    "mimetype" to PhoneContacts.MIME_MAIL,
                    "data1" to "me@$pcLabel.skynet",
                    "data2" to "SkyMail",
                    "data3" to "mail me@$pcLabel.skynet",
                ),
                mapOf("mimetype" to "vnd.android.cursor.item/email_v2", "data1" to "pc@example.com", "data2" to 3),
                mapOf("mimetype" to "vnd.android.cursor.item/note", "data1" to "desk"),
            ),
            rows(c),
        )
    }

    @Test
    fun theStampFollowsWhatIsWritten() {
        val c = BookContact(uid = "a", name = "PC", keys = listOf(pc))
        assertEquals(PhoneContacts.stamp(rows(c)), PhoneContacts.stamp(rows(c.copy(groups = listOf("work")))))
        assertNotEquals(PhoneContacts.stamp(rows(c)), PhoneContacts.stamp(rows(c.copy(name = "Desk"))))
    }
}

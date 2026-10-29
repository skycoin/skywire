package com.skycoin.skywire

import com.skycoin.skywire.api.BookContact
import com.skycoin.skywire.api.BookPayment
import com.skycoin.skywire.core.MailContacts
import com.skycoin.skywire.core.SkymailAddress
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class MailContactsTest {

    @get:Rule
    val tmp = TemporaryFolder()

    private val pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private val phoneLabel = "anyq2ho3fh467piyhz5ed33pndpspexznab2gckoulqs5h4hr2xse"
    private val pc = SkymailAddress.pkOf(pcLabel)!!
    private val phone = SkymailAddress.pkOf(phoneLabel)!!

    @Test
    fun oneAddressPerMailboxFromMailAndKeys() {
        val book = listOf(
            BookContact(uid = "a", name = "Alice", keys = listOf(pc, phone), mail = listOf("bob@$pcLabel.skynet")),
            BookContact(uid = "b", name = "Bank", mail = listOf("bank@example.com")),
            BookContact(uid = "c", name = "Coins", payments = listOf(BookPayment("skycoin", "2jBbGx"))),
        )
        val list = MailContacts.of(book)
        assertEquals(listOf("a"), list.map { it.uid })
        assertEquals(listOf("bob@$pcLabel.skynet", "mail@$phoneLabel.dmsg"), list[0].addresses)
    }

    @Test
    fun aNewNameForAKnownMailboxRenamesThatContact() {
        val pay = listOf(BookPayment("skycoin", "2jBbGx"))
        val book = listOf(BookContact(uid = "a", name = "PC", keys = listOf(pc), payments = pay, notes = "desk"))
        val saved = MailContacts.saved(book, " Desktop ", "mail@$pcLabel.dmsg")
        assertEquals(
            BookContact(uid = "a", name = "Desktop", keys = listOf(pc), mail = listOf("mail@$pcLabel.dmsg"), payments = pay, notes = "desk"),
            saved,
        )
        val fresh = MailContacts.saved(book, "Phone", "mail@$phoneLabel.dmsg")
        assertEquals(BookContact(name = "Phone", keys = listOf(phone), mail = listOf("mail@$phoneLabel.dmsg")), fresh)
    }

    @Test
    fun editingCanChangeTheAddress() {
        val book = listOf(BookContact(uid = "a", name = "PC", keys = listOf(pc), mail = listOf("mail@$pcLabel.skynet")))
        val saved = MailContacts.saved(book, "Phone", "mail@$phoneLabel.dmsg", uid = "a", replacing = "mail@$pcLabel.skynet")
        assertEquals(BookContact(uid = "a", name = "Phone", keys = listOf(phone), mail = listOf("mail@$phoneLabel.dmsg")), saved)
    }

    @Test
    fun importSkipsMailboxesAlreadyInTheBook() {
        val file = tmp.root.resolve("skymail-contacts.json")
        file.writeText(
            """[{"name":"PC","address":"mail@$pcLabel.dmsg"},{"name":"Phone","address":"mail@$phoneLabel.dmsg"},""" +
                """{"name":"Phone again","address":"x@$phoneLabel.skynet"},{"name":"Typo","address":"mail@nope.dmsg"}]""",
        )
        val book = listOf(BookContact(uid = "a", name = "Desktop", keys = listOf(pc)))
        assertEquals(
            listOf(BookContact(name = "Phone", keys = listOf(phone), mail = listOf("mail@$phoneLabel.dmsg"))),
            MailContacts.imports(file, book),
        )
        file.writeText("not json")
        assertEquals(emptyList<BookContact>(), MailContacts.imports(file, book))
    }
}

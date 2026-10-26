package com.skycoin.skywire

import com.skycoin.skywire.core.MailContact
import com.skycoin.skywire.core.MailContacts
import org.junit.Assert.assertEquals
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder

class MailContactsTest {

    @get:Rule
    val tmp = TemporaryFolder()

    private val pcLabel = "akn3cloqt2fcnaq42wbiy5ye7uymhyogbmyuvegosv7xj3q4ynf5e"
    private val phoneLabel = "anyq2ho3fh467piyhz5ed33pndpspexznab2gckoulqs5h4hr2xse"

    private fun store() = MailContacts(tmp.root.resolve("local/skymail-contacts.json"))

    @Test
    fun oneEntryPerMailboxWhateverTheNetwork() {
        val book = store()
        book.put(MailContact("PC", "mail@$pcLabel.dmsg"))
        book.put(MailContact("Phone", "mail@$phoneLabel.dmsg"))
        val list = book.put(MailContact("Desktop", "mail@$pcLabel.skynet"))
        assertEquals(listOf("Desktop", "Phone"), list.map { it.name })
        assertEquals(list, store().load())
    }

    @Test
    fun editingCanChangeTheAddress() {
        val book = store()
        book.put(MailContact("PC", "mail@$pcLabel.dmsg"))
        val list = book.put(MailContact("Phone", "mail@$phoneLabel.dmsg"), replacing = "mail@$pcLabel.dmsg")
        assertEquals(listOf(MailContact("Phone", "mail@$phoneLabel.dmsg")), list)
    }

    @Test
    fun removeAndUnreadable() {
        val book = store()
        book.put(MailContact("PC", "mail@$pcLabel.dmsg"))
        assertEquals(emptyList<MailContact>(), book.remove("MAIL@$pcLabel.SKYNET"))
        tmp.root.resolve("local/skymail-contacts.json").writeText("not json")
        assertEquals(emptyList<MailContact>(), store().load())
    }
}

package com.skycoin.skywire.core

import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import java.io.File

/** A name for a SkyMail address. */
@Serializable
data class MailContact(val name: String, val address: String)

/**
 * SkyMail's own address book, a JSON file in the identity's local folder, so
 * replacing the key clears it with the rest. SkyChat's names are separate.
 */
class MailContacts(private val file: File) {

    private val json = Json { ignoreUnknownKeys = true }

    /** The saved contacts by name, or none when the file is missing or unreadable. */
    @Synchronized
    fun load(): List<MailContact> = runCatching {
        json.decodeFromString(ListSerializer(MailContact.serializer()), file.readText())
    }.getOrDefault(emptyList()).sortedBy { it.name.lowercase() }

    /**
     * Saves [contact], replacing the one at [replacing] when editing and any
     * other entry for the same mailbox. Returns the new list.
     */
    @Synchronized
    fun put(contact: MailContact, replacing: String? = null): List<MailContact> {
        val drop = setOfNotNull(key(contact.address), replacing?.let(::key))
        return write(load().filter { key(it.address) !in drop } + contact)
    }

    @Synchronized
    fun remove(address: String): List<MailContact> = write(load().filter { key(it.address) != key(address) })

    private fun write(list: List<MailContact>): List<MailContact> {
        file.parentFile?.mkdirs()
        val tmp = File(file.parentFile, file.name + ".tmp")
        tmp.writeText(json.encodeToString(ListSerializer(MailContact.serializer()), list))
        if (!tmp.renameTo(file)) error("cannot write ${file.name}")
        return list.sortedBy { it.name.lowercase() }
    }

    companion object {
        /** One mailbox whichever network an address was written for. */
        fun key(address: String): String =
            SkymailAddress.withNetwork(address.trim().lowercase(), SkymailAddress.DMSG)
    }
}

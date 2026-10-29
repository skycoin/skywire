package com.skycoin.skywire.api

import kotlinx.serialization.Serializable

/**
 * One entry of the visor's contact book, `/api/visors/{pk}/contacts`
 * (pkg/contacts). SkyChat and the dashboard read the same book.
 */
@Serializable
data class BookContact(
    /** Empty on a new contact: the visor gives it one. */
    val uid: String = "",
    val name: String = "",
    /** Skywire public keys, as hex. */
    val keys: List<String> = emptyList(),
    val mail: List<String> = emptyList(),
    val payments: List<BookPayment> = emptyList(),
    val groups: List<String> = emptyList(),
    val notes: String = "",
)

/** An address the contact can be paid at, e.g. skycoin + "2jBbGx…". */
@Serializable
data class BookPayment(val coin: String, val address: String)

/** The visor refused a contact as malformed, so sending it again will not help. */
class ContactRejectedException(message: String) : java.io.IOException(message)

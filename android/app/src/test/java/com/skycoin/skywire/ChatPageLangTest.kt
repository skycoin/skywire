package com.skycoin.skywire

import com.skycoin.skywire.core.AppLanguage
import com.skycoin.skywire.ui.chat.ChatWebView
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The chat page's language must follow the app's, including when the app is on "System".
 * Read from the resource sources, the same way TranslationCatalogTest reads them.
 */
class ChatPageLangTest {

    private val res = File("src/main/res")

    @Test
    fun explicitChoicesKeepTheirQuery() {
        for (resolved in listOf("", "zh", "es", "fr")) {
            assertEquals("&lang=zh", ChatWebView.langQuery(AppLanguage.CHINESE_SIMPLIFIED, resolved))
            assertEquals("&lang=es", ChatWebView.langQuery(AppLanguage.SPANISH, resolved))
            assertEquals("&lang=fr", ChatWebView.langQuery(AppLanguage.FRENCH, resolved))
            assertEquals("", ChatWebView.langQuery(AppLanguage.ENGLISH, resolved))
        }
    }

    @Test
    fun systemFollowsTheResolvedResource() {
        assertEquals("&lang=zh", ChatWebView.langQuery(AppLanguage.SYSTEM, "zh"))
        assertEquals("&lang=es", ChatWebView.langQuery(AppLanguage.SYSTEM, "es"))
        assertEquals("&lang=fr", ChatWebView.langQuery(AppLanguage.SYSTEM, "fr"))
        assertEquals("", ChatWebView.langQuery(AppLanguage.SYSTEM, ""))
        // Only codes the page ships go into the URL.
        assertEquals("", ChatWebView.langQuery(AppLanguage.SYSTEM, "de"))
        assertEquals("", ChatWebView.langQuery(AppLanguage.SYSTEM, "zh&x=1"))
    }

    @Test
    fun everyCatalogNamesThePageLanguageItShows() {
        assertEquals("", pageLang("values"))
        for (language in AppLanguage.entries) {
            if (language == AppLanguage.SYSTEM || language == AppLanguage.ENGLISH) continue
            val folder = "values-${language.tag.replace("-", "-r")}"
            val resolved = pageLang(folder)
            assertTrue("$folder has no chat_page_lang", resolved.isNotEmpty())
            // System on a phone in this language must load what the explicit choice loads.
            assertEquals(
                folder,
                ChatWebView.langQuery(language, resolved),
                ChatWebView.langQuery(AppLanguage.SYSTEM, resolved),
            )
        }
    }

    private fun pageLang(folder: String): String {
        val file = File(res, "$folder/strings.xml")
        assertTrue("cannot find ${file.absolutePath}", file.isFile)
        val doc = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file)
        val nodes = doc.getElementsByTagName("string")
        for (i in 0 until nodes.length) {
            val el = nodes.item(i) as Element
            if (el.getAttribute("name") == "chat_page_lang") return el.textContent
        }
        throw AssertionError("$folder/strings.xml has no chat_page_lang")
    }
}

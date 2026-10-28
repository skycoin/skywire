package com.skycoin.skywire

import com.skycoin.skywire.core.AppLanguage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * The embedded SkyDEX page translates itself from `chat_page_lang`, so every
 * catalog must name the language it shows. Read from the resource sources,
 * the same way TranslationCatalogTest reads them.
 */
class ChatPageLangTest {

    private val res = File("src/main/res")

    @Test
    fun everyCatalogNamesThePageLanguageItShows() {
        assertEquals("", pageLang("values"))
        for (language in AppLanguage.entries) {
            if (language == AppLanguage.SYSTEM || language == AppLanguage.ENGLISH) continue
            val folder = "values-${language.tag.replace("-", "-r")}"
            assertTrue("$folder has no chat_page_lang", pageLang(folder).isNotEmpty())
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

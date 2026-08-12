package com.skycoin.skywire

import com.skycoin.skywire.core.AppLanguage
import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import org.w3c.dom.Element
import java.io.File
import javax.xml.parsers.DocumentBuilderFactory

/**
 * Every language the app offers has to actually be there, and every string in
 * it has to be usable.
 *
 * Three failures this catches, all of which look like nothing in a diff and
 * are only visible on a phone set to the language nobody on the team reads:
 *
 *  - A string added to the English catalog and not to the others. The screen
 *    silently falls back to English, so it ships looking finished.
 *  - A format placeholder dropped or retyped in translation — `%1$s` becoming
 *    `%1$d`, or vanishing. That is not a cosmetic fault: `getString` throws
 *    `IllegalFormatException` and takes the screen down with it, in that
 *    language only.
 *  - A language offered in [AppLanguage] with no `values-` folder behind it, or
 *    one missing from `locales_config.xml` — the picker shows an entry that
 *    changes nothing, or Android 13+ refuses to list the app in its own
 *    per-app language settings.
 *
 * Asserted against the resource sources rather than a built `R`, for the same
 * reason [ManifestKeyguardTest] reads the manifest: this is a statement about
 * what is committed, and it has to hold before anything is assembled.
 */
class TranslationCatalogTest {

    private val res = File("src/main/res")

    /** `%1$s`, `%2$d`, a bare `%s` — everything `String.format` will act on. */
    private val placeholder = Regex("%(?:(\\d+)\\$)?([a-zA-Z])")

    @Test
    fun everyOfferedLanguageIsShippedAndDeclared() {
        val declared = locales()
        for (language in AppLanguage.entries) {
            if (language == AppLanguage.SYSTEM) continue
            // en is the default catalog — values/, not values-en/.
            val folder = if (language == AppLanguage.ENGLISH) {
                File(res, "values")
            } else {
                File(res, "values-${language.tag.replace("-", "-r")}")
            }
            assertTrue(
                "AppLanguage.$language offers ${language.tag} but ${folder.path} " +
                    "has no strings.xml — the picker would show a language that changes nothing",
                File(folder, "strings.xml").isFile,
            )
            assertTrue(
                "AppLanguage.$language offers ${language.tag}, which is not in " +
                    "res/xml/locales_config.xml (declared: $declared) — Android 13+ reads that " +
                    "file to list the app in Settings ▸ Apps ▸ Skywire ▸ Language",
                declared.contains(language.tag),
            )
        }
        for (tag in declared) {
            assertTrue(
                "locales_config.xml declares $tag, which no AppLanguage entry offers — " +
                    "the system would let the user pick a language the app cannot show in Settings",
                AppLanguage.entries.any { it.tag == tag },
            )
        }
    }

    @Test
    fun everyTranslatableStringIsTranslated() {
        val english = catalog(File(res, "values/strings.xml"))
        for ((tag, translated) in translations()) {
            val missing = english.keys - translated.keys
            assertTrue(
                "$tag is missing ${missing.size} string(s) that values/strings.xml has, so those " +
                    "screens fall back to English in a language that looks finished: " +
                    missing.sorted().joinToString(", ").take(600),
                missing.isEmpty(),
            )
            val unknown = translated.keys - english.keys
            assertTrue(
                "$tag defines string(s) the English catalog does not — a rename left them " +
                    "behind, and nothing reads them: " + unknown.sorted().joinToString(", "),
                unknown.isEmpty(),
            )
        }
    }

    /**
     * A `<string>` must carry exactly the English placeholders. A `<plurals>`
     * is checked against what its categories offer between them, not against
     * any one of them: an English `one` item is free to drop the count that
     * `other` interpolates ("its wallet" reads better than "its 1 wallet"),
     * and a language with a single category still needs the number. What must
     * never happen either way is a translation asking for an argument the call
     * site does not pass.
     */
    @Test
    fun placeholdersSurviveTranslation() {
        val english = catalog(File(res, "values/strings.xml"))
        for ((tag, translated) in translations()) {
            for ((name, values) in translated) {
                val source = english.getValue(name)
                val offered = source.flatMap { placeholdersOf(it) }.toSet()
                for (value in values) {
                    val used = placeholdersOf(value)
                    if (source.size == 1) {
                        assertEquals(
                            "$tag:$name has different format placeholders from the English " +
                                "string. getString() throws IllegalFormatException at runtime " +
                                "for this, in this language only.\n  en: ${source.first()}\n  " +
                                "$tag: $value",
                            placeholdersOf(source.first()),
                            used,
                        )
                    } else {
                        assertTrue(
                            "$tag:$name uses $used, but the English plural only ever passes " +
                                "$offered — the extra argument does not exist at the call site " +
                                "and formatting throws.\n  $tag: $value",
                            offered.containsAll(used),
                        )
                    }
                }
            }
        }
    }

    /**
     * Simplified Chinese has one plural category: `other`. An `one` item there
     * is never selected — it is dead text that reads as a covered case.
     */
    @Test
    fun chinesePluralsCarryOnlyTheCategoryItUses() {
        val file = File(res, "values-zh-rCN/strings.xml")
        for (plurals in elements(file, "plurals")) {
            val quantities = plurals.getElementsByTagName("item").let { items ->
                (0 until items.length).map { (items.item(it) as Element).getAttribute("quantity") }
            }
            assertEquals(
                "${plurals.getAttribute("name")} in values-zh-rCN carries $quantities. " +
                    "Chinese selects `other` for every count; anything else is never shown.",
                listOf("other"),
                quantities,
            )
        }
    }

    // --- reading the resources ---

    /** Every shipped translation but the default catalog: tag -> its strings. */
    private fun translations(): Map<String, Map<String, List<String>>> =
        AppLanguage.entries
            .filter { it != AppLanguage.SYSTEM && it != AppLanguage.ENGLISH }
            .associate { language ->
                val folder = "values-${language.tag.replace("-", "-r")}"
                language.tag to catalog(File(res, "$folder/strings.xml"))
            }

    /** name -> every value under it (one for a string, one per item for plurals). */
    private fun catalog(file: File): Map<String, List<String>> {
        assertTrue("cannot find ${file.absolutePath}", file.isFile)
        val out = linkedMapOf<String, List<String>>()
        for (el in elements(file, "string")) {
            if (el.getAttribute("translatable") == "false") continue
            out[el.getAttribute("name")] = listOf(el.textContent)
        }
        for (el in elements(file, "plurals")) {
            val items = el.getElementsByTagName("item")
            out[el.getAttribute("name")] =
                (0 until items.length).map { items.item(it).textContent }
        }
        return out
    }

    private fun elements(file: File, tag: String): List<Element> {
        val document = DocumentBuilderFactory.newInstance().newDocumentBuilder().parse(file)
        val nodes = document.getElementsByTagName(tag)
        return (0 until nodes.length)
            .map { nodes.item(it) as Element }
            // Top level only: an <item> inside <plurals> is not a string of its own.
            .filter { it.parentNode === document.documentElement }
    }

    private fun placeholdersOf(value: String): List<String> =
        placeholder.findAll(value)
            .map { it.value }
            .filter { it != "%%" }
            .sorted()
            .toList()

    private fun locales(): List<String> {
        val file = File(res, "xml/locales_config.xml")
        assertTrue("cannot find ${file.absolutePath}", file.isFile)
        return elements(file, "locale").map {
            it.getAttribute("android:name").ifEmpty { it.getAttribute("name") }
        }
    }
}

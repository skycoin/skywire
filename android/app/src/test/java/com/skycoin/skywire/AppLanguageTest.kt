package com.skycoin.skywire

import com.skycoin.skywire.core.AppLanguage
import org.junit.Assert.assertEquals
import org.junit.Test

/**
 * What a stored value and a platform tag list mean.
 *
 * Both directions matter and they are not the same one. Below API 33 the
 * choice comes back from our own prefs as an enum constant's name; on 33+ it
 * comes back from `LocaleManager` as BCP-47, and the platform is free to
 * canonicalise what it was given — ask for `zh-CN` and a later read can return
 * `zh-Hans-CN`. A picker that fails to recognise its own stored choice shows
 * "System" while the app is plainly in Chinese, and the user cannot get back.
 */
class AppLanguageTest {

    @Test
    fun storedNamesRoundTrip() {
        for (language in AppLanguage.entries) {
            assertEquals(language, AppLanguage.of(language.name))
        }
    }

    @Test
    fun unknownStoredValueFallsBackToSystem() {
        // An older build's value, or a language that has since been dropped.
        assertEquals(AppLanguage.SYSTEM, AppLanguage.of("KLINGON"))
        assertEquals(AppLanguage.SYSTEM, AppLanguage.of(null))
        assertEquals(AppLanguage.SYSTEM, AppLanguage.of(""))
    }

    @Test
    fun platformTagsResolveToTheShippedTranslation() {
        // Every shape the platform is entitled to hand back for what we set.
        for (tags in listOf("zh-CN", "zh-Hans-CN", "zh", "zh-CN,en")) {
            assertEquals("tags=$tags", AppLanguage.CHINESE_SIMPLIFIED, AppLanguage.ofTags(tags))
        }
        for (tags in listOf("en", "en-US", "en-GB,fr")) {
            assertEquals("tags=$tags", AppLanguage.ENGLISH, AppLanguage.ofTags(tags))
        }
    }

    @Test
    fun noTagAndUnshippedLanguagesReadAsSystem() {
        // An empty list is how LocaleManager says "follow the system".
        assertEquals(AppLanguage.SYSTEM, AppLanguage.ofTags(""))
        assertEquals(AppLanguage.SYSTEM, AppLanguage.ofTags(null))
        // A language the app does not ship is the same situation as no choice:
        // the system resolves the resources, and so should the picker.
        assertEquals(AppLanguage.SYSTEM, AppLanguage.ofTags("fa-IR"))
    }
}

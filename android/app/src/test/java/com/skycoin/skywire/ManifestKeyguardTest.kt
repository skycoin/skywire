package com.skycoin.skywire

import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/**
 * The app must not be granted the lock screen in its manifest.
 *
 * A call has to be answerable without unlocking, and the obvious way to get
 * that is `android:showWhenLocked` on the Activity. This app is one Activity,
 * so that one line hands the privilege to every screen behind the call: lock
 * the phone with the app open and the whole app stayed on top of the keyguard,
 * readable and operable by whoever picked it up — chats, the address book, the
 * wallet. Reported as "app still usable, but phone is lock!".
 *
 * MainActivity.showOverKeyguard now turns it on for the length of a call and
 * off again after, so the privilege exists only while something is ringing.
 * This asserts the static grant has not come back. It is a one-line change
 * that looks helpful, a call screen is exactly the context in which someone
 * would add it, and nothing else in a build would say a word.
 *
 * Asserted against the manifest source rather than ActivityInfo at runtime:
 * the platform constants for these two flags were removed in recent SDKs, so
 * a runtime check would have to hard-code bits that could quietly stop being
 * populated — a test that passes because it no longer measures anything.
 */
class ManifestKeyguardTest {

    @Test
    fun manifestDoesNotDeclareLockScreenAccess() {
        val manifest = File("src/main/AndroidManifest.xml")
        assertTrue(
            "cannot find ${manifest.absolutePath} — unit tests are expected to run " +
                "with the module directory as their working directory",
            manifest.isFile,
        )
        // Attribute form only: the manifest carries a comment naming both
        // flags to explain why they are absent, and that comment is the point.
        val text = manifest.readText()
        for (attribute in listOf("android:showWhenLocked", "android:turnScreenOn")) {
            assertTrue(
                "$attribute is declared in the manifest — that grants it to every screen " +
                    "in the app, not just a ringing call, and puts the whole app on top " +
                    "of the keyguard. Set it from MainActivity for the length of the call.",
                !text.contains("$attribute="),
            )
        }
    }
}

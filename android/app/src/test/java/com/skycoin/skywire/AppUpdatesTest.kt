package com.skycoin.skywire

import com.skycoin.skywire.core.AppUpdates
import com.skycoin.skywire.core.AppUpdates.Version
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNotNull
import org.junit.Assert.assertNull
import org.junit.Test

/**
 * Which release the updater offers, and whether it offers one at all.
 *
 * The whole feature is one decision made against a list from GitHub, and
 * every way it can go wrong is silent: it offers nothing forever, or it
 * offers a desktop release with no APK, or it offers an older build because
 * "0.0.9" sorts above "0.0.10" as text. None of those show up in a build and
 * all of them look like "updates just don't work" on a phone.
 *
 * The one that is not obvious, and that this was written after getting
 * wrong: the repo publishes an `android-arm64.apk` on some of the project's
 * own `vX.Y.Z` releases too. Those are the visor's releases, numbered 1.3.x
 * against the app's 0.0.x, and taking one would move the phone onto a version
 * line this app does not use — after which no real mobile release would ever
 * outrank it again. The fixture holds exactly that trap.
 */
class AppUpdatesTest {

    @Test
    fun versionParsesBothTagShapesAndRejectsTheRest() {
        assertEquals(Version(0, 0, 2), AppUpdates.versionOf("mobile-v0.0.2"))
        assertEquals(Version(1, 3, 29), AppUpdates.versionOf("v1.3.29"))
        assertEquals(Version(1, 3, 29), AppUpdates.versionOf("1.3.29"))
        // A suffix orders with its base — the tag is still that release.
        assertEquals(Version(1, 3, 29), AppUpdates.versionOf("v1.3.29-rc1"))
        // Not releases of this app.
        assertNull(AppUpdates.versionOf("vpn-lite"))
        assertNull(AppUpdates.versionOf("v1.3"))
        assertNull(AppUpdates.versionOf(""))
    }

    @Test
    fun versionsCompareAsNumbersNotAsText() {
        // The one that a string comparison gets backwards.
        assertEquals(true, Version(0, 0, 10) > Version(0, 0, 9))
        assertEquals(true, Version(0, 1, 0) > Version(0, 0, 99))
        assertEquals(true, Version(1, 0, 0) > Version(0, 99, 99))
    }

    @Test
    fun picksTheNewestMobileRelease() {
        val found = AppUpdates.selectUpdate(FIXTURE, Version(0, 0, 1))
        assertNotNull("a newer mobile release is in the fixture", found)
        assertEquals(Version(0, 0, 2), found!!.version)
        assertEquals("skywire-0.0.2-arm64-v8a.apk", found.apkName)
        assertEquals(
            "https://example.test/skywire-0.0.2-arm64-v8a.apk.sha256",
            found.sha256Url,
        )
    }

    @Test
    fun theVisorsOwnReleasesAreNotAppUpdates() {
        // v1.3.93 is in the fixture, is newer than every mobile release by
        // number, and really does carry an android-arm64.apk. It is the
        // visor's release, not this app's, and taking it would strand the
        // phone on 1.3.x where no 0.0.x could ever reach it again.
        val found = AppUpdates.selectUpdate(FIXTURE, Version(0, 0, 1))
        assertEquals(Version(0, 0, 2), found!!.version)
        // And with nothing but the visor's releases on offer, there is no
        // update at all rather than the wrong one.
        assertNull(AppUpdates.selectUpdate(FIXTURE_VISOR_ONLY, Version(0, 0, 1)))
    }

    @Test
    fun theAgreedReleaseConventionIsWhatIsMatched() {
        // Stated by the maintainer who cuts them: the title is
        // "Skywire Mobile X.Y.Z" and the tag is "mobile-vX.Y.Z". Both are
        // matched, and either alone is enough, so a release is still found if
        // one of the two is ever typed differently. Case is not load-bearing.
        val found = AppUpdates.selectUpdate(FIXTURE_CONVENTION, Version(1, 2, 2))
        assertNotNull(found)
        assertEquals(Version(1, 2, 3), found!!.version)
        assertEquals("mobile-v1.2.3", found.tag)
        assertEquals("Skywire Mobile 1.2.3", found.title)
    }

    @Test
    fun aMobileReleaseIsFoundByItsTitleToo() {
        // Same release, tagged without the prefix but still titled for what
        // it is — one publishing habit changing should not silently end
        // updates for everyone.
        val found = AppUpdates.selectUpdate(FIXTURE_TITLED_ONLY, Version(0, 0, 1))
        assertNotNull(found)
        assertEquals(Version(0, 0, 5), found!!.version)
    }

    @Test
    fun nothingNewerMeansNoUpdate() {
        assertNull(AppUpdates.selectUpdate(FIXTURE, Version(0, 0, 2)))
        assertNull(AppUpdates.selectUpdate(FIXTURE, Version(9, 9, 9)))
    }

    @Test
    fun draftsAndPreReleasesAreNotOffered() {
        // mobile 0.0.4 is a prerelease and 0.0.3 is a draft, both newer than
        // the 0.0.2 that is the right answer.
        val found = AppUpdates.selectUpdate(FIXTURE, Version(0, 0, 1))
        assertEquals(Version(0, 0, 2), found!!.version)
    }

    @Test
    fun aReleaseWithoutAnApkIsNotAnUpdate() {
        assertNull(AppUpdates.selectUpdate(FIXTURE_NO_APK, Version(0, 0, 1)))
    }

    @Test
    fun anApkForAnotherAbiIsNotAnUpdate() {
        // The Go payload is arm64-only. An x86 asset is not installable here
        // and offering it would download 56 MB to fail at the installer.
        assertNull(AppUpdates.selectUpdate(FIXTURE_WRONG_ABI, Version(0, 0, 1)))
    }

    private companion object {
        fun release(
            tag: String,
            assets: String,
            draft: Boolean = false,
            prerelease: Boolean = false,
            name: String = "Skywire $tag",
        ) = """
            {
              "tag_name": "$tag",
              "name": "$name",
              "body": "notes for $tag",
              "draft": $draft,
              "prerelease": $prerelease,
              "html_url": "https://example.test/$tag",
              "assets": [$assets]
            }
        """.trimIndent()

        fun asset(name: String, size: Long = 1L) = """
            {
              "name": "$name",
              "size": $size,
              "browser_download_url": "https://example.test/$name"
            }
        """.trimIndent()

        /** The live shape: the visor's releases and the app's, interleaved. */
        val FIXTURE = """
            [
              ${release("v1.3.94", asset("skywire-v1.3.94-linux-amd64.tar.xz"))},
              ${release("v1.3.93", asset("skywire-v1.3.93-android-arm64.apk", 58_000_000))},
              ${release("v1.3.92", asset("skywire-v1.3.92-android-arm64.apk", 57_000_000))},
              ${
            release(
                "mobile-v0.0.4",
                asset("skywire-0.0.4-arm64-v8a.apk"),
                prerelease = true,
                name = "Skywire Mobile 0.0.4",
            )
        },
              ${
            release(
                "mobile-v0.0.3",
                asset("skywire-0.0.3-arm64-v8a.apk"),
                draft = true,
                name = "Skywire Mobile 0.0.3",
            )
        },
              ${
            release(
                "mobile-v0.0.2",
                asset("skywire-0.0.2-arm64-v8a.apk", 55_957_359) + "," +
                    asset("skywire-0.0.2-arm64-v8a.apk.sha256", 94),
                name = "Skywire Mobile 0.0.2",
            )
        },
              ${
            release(
                "mobile-v0.0.1",
                asset("skywire-0.0.1-arm64-v8a.apk", 41_056_187),
                name = "Skywire Mobile 0.0.1",
            )
        }
            ]
        """.trimIndent()

        /** Only the visor's releases — including ones that do carry an APK. */
        val FIXTURE_VISOR_ONLY = """
            [
              ${release("v1.3.93", asset("skywire-v1.3.93-android-arm64.apk", 58_000_000))},
              ${release("v1.3.91", asset("skywire-v1.3.91-linux-amd64.tar.xz"))}
            ]
        """.trimIndent()

        /** Exactly the convention releases are cut under. */
        val FIXTURE_CONVENTION = """
            [
              ${
            release(
                "mobile-v1.2.3",
                asset("skywire-1.2.3-arm64-v8a.apk", 55_000_000) + "," +
                    asset("skywire-1.2.3-arm64-v8a.apk.sha256", 94),
                name = "Skywire Mobile 1.2.3",
            )
        },
              ${
            release(
                "MOBILE-V1.2.2",
                asset("skywire-1.2.2-arm64-v8a.apk"),
                name = "SKYWIRE MOBILE 1.2.2",
            )
        }
            ]
        """.trimIndent()

        /** A mobile release whose tag lost the prefix but kept the title. */
        val FIXTURE_TITLED_ONLY = """
            [
              ${
            release(
                "v0.0.5",
                asset("skywire-0.0.5-arm64-v8a.apk"),
                name = "Skywire Mobile 0.0.5",
            )
        }
            ]
        """.trimIndent()

        val FIXTURE_NO_APK = """
            [
              ${release("mobile-v9.9.9", asset("notes.txt"), name = "Skywire Mobile 9.9.9")}
            ]
        """.trimIndent()

        val FIXTURE_WRONG_ABI = """
            [
              ${
            release(
                "mobile-v9.9.9",
                asset("skywire-9.9.9-x86_64.apk"),
                name = "Skywire Mobile 9.9.9",
            )
        }
            ]
        """.trimIndent()
    }
}

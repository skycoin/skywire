package com.skycoin.wallet

import java.io.File

/** Reads a golden file from the skycoin module that go.mod pins. */
internal object SkycoinVectors {
    fun read(path: String): String =
        File(System.getProperty("skycoin.dir") ?: error("skycoin.dir is set by the Gradle test task"), path).readText()
}

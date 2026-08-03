// Root build file — plugin versions come from gradle/libs.versions.toml.
plugins {
    alias(libs.plugins.android.application) apply false
    // Kotlin support is built into AGP ≥9 — no org.jetbrains.kotlin.android here.
    alias(libs.plugins.kotlin.compose) apply false
    alias(libs.plugins.kotlin.serialization) apply false
}

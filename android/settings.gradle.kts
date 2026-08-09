// Skywire Android app — the monorepo Android module.
// Android Studio opens THIS directory; the Go payload lands in
// app/src/main/jniLibs/arm64-v8a/ via `make android-mobile` at the repo root.
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}

dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "skywire-android"
include(":app")
// Wallet crypto and node clients live in :wallet-core, a plain Kotlin/JVM module —
// no Android types, so its transaction and key code runs under JVM unit tests.
include(":wallet-core")

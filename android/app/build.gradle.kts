import java.util.Properties

plugins {
    alias(libs.plugins.android.application)
    // No org.jetbrains.kotlin.android: Kotlin support is built into AGP ≥9.
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// The version of record is android/version.properties, which is what a local
// `./gradlew assembleDebug` and the F-Droid build use. The release workflow
// passes the `mobile-vX.Y.Z` tag's version as properties through
// `make android-apk`, after checking the tag against that file — see
// .github/workflows/android-release.yml.
val versionFile = Properties().apply {
    rootProject.file("version.properties").inputStream().use { load(it) }
}
val appVersionName = (project.findProperty("skywireVersionName") as String?)
    ?: versionFile.getProperty("versionName")
val appVersionCode = ((project.findProperty("skywireVersionCode") as String?)
    ?: versionFile.getProperty("versionCode")).toInt()

// Release signing is supplied by the environment, never committed. Absent
// (every local build), `release` stays unsigned exactly as before; the
// release workflow refuses to publish in that state rather than shipping an
// APK nobody can install.
val keystoreFile = System.getenv("ANDROID_KEYSTORE_FILE")

// F-Droid builds the app itself, signs it with its own key and delivers the
// updates, so the F-Droid build (`-PskywireFdroid=true`, set by its recipe in
// android/fdroid/) carries no updater: AppUpdates is switched off through
// BuildConfig.SELF_UPDATE and REQUEST_INSTALL_PACKAGES is stripped by the
// overlay manifest in src/fdroid/. A property rather than a product flavor so
// every task name and output path the Makefile and workflows use stays as is.
val fdroid = project.findProperty("skywireFdroid") == "true"

android {
    namespace = "com.skycoin.skywire"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.skycoin.skywire"
        // minSdk 26 / target latest, arm64-only first release.
        minSdk = 26
        targetSdk = 36
        versionCode = appVersionCode
        versionName = appVersionName
        testInstrumentationRunner = "androidx.test.runner.AndroidJUnitRunner"
        buildConfigField("boolean", "SELF_UPDATE", (!fdroid).toString())
        ndk {
            // The Go payload (libskywire-mobile.so) is arm64-only.
            abiFilters += "arm64-v8a"
        }
    }

    signingConfigs {
        if (keystoreFile != null) {
            create("release") {
                storeFile = file(keystoreFile)
                storePassword = System.getenv("ANDROID_KEYSTORE_PASSWORD")
                keyAlias = System.getenv("ANDROID_KEY_ALIAS")
                keyPassword = System.getenv("ANDROID_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            // R8 is half of the download: unshrunk, every Compose, icons-extended,
            // OkHttp and BouncyCastle class ships — 24 MB of dex against 2.4 MB
            // shrunk. Class names stay readable (proguard-rules.pro says how);
            // the win is the unused code removed, not the renaming. R8 breaks at
            // RUNTIME when something is only reached by reflection, which is why
            // android-app.yml assembles this build type on every PR and why keep
            // rules for anything that turns up go in proguard-rules.pro.
            isMinifyEnabled = true
            isShrinkResources = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            // findByName, not getByName: null is the valid "unsigned" state.
            signingConfig = signingConfigs.findByName("release")
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }

    buildFeatures {
        compose = true
        buildConfig = true
    }

    if (fdroid) {
        // A build-type manifest merges over src/main's, so it can remove what
        // main declares. Both build types, so a debug build is the same app.
        for (buildType in listOf("debug", "release")) {
            sourceSets.getByName(buildType).manifest.srcFile("src/fdroid/AndroidManifest.xml")
        }
    }

    // The in-app language picker can only offer what is installed. Play's App
    // Bundle language splits deliver the device's language and nothing else,
    // so a phone set to English would install without the Chinese resources
    // and picking 简体中文 would silently give English back. The release
    // workflow now builds the App Bundle (`make android-aab`) alongside the
    // APK, so this split-config is what keeps every installed language present.
    bundle {
        language {
            enableSplit = false
        }
    }

    packaging {
        jniLibs {
            // The core service EXECS libskywire-mobile.so from applicationInfo.nativeLibraryDir
            // (the visor is a child process, not a linked library). That requires the
            // .so extracted to disk at install time — the modern "serve straight from
            // the APK" packaging leaves nativeLibraryDir empty and exec would fail.
            // This is why the installed-size estimate exceeds the APK download size.
            useLegacyPackaging = true
        }
        // BouncyCastle's post-quantum data tables (1.2 MB compressed). R8 drops
        // the pqc classes as unreached, but Java resources are not code and
        // ship regardless. The wallet uses only the lightweight secp256k1,
        // RFC 6979 and digest API (org.bouncycastle.crypto.* / math.ec.*) —
        // nothing under org.bouncycastle.pqc — so nothing can ask for them.
        resources {
            excludes += "org/bouncycastle/pqc/**"
        }
    }
}

// ManifestKeyguardTest asserts against the manifest SOURCE, which Gradle has
// no reason to consider an input to a unit test — so without this the guard
// stays UP-TO-DATE through the exact edit it exists to catch, and reports
// success for a test it never ran. Verified by re-adding the attribute and
// watching the task run and fail.
// TranslationCatalogTest reads the string catalogues and the locale config the
// same way and for the same reason, so they are inputs too. Without this a
// translation edit — the one thing that guard exists to check — leaves the task
// UP-TO-DATE and the build green. Verified the same way: break a placeholder in
// values-zh-rCN and watch the task run and fail.
tasks.withType<Test>().configureEach {
    inputs.file("src/main/AndroidManifest.xml")
        .withPathSensitivity(PathSensitivity.RELATIVE)
        .withPropertyName("appManifest")
    inputs.files(
        fileTree("src/main/res") {
            include("values*/strings.xml", "xml/locales_config.xml")
        },
    )
        .withPathSensitivity(PathSensitivity.RELATIVE)
        .withPropertyName("stringCatalogues")
}

dependencies {
    implementation(platform(libs.compose.bom))
    implementation(libs.compose.ui)
    implementation(libs.compose.material3)
    implementation(libs.compose.ui.tooling.preview)
    // Placeholder app icons until the designed logos land.
    implementation(libs.compose.material.icons.extended)
    implementation(libs.androidx.core.ktx)
    implementation(libs.androidx.splashscreen)
    implementation(libs.androidx.activity.compose)
    implementation(libs.androidx.navigation.compose)
    implementation(libs.androidx.lifecycle.viewmodel.compose)
    implementation(libs.androidx.datastore.preferences)
    // App lock + every secret-revealing confirmation. Brings androidx.fragment
    // with it, which is why MainActivity is a FragmentActivity: BiometricPrompt
    // hosts itself in a fragment and takes nothing less.
    implementation(libs.androidx.biometric)
    // Local-API client — declared now so the module dep set is final.
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    // The core service runs its own supervisor scope outside any lifecycle.
    implementation(libs.kotlinx.coroutines.android)
    // Wallet crypto + node clients. Pure JVM — the seed never crosses a JNI
    // boundary and the same bytes run under unit tests on the host.
    implementation(project(":wallet-core"))
    // QR: journeyapps hosts the scan activity, zxing core renders the
    // receive-address code into a Bitmap.
    implementation(libs.zxing.embedded)
    implementation(libs.zxing.core)
    debugImplementation(libs.compose.ui.tooling)
    // Host-side. For the few facts that are about the module's own sources
    // rather than about a running app — the manifest not handing the lock
    // screen to every screen, for one.
    testImplementation(libs.junit)
    // The answer-request lifecycle is a suspend function with a timeout in
    // it; asserting that the timeout actually fires needs a virtual clock,
    // not a real fifteen-second wait.
    testImplementation(libs.kotlinx.coroutines.test)
    // Instrumented. The audio engine's contract — that stopping a call
    // hands the microphone back — is a statement about AudioRecord and a real
    // socket, and neither has a meaningful stand-in on the JVM.
    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.rules)
    androidTestImplementation(libs.androidx.test.ext.junit)
}

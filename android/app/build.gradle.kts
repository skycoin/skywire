plugins {
    alias(libs.plugins.android.application)
    // No org.jetbrains.kotlin.android: Kotlin support is built into AGP ≥9.
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

// Version comes from the release tag when there is one, and from the
// fallbacks below otherwise, so a local `./gradlew assembleDebug` needs no
// arguments. The release workflow derives both from `mobile-vX.Y.Z` — see
// .github/workflows/android-release.yml.
val appVersionName = (project.findProperty("skywireVersionName") as String?) ?: "0.1.0"
val appVersionCode = (project.findProperty("skywireVersionCode") as String?)?.toInt() ?: 1

// Release signing is supplied by the environment, never committed. Absent
// (every local build), `release` stays unsigned exactly as before; the
// release workflow refuses to publish in that state rather than shipping an
// APK nobody can install.
val keystoreFile = System.getenv("ANDROID_KEYSTORE_FILE")

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
            // Minification/shrinking is deferred to release hardening.
            isMinifyEnabled = false
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
    }
}

// ManifestKeyguardTest asserts against the manifest SOURCE, which Gradle has
// no reason to consider an input to a unit test — so without this the guard
// stays UP-TO-DATE through the exact edit it exists to catch, and reports
// success for a test it never ran. Verified by re-adding the attribute and
// watching the task run and fail.
tasks.withType<Test>().configureEach {
    inputs.file("src/main/AndroidManifest.xml")
        .withPathSensitivity(PathSensitivity.RELATIVE)
        .withPropertyName("appManifest")
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
    // Instrumented. The audio engine's contract — that stopping a call
    // hands the microphone back — is a statement about AudioRecord and a real
    // socket, and neither has a meaningful stand-in on the JVM.
    androidTestImplementation(libs.junit)
    androidTestImplementation(libs.androidx.test.runner)
    androidTestImplementation(libs.androidx.test.rules)
    androidTestImplementation(libs.androidx.test.ext.junit)
}

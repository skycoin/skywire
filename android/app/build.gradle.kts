plugins {
    alias(libs.plugins.android.application)
    // No org.jetbrains.kotlin.android: Kotlin support is built into AGP ≥9.
    alias(libs.plugins.kotlin.compose)
    alias(libs.plugins.kotlin.serialization)
}

android {
    namespace = "com.skycoin.skywire"
    compileSdk = 37

    defaultConfig {
        applicationId = "com.skycoin.skywire"
        // minSdk 26 / target latest, arm64-only first release.
        minSdk = 26
        targetSdk = 36
        versionCode = 1
        versionName = "0.1.0"
        ndk {
            // The Go payload (libskywire-mobile.so) is arm64-only.
            abiFilters += "arm64-v8a"
        }
    }

    buildTypes {
        release {
            // Minification/shrinking is deferred to release hardening.
            isMinifyEnabled = false
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
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
    // Local-API client — declared now so the module dep set is final.
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    // The core service runs its own supervisor scope outside any lifecycle.
    implementation(libs.kotlinx.coroutines.android)
    debugImplementation(libs.compose.ui.tooling)
}

// Wallet core: Skycoin/fiber and Bitcoin key handling, transaction
// construction and node clients. Deliberately a plain Kotlin/JVM module —
// no Android dependency — so every byte of the money-handling code is
// exercised by host-side unit tests against reference vectors.
plugins {
    // Versionless: AGP ≥9 already carries the Kotlin Gradle plugin on the
    // build classpath, and re-requesting it with a version is rejected.
    id("org.jetbrains.kotlin.jvm")
    alias(libs.plugins.kotlin.serialization)
}

// Match :app's Java 17 target without demanding a separate JDK 17 toolchain.
kotlin {
    compilerOptions {
        jvmTarget.set(org.jetbrains.kotlin.gradle.dsl.JvmTarget.JVM_17)
    }
}

java {
    sourceCompatibility = JavaVersion.VERSION_17
    targetCompatibility = JavaVersion.VERSION_17
}

dependencies {
    // Lightweight API only (org.bouncycastle.crypto.* / math.ec.*): secp256k1
    // curve math, RFC 6979 nonces, RIPEMD-160. The JCA "BC" provider is never
    // registered — Android ships its own crippled copy under that name.
    implementation(libs.bouncycastle)
    implementation(libs.okhttp)
    implementation(libs.kotlinx.serialization.json)
    implementation(libs.kotlinx.coroutines.core)

    testImplementation(libs.junit)
    testImplementation(libs.kotlin.test.junit)
}

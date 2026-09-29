// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

// Release signing is wired entirely through environment variables so CI can
// inject them without a committed keystore (see .github/workflows/release.yml).
val releaseKeystore: String? = System.getenv("CORTEX_RELEASE_KEYSTORE")

android {
    namespace = "solutions.tpt.cortex"
    compileSdk = 35

    defaultConfig {
        applicationId = "solutions.tpt.cortex"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "0.1.0"
    }

    if (releaseKeystore != null) {
        signingConfigs {
            create("release") {
                storeFile = file(releaseKeystore)
                storePassword = System.getenv("CORTEX_RELEASE_STORE_PASSWORD")
                keyAlias = System.getenv("CORTEX_RELEASE_KEY_ALIAS")
                keyPassword = System.getenv("CORTEX_RELEASE_KEY_PASSWORD")
            }
        }
    }

    buildTypes {
        release {
            isMinifyEnabled = true
            proguardFiles(getDefaultProguardFile("proguard-android-optimize.txt"), "proguard-rules.pro")
            if (releaseKeystore != null) {
                signingConfig = signingConfigs.getByName("release")
            }
        }
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
}

dependencies {
    // The gomobile-bound Go daemon (cortex.aar), placed in libs/ by
    // release.yml when the embedded build is wanted; empty otherwise.
    implementation(fileTree(mapOf("dir" to "libs", "include" to listOf("*.aar"))))

    // WebSocket client for the JSON-RPC bridge to the in-process daemon
    // (docs/jsonrpc-contract.md).
    implementation("com.squareup.okhttp3:okhttp:4.12.0")
    implementation("androidx.core:core-ktx:1.15.0")
    implementation("androidx.appcompat:appcompat:1.7.0")
    implementation("androidx.webkit:webkit:1.12.1")

    testImplementation("junit:junit:4.13.2")
    // Real org.json implementation for JVM unit tests (the Android stub
    // throws "not mocked").
    testImplementation("org.json:json:20240303")
}

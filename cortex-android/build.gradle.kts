// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
plugins {
    // AGP 9 ships built-in Kotlin support, so org.jetbrains.kotlin.android
    // is no longer applied; applying it alongside AGP 9 fails with
    // "Cannot add extension with name 'kotlin'".
    id("com.android.application") version "9.4.1" apply false
}

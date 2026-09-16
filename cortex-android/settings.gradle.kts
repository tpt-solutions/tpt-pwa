// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositories {
        google()
        mavenCentral()
    }
}

rootProject.name = "cortex-android"
include(":app")

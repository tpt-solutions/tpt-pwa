# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
# The Go daemon is embedded via gomobile in a later milestone; keep its
# JNI symbols out of shrinking.
-keep class cortex.** { *; }

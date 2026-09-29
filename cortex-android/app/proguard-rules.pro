# Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
# The Go daemon is embedded via gomobile in a later milestone; keep its
# JNI symbols out of shrinking. DaemonService loads the bound class
# reflectively (Class.forName), so R8 can't see the usage.
-keep class solutions.tpt.cortex.Mobile { *; }
# gomobile's JNI runtime lives in the go.* packages inside cortex.aar.
-keep class go.** { *; }

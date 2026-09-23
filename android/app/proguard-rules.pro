# Project-specific ProGuard/R8 rules, on top of proguard-android-optimize.txt
# (which already keeps @JavascriptInterface methods, enum values()/valueOf and
# Parcelable CREATORs). kotlinx.serialization, OkHttp and zxing bring their own
# rules in their jars. Add keep rules here as reflection-only paths surface.

# Shrink, don't rename. The app's own exception class names reach the user
# (`e::class.java.simpleName` is the error text of last resort in the settings,
# wallet and diagnostics screens) and logcat traces are how a device problem
# gets diagnosed. Renaming would save ~50 KB of the ~22 MB R8 removes.
-dontobfuscate

# -dontobfuscate alone is not enough: R8 still MERGES classes, which renames
# them just as well. Unpinned, the app's 1198 classes came out as 764, the rest
# merged into unrelated ones (SkywirePaths into com.skycoin.wallet.eth.EthTxn,
# AddressBook into SignedTx) or inlined away, so a trace could name the wrong
# class. Pinning the names costs 38 KB.
# allowshrinking: an unused class is still removed, and members are still
# inlined and removed; only the class's identity is kept.
# Line numbers are another matter: R8 always rewrites them to instruction
# offsets under an `r8-map-id-…` source file, so a trace's line numbers mean
# something only when retraced with that build's mapping.txt.
-keep,allowshrinking class com.skycoin.**

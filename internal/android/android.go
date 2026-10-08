// Package android orchestrates Android-side build and (Day 3) provisioning.
// It owns the Gradle/aapt2 invocation, APK selection, and SDK binary discovery
// — anything that needs to know where avdmanager/sdkmanager/adb/aapt2/emulator
// live on the host.
//
// Nothing in this package writes to stdout — the JSON contract is owned by
// internal/cli. Progress events go through internal/progress.
package android

// Package ios orchestrates iOS-side build and (Day 3) provisioning. It owns
// the xcodebuild invocation, Info.plist parsing for CFBundleIdentifier, and
// the idb wrapper used during data-container wipe.
//
// Nothing in this package writes to stdout — the JSON contract is owned by
// internal/cli. Progress events go through internal/progress.
package ios

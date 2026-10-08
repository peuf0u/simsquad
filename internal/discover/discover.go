// Package discover enumerates the host's installed iOS simulators, Android
// system images, and sibling project repositories. It powers the `equip`
// wizard's "what device types are available?" prompts and is consumed by the
// build/provisioning layers to normalise user-supplied runtime/device strings
// into the full identifiers simctl/avdmanager require.
//
// Every function returns plain data structures with no side effects beyond the
// subprocess calls they require. UI presentation lives in internal/tui.
package discover

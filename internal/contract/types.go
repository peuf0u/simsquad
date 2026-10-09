// Package contract defines the on-disk and stdout JSON shapes that simsquad
// produces. Every field name here is part of the documented surface area —
// downstream consumers (e.g. the qa-run skill) parse these by key, so renames
// are breaking changes.
package contract

// Platform identifies an OS family. iOS simulators and Android emulators are
// the only first-class targets today; physical Android devices share the same
// Platform but set the Physical flag on Device.
type Platform string

// Platform constants.
const (
	PlatformIOS     Platform = "ios"
	PlatformAndroid Platform = "android"
)

// DeviceStatus is the readiness state of a Device after provisioning.
type DeviceStatus string

// DeviceStatus constants.
const (
	StatusReady DeviceStatus = "ready"
	StatusError DeviceStatus = "error"
)

// AndroidTarget distinguishes emulator vs. physical Android devices.
type AndroidTarget string

// AndroidTarget constants.
const (
	TargetEmulator AndroidTarget = "emulator"
	TargetPhysical AndroidTarget = "physical"
)

// IosSpec describes one row of the iOS sim matrix, either from config or
// `--ios-spec DEVICE:RUNTIME:COUNT`.
type IosSpec struct {
	Device  string `json:"device"`
	Runtime string `json:"runtime"`
	Count   int    `json:"count"`
}

// AndroidSpec describes one row of the Android sim matrix, either from config
// or `--android-spec DEVICE:IMAGE:COUNT[:TARGET]`.
type AndroidSpec struct {
	Device string        `json:"device"`
	Image  string        `json:"image"`
	Count  int           `json:"count"`
	Target AndroidTarget `json:"target"`
}

// Device is a discriminated record for any provisioned simulator, emulator, or
// physical device. Platform-specific fields are tagged omitempty so iOS records
// don't leak Android keys and vice versa.
type Device struct {
	Platform     Platform     `json:"platform"`
	Name         string       `json:"name"`
	UDID         string       `json:"udid"` // empty string when Status == StatusError pre-create
	BundleID     string       `json:"bundle_id"`
	Status       DeviceStatus `json:"status"`
	ErrorMessage string       `json:"error_message,omitempty"`
	// ReadyAt is stamped by every deploy that (re)installs the app, so it
	// moves on spec-match reuse. Empty on records written before 0.3.0.
	ReadyAt string `json:"ready_at,omitempty"`

	// iOS only.
	DeviceType string `json:"device_type,omitempty"`
	Runtime    string `json:"runtime,omitempty"`

	// Android only.
	AVDName       string `json:"avd_name,omitempty"`
	Port          int    `json:"port,omitempty"`
	DeviceProfile string `json:"device_profile,omitempty"`
	SystemImage   string `json:"system_image,omitempty"`
	Physical      bool   `json:"physical,omitempty"`
	EmulatorPID   int    `json:"emulator_pid,omitempty"`
}

// IosBuild is the iOS half of Builds. Either the success fields are populated
// or Error is — never both.
type IosBuild struct {
	AppPath  string    `json:"app_path,omitempty"`
	BundleID string    `json:"bundle_id,omitempty"`
	GitSHA   string    `json:"git_sha,omitempty"`
	Reused   bool      `json:"reused,omitempty"`
	Specs    []IosSpec `json:"specs,omitempty"`
	Error    string    `json:"error,omitempty"`
}

// AndroidBuild is the Android half of Builds. Either the success fields are
// populated or Error is — never both.
type AndroidBuild struct {
	APKPath  string        `json:"apk_path,omitempty"`
	BundleID string        `json:"bundle_id,omitempty"`
	GitSHA   string        `json:"git_sha,omitempty"`
	Reused   bool          `json:"reused,omitempty"`
	Specs    []AndroidSpec `json:"specs,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// Builds aggregates per-platform build outputs on a SquadRecord. Each pointer
// is nil when that platform isn't part of the requested matrix.
type Builds struct {
	IOS     *IosBuild     `json:"ios,omitempty"`
	Android *AndroidBuild `json:"android,omitempty"`
}

// SquadRecord is the internal persisted shape at
// ~/.cache/simsquad/<name>.json. Name is the squad's identifier and the
// registry key. Ports is internal-only (allocator state) and is excluded from
// Public output.
//
// Env is the squad's free-form test context — credentials, API endpoint hints,
// feature flags, locale, anything a consumer (qa-run skill, CI script, AI
// agent) needs to drive tests. Simsquad does not parse env values; the
// execution layer (ios/, android/) does not read env. Mutated via
// `simsquad set-env`.
type SquadRecord struct {
	Name      string            `json:"name"`
	CreatedAt string            `json:"created_at"`
	Builds    Builds            `json:"builds"`
	Devices   []Device          `json:"devices"`
	Env       map[string]string `json:"env,omitempty"`
	Ports     map[string]int    `json:"ports,omitempty"`
}

// Public projects a SquadRecord into the stdout/status JSON shape. It drops
// the internal Ports map; callers may set Reused on top.
func (s SquadRecord) Public() SquadPublic {
	return SquadPublic{
		Name:      s.Name,
		CreatedAt: s.CreatedAt,
		Builds:    s.Builds,
		Devices:   s.Devices,
		Env:       s.Env,
	}
}

// SquadPublic is the JSON shape emitted on stdout by `simsquad deploy` and
// `simsquad status --name=…`.
type SquadPublic struct {
	Name      string            `json:"name"`
	CreatedAt string            `json:"created_at"`
	Builds    Builds            `json:"builds"`
	Devices   []Device          `json:"devices"`
	Env       map[string]string `json:"env,omitempty"`

	Reused bool `json:"reused,omitempty"`
}

// DeviceView is the uniform per-device shape emitted by `simsquad devices`.
// It carries the minimal capability set qa-run needs to branch test selection
// and connect to the device. Fat per-platform fields (AVD name, port, system
// image, etc.) stay in the persisted Device and are reachable via
// `status --name=<squad>` for orchestrators that want them.
//
// Env is a defensive copy of the parent squad's env, surfaced on every device
// row so consumers don't need a cross-reference back to the squad record.
type DeviceView struct {
	Platform  Platform          `json:"platform"`
	ID        string            `json:"id"` // udid (iOS) or serial (Android)
	Name      string            `json:"name"`
	Model     string            `json:"model"`      // iPhone 17 / pixel_7
	OSVersion string            `json:"os_version"` // iOS 26.4 / Android <N> / API <N>
	BundleID  string            `json:"bundle_id,omitempty"`
	Status    DeviceStatus      `json:"status"`
	ReadyAt   string            `json:"ready_at,omitempty"`
	Env       map[string]string `json:"env,omitempty"`
}

// NewDeviceView projects a Device into the DeviceView shape, mapping the
// platform-specific fields onto the uniform Model + OSVersion fields. The
// device's own ReadyAt wins; readyAt (typically the squad-record creation
// time) is the fallback for records that predate per-device stamps. env is a defensive copy of the squad's env, passed in so consumers see
// the test context alongside the device handle.
func NewDeviceView(d Device, readyAt string, env map[string]string) DeviceView {
	v := DeviceView{
		Platform: d.Platform,
		ID:       d.UDID,
		Name:     d.Name,
		BundleID: d.BundleID,
		Status:   d.Status,
	}
	if d.Status == StatusReady {
		v.ReadyAt = readyAt
		if d.ReadyAt != "" {
			v.ReadyAt = d.ReadyAt
		}
	}
	switch d.Platform {
	case PlatformIOS:
		v.Model = d.DeviceType
		v.OSVersion = d.Runtime
	case PlatformAndroid:
		v.Model = d.DeviceProfile
		v.OSVersion = androidOSVersion(d.SystemImage)
	}
	if len(env) > 0 {
		v.Env = make(map[string]string, len(env))
		for k, val := range env {
			v.Env[k] = val
		}
	}
	return v
}

// androidOSVersion extracts a human-readable API level from an avdmanager
// system-image identifier like
// "system-images;android-34;google_apis;arm64-v8a". On parse failure the raw
// identifier is returned unchanged so callers still see something useful.
func androidOSVersion(systemImage string) string {
	if systemImage == "" {
		return ""
	}
	// Split on ';' and look for the android-N part.
	for _, part := range splitSemicolons(systemImage) {
		if len(part) > len("android-") && part[:len("android-")] == "android-" {
			return "Android API " + part[len("android-"):]
		}
	}
	return systemImage
}

func splitSemicolons(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == ';' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

// SquadIndexEntry is one row of the registry at ~/.cache/simsquad/squads.json.
type SquadIndexEntry struct {
	Name       string `json:"name"`
	CreatedAt  string `json:"created_at"`
	LastUsedAt string `json:"last_used_at"`
	StateFile  string `json:"state_file"`
}

// SquadIndex is the on-disk format of the registry file. The map key is the
// squad name; the entry's Name field repeats it so a flattened list is also
// self-describing.
type SquadIndex struct {
	Squads map[string]SquadIndexEntry `json:"squads"`
}

// SkillStatus is the stdout of `simsquad skill status`: whether the agent
// skills installed in the app repo and the mobilecli on PATH fit this
// binary. The simsquad-test skill reads it as its first step.
type SkillStatus struct {
	Installed bool `json:"installed"`
	// SkillContract is the contract the installed skills were written for
	// (0 when none are installed); BinaryContract is this binary's.
	SkillContract  int  `json:"skill_contract"`
	BinaryContract int  `json:"binary_contract"`
	InSync         bool `json:"in_sync"`
	// Warning says what to do when the skills don't fit: re-run `skill
	// install` (missing or older skills) or upgrade simsquad (newer skills).
	Warning   string          `json:"warning,omitempty"`
	Mobilecli MobilecliStatus `json:"mobilecli"`
}

// MobilecliStatus reports the mobilecli found on PATH against the minimum
// version simsquad was tested with.
type MobilecliStatus struct {
	Found        bool   `json:"found"`
	Version      string `json:"version,omitempty"`
	Minimum      string `json:"minimum"`
	MeetsMinimum bool   `json:"meets_minimum"`
}

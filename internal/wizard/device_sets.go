package wizard

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
)

// deviceSet is one named device set as equip edits it: the rows deploy uses,
// and which file they live in. equip rewrites both TOMLs on every save, so a
// set defined in simsquad.local.toml is written back there, and the
// simsquad.toml set it replaces (shadow) is written back unchanged: the team
// still gets the shared set.
type deviceSet struct {
	name    string
	ios     []contract.IosSpec
	android []contract.AndroidSpec
	local   bool
	shadow  *config.DeviceSet
}

// DeviceSetSummary is one device set in `equip --show`. Local is true when
// simsquad.local.toml defines it (replacing any simsquad.toml set of that
// name).
type DeviceSetSummary struct {
	Name    string                 `json:"name"`
	Local   bool                   `json:"local,omitempty"`
	IOS     []contract.IosSpec     `json:"ios"`
	Android []contract.AndroidSpec `json:"android"`
}

// deviceSetNameRx is what equip accepts for a new or renamed set: a bare TOML
// key, so the name reads the same in the file and on `deploy --device-set`.
// Hand-written sets with other names still load and save (quoted).
var deviceSetNameRx = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateDeviceSetName(name string) error {
	if !deviceSetNameRx.MatchString(name) {
		return fmt.Errorf("device set name %q: use letters, digits, '-' and '_' only", name)
	}
	return nil
}

func loadDeviceSets(cfg *config.Config) []deviceSet {
	var out []deviceSet
	for _, name := range cfg.DeviceSetNames() {
		set, _ := cfg.DeviceSet(name)
		ds := deviceSet{name: name, ios: set.IOS, android: set.Android, local: cfg.DeviceSetIsLocal(name)}
		if ds.local {
			if shared, ok := cfg.SharedDeviceSet(name); ok {
				ds.shadow = &shared
			}
		}
		out = append(out, ds)
	}
	return out
}

// findDeviceSet returns the index of the named set in sets, or -1.
func findDeviceSet(sets []deviceSet, name string) int {
	for i := range sets {
		if sets[i].name == name {
			return i
		}
	}
	return -1
}

// addToDeviceSet appends rows to the named set, creating it in simsquad.toml
// when no file defines it. An existing set gets the rows where deploy reads
// it from, which is the local file for a local set.
func addToDeviceSet(state *wizardState, name string, ios []contract.IosSpec, android []contract.AndroidSpec) {
	i := findDeviceSet(state.deviceSets, name)
	if i < 0 {
		state.deviceSets = append(state.deviceSets, deviceSet{name: name})
		sortDeviceSets(state.deviceSets)
		i = findDeviceSet(state.deviceSets, name)
	}
	state.deviceSets[i].ios = append(state.deviceSets[i].ios, ios...)
	state.deviceSets[i].android = append(state.deviceSets[i].android, android...)
}

// renameDeviceSet renames sets[i] in a copy of sets. Renaming a local set
// that replaces a shared one renames only the local set: the shared set
// stays in simsquad.toml under its name, for the teammates and squads that
// use it.
func renameDeviceSet(sets []deviceSet, i int, name string) []deviceSet {
	out := append([]deviceSet{}, sets...)
	if shadow := out[i].shadow; shadow != nil {
		out = append(out, deviceSet{name: out[i].name, ios: shadow.IOS, android: shadow.Android})
		out[i].shadow = nil
	}
	out[i].name = name
	sortDeviceSets(out)
	return out
}

// deleteDeviceSet deletes sets[i] from a copy of sets. Deleting a local set
// that replaces a shared one deletes only the local set, so the shared set
// is the one deploy reads again; a shared set is deleted from simsquad.toml.
func deleteDeviceSet(sets []deviceSet, i int) []deviceSet {
	out := append([]deviceSet{}, sets...)
	if shadow := out[i].shadow; shadow != nil {
		out[i] = deviceSet{name: out[i].name, ios: shadow.IOS, android: shadow.Android}
		return out
	}
	return append(out[:i], out[i+1:]...)
}

func sortDeviceSets(sets []deviceSet) {
	sort.SliceStable(sets, func(i, j int) bool { return sets[i].name < sets[j].name })
}

func summarizeDeviceSets(sets []deviceSet) []DeviceSetSummary {
	if len(sets) == 0 {
		return nil
	}
	out := make([]DeviceSetSummary, 0, len(sets))
	for _, s := range sets {
		ios := append([]contract.IosSpec{}, s.ios...)
		android := append([]contract.AndroidSpec{}, s.android...)
		out = append(out, DeviceSetSummary{Name: s.name, Local: s.local, IOS: ios, Android: android})
	}
	return out
}

// renderProjectDeviceSets writes the sets simsquad.toml holds: every shared
// set, and the shared version under each local set that replaces one, always
// under the name it has in simsquad.toml (the editor never renames it).
func renderProjectDeviceSets(b *strings.Builder, sets []deviceSet) {
	for _, s := range sets {
		switch {
		case !s.local:
			renderDeviceSet(b, s.name, s.ios, s.android)
		case s.shadow != nil:
			renderDeviceSet(b, s.name, s.shadow.IOS, s.shadow.Android)
		}
	}
}

// renderLocalDeviceSets writes the sets simsquad.local.toml defines.
func renderLocalDeviceSets(b *strings.Builder, sets []deviceSet) {
	for _, s := range sets {
		if s.local {
			renderDeviceSet(b, s.name, s.ios, s.android)
		}
	}
}

// renderDeviceSet writes one set as a [device_sets.<name>] header followed by
// rows shaped like the default matrix's. The header keeps a set with no rows
// in the file and marks where each set starts.
func renderDeviceSet(b *strings.Builder, name string, ios []contract.IosSpec, android []contract.AndroidSpec) {
	key := "device_sets." + tomlKey(name)
	b.WriteString("\n[" + key + "]\n")
	for _, spec := range ios {
		b.WriteString("\n[[" + key + ".ios.sims]]\n")
		writeIOSRow(b, spec)
	}
	for _, spec := range android {
		b.WriteString("\n[[" + key + ".android.sims]]\n")
		writeAndroidRow(b, spec)
	}
}

func tomlKey(name string) string {
	if deviceSetNameRx.MatchString(name) {
		return name
	}
	return tomlQuote(name)
}

// tomlQuote writes s as a TOML basic string. Go's strconv.Quote escapes
// (\a, \v, \x..) aren't TOML, and a file equip writes with them fails the
// next load. Invalid UTF-8 can't be written to TOML at all; it becomes
// U+FFFD rather than refusing the whole save over one hand-written byte.
func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for len(s) > 0 {
		r, size := utf8.DecodeRuneInString(s)
		s = s[size:]
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
				continue
			}
			b.WriteRune(r) // utf8.RuneError for an invalid byte: U+FFFD
		}
	}
	b.WriteByte('"')
	return b.String()
}

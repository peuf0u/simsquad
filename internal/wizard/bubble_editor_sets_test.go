package wizard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peuf0u/simsquad/internal/contract"
)

const menuDeviceSets = 4 // Device sets… in the top menu.

func press(t *testing.T, m editorModel, keys ...string) editorModel {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		next, _ := m.Update(msg)
		m = next.(editorModel)
	}
	return m
}

// submit fills the open form's fields with values and presses its done row.
func submit(t *testing.T, m editorModel, values ...string) editorModel {
	t.Helper()
	if m.mode != editorFields {
		t.Fatalf("mode = %v, want a form open", m.mode)
	}
	for i, v := range values {
		m.fields[i].value = v
	}
	m.fieldIdx = len(m.fields)
	return press(t, m, "enter")
}

func tabletState() wizardState {
	return wizardState{
		iosSpecs: []contract.IosSpec{{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1}},
		deviceSets: []deviceSet{{
			name: "tablet",
			ios:  []contract.IosSpec{{Device: "iPad Pro 13-inch (M5)", Runtime: "iOS 26.4", Count: 1}},
		}},
	}
}

func TestBubbleEditorCreatesDeviceSetAndAddsRows(t *testing.T) {
	t.Parallel()
	m := editorModel{configDir: "/tmp/app", mode: editorMenu, cursor: menuDeviceSets,
		state: wizardState{iosSpecs: []contract.IosSpec{{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1}}}}

	m = press(t, m, "enter")
	if m.activeSubmenu != submenuSets {
		t.Fatalf("activeSubmenu = %v, want the device sets submenu", m.activeSubmenu)
	}
	m = press(t, m, "enter") // New device set (the only set row is none)
	m = submit(t, m, "tablet")
	if len(m.state.deviceSets) != 1 || m.state.deviceSets[0].name != "tablet" || m.state.deviceSets[0].local {
		t.Fatalf("deviceSets = %+v, want a new shared tablet set", m.state.deviceSets)
	}
	if m.activeSubmenu != submenuSet || m.activeSet != "tablet" || !m.dirty {
		t.Fatalf("submenu %v set %q dirty %v, want the new set opened", m.activeSubmenu, m.activeSet, m.dirty)
	}

	// iOS simulators… → Add simulator, with the default matrix's pickers.
	m = press(t, m, "enter", "enter")
	if m.inputKind != actionAddIOS || len(m.fields) != 3 || m.fields[0].choices == nil && m.fields[0].value == "" {
		t.Fatalf("inputKind %q fields %+v, want the add-simulator form", m.inputKind, m.fields)
	}
	m = submit(t, m, "iPad Air 11-inch (M3)", "iOS 26.4", "2")
	want := contract.IosSpec{Device: "iPad Air 11-inch (M3)", Runtime: "iOS 26.4", Count: 2}
	if got := m.state.deviceSets[0].ios; len(got) != 1 || got[0] != want {
		t.Fatalf("tablet iOS rows = %+v, want [%+v]", got, want)
	}
	if len(m.state.iosSpecs) != 1 || m.state.iosSpecs[0].Device != "iPhone 17" {
		t.Fatalf("default iOS matrix = %+v, want it untouched", m.state.iosSpecs)
	}

	// Back out level by level to the top menu, on the Device sets row.
	m = press(t, m, "esc")
	if m.activeSubmenu != submenuSet || m.activeSet != "tablet" {
		t.Fatalf("after esc: submenu %v set %q, want the tablet set", m.activeSubmenu, m.activeSet)
	}
	m = press(t, m, "esc")
	if m.activeSubmenu != submenuSets || m.activeSet != "" {
		t.Fatalf("after esc: submenu %v set %q, want the sets list", m.activeSubmenu, m.activeSet)
	}
	m = press(t, m, "esc")
	if m.activeSubmenu != submenuNone || m.cursor != menuDeviceSets {
		t.Fatalf("after esc: submenu %v cursor %d, want the top menu on Device sets", m.activeSubmenu, m.cursor)
	}

	// The default matrix's iOS submenu still edits the default matrix.
	m.cursor = 2
	m = press(t, m, "enter", "enter")
	m = submit(t, m, "iPhone 16", "iOS 26.4", "1")
	if len(m.state.iosSpecs) != 2 || len(m.state.deviceSets[0].ios) != 1 {
		t.Fatalf("default %+v, tablet %+v; want the row in the default matrix", m.state.iosSpecs, m.state.deviceSets[0].ios)
	}
}

func TestBubbleEditorEditsDeviceSetRow(t *testing.T) {
	t.Parallel()
	m := editorModel{configDir: "/tmp/app", mode: editorMenu, cursor: menuDeviceSets, state: tabletState()}
	m = press(t, m, "enter", "enter") // Device sets… → tablet
	if m.activeSubmenu != submenuSet || m.activeSet != "tablet" {
		t.Fatalf("submenu %v set %q, want the tablet set", m.activeSubmenu, m.activeSet)
	}
	m = press(t, m, "enter") // iOS simulators…
	m.cursor = 1             // Edit simulator
	m = press(t, m, "enter")
	if m.mode != editorSpecs || m.specCount() != 1 {
		t.Fatalf("mode %v rows %d, want the tablet's one row listed", m.mode, m.specCount())
	}
	m = press(t, m, "enter")
	m = submit(t, m, "iPad Pro 13-inch (M5)", "iOS 26.4", "3")
	if got := m.state.deviceSets[0].ios[0].Count; got != 3 {
		t.Fatalf("tablet row count = %d, want 3", got)
	}
	if m.state.iosSpecs[0].Count != 1 {
		t.Fatalf("default matrix changed: %+v", m.state.iosSpecs)
	}
}

func TestBubbleEditorRenamesDeviceSet(t *testing.T) {
	t.Parallel()
	state := tabletState()
	state.deviceSets = append(state.deviceSets, deviceSet{name: "watch"})
	m := editorModel{configDir: "/tmp/app", mode: editorMenu, cursor: menuDeviceSets, state: state}
	m = press(t, m, "enter", "enter") // tablet
	m.cursor = 2                      // Rename set
	m = press(t, m, "enter")
	if m.fields[0].value != "tablet" {
		t.Fatalf("rename form value = %q, want the current name", m.fields[0].value)
	}

	for _, bad := range []string{"watch", "two words"} {
		m = submit(t, m, bad)
		if m.mode != editorFields || m.state.deviceSets[0].name != "tablet" || m.message == "" {
			t.Fatalf("rename to %q: mode %v sets %+v message %q, want refused", bad, m.mode, m.state.deviceSets, m.message)
		}
	}

	m = submit(t, m, "big-tablet")
	names := []string{m.state.deviceSets[0].name, m.state.deviceSets[1].name}
	if names[0] != "big-tablet" || names[1] != "watch" || len(m.state.deviceSets[0].ios) != 1 {
		t.Fatalf("sets = %+v, want tablet renamed with its rows", m.state.deviceSets)
	}
	if m.activeSubmenu != submenuSet || m.activeSet != "big-tablet" {
		t.Fatalf("submenu %v set %q, want the renamed set still open", m.activeSubmenu, m.activeSet)
	}
}

func TestBubbleEditorDeletesDeviceSet(t *testing.T) {
	t.Parallel()
	m := editorModel{configDir: "/tmp/app", mode: editorMenu, cursor: menuDeviceSets, state: tabletState()}
	m = press(t, m, "enter", "enter") // tablet
	m.cursor = 3                      // Delete set
	m = press(t, m, "enter")
	if m.mode != editorConfirm || !strings.Contains(m.confirmTitle, "tablet") {
		t.Fatalf("mode %v title %q, want a confirm naming the set", m.mode, m.confirmTitle)
	}
	m = press(t, m, "n")
	if len(m.state.deviceSets) != 1 || m.activeSubmenu != submenuSet {
		t.Fatal("declined delete removed the set or left the set menu")
	}
	m = press(t, m, "enter", "y")
	if len(m.state.deviceSets) != 0 || !m.dirty {
		t.Fatalf("deviceSets = %+v, want none after delete", m.state.deviceSets)
	}
	if m.activeSubmenu != submenuSets || m.activeSet != "" {
		t.Fatalf("submenu %v set %q, want back on the sets list", m.activeSubmenu, m.activeSet)
	}
}

func TestBubbleEditorSummaryShowsDeviceSets(t *testing.T) {
	t.Parallel()
	state := tabletState()
	state.deviceSets = append(state.deviceSets, deviceSet{name: "mine", local: true})
	view := editorModel{configDir: "/tmp/app", mode: editorMenu, width: 140, state: state}.View()
	for _, want := range []string{"Device sets", "tablet", "iPad Pro 13-inch (M5)", "mine", "local"} {
		if !strings.Contains(view, want) {
			t.Fatalf("view missing %q:\n%s", want, view)
		}
	}
}

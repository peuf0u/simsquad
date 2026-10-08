package wizard

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peuf0u/simsquad/internal/contract"
)

func TestBubbleEditorAddIOSSpecThroughModel(t *testing.T) {
	t.Parallel()

	model := editorModel{
		configDir: "/tmp/app",
		state: wizardState{
			iosScheme:  "App",
			gradleTask: ":app:assembleDebug",
		},
		mode:   editorMenu,
		cursor: 2, // iOS matrix… (descends into iOS submenu).
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.activeSubmenu != submenuIOS {
		t.Fatalf("activeSubmenu = %v, want submenuIOS", model.activeSubmenu)
	}
	// Cursor is now at 0 inside the iOS submenu, which is "Add simulator".
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorFields {
		t.Fatalf("mode = %v, want editorFields", model.mode)
	}
	model.fields = []editorField{
		{label: "Device", value: "iPhone 16 Pro", required: true},
		{label: "Runtime", value: "iOS 26.1", required: true},
		{label: "Count", value: "2", required: true, count: true},
	}
	model.fieldIdx = len(model.fields)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)

	want := contract.IosSpec{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 2}
	if len(model.state.iosSpecs) != 1 || model.state.iosSpecs[0] != want {
		t.Fatalf("iosSpecs = %+v, want [%+v]", model.state.iosSpecs, want)
	}
}

func TestBubbleEditorProjectEditsSingleField(t *testing.T) {
	t.Parallel()

	model := editorModel{
		configDir: "/tmp/app",
		state: wizardState{
			iosRepo:     "/tmp/app/ios",
			androidRepo: "/tmp/app/android",
			iosScheme:   "Old",
			gradleTask:  ":app:assembleDebug",
		},
		mode:   editorMenu,
		cursor: 1, // Edit project settings.
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	model.fieldIdx = 2 // iOS scheme.
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorValue {
		t.Fatalf("mode = %v, want editorValue", model.mode)
	}
	model.edit.SetValue("NewScheme")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)

	if model.state.iosScheme != "NewScheme" {
		t.Fatalf("iosScheme = %q, want NewScheme", model.state.iosScheme)
	}
	if model.mode != editorFields {
		t.Fatalf("mode = %v, want editorFields", model.mode)
	}
}

func TestBubbleEditorEditsExistingIOSSpecSingleField(t *testing.T) {
	t.Parallel()

	model := editorModel{
		configDir: "/tmp/app",
		state: wizardState{
			iosSpecs: []contract.IosSpec{
				{Device: "iPhone 16", Runtime: "iOS 26.1", Count: 1},
			},
		},
		mode:   editorMenu,
		cursor: 2, // iOS matrix… (descends into iOS submenu).
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.activeSubmenu != submenuIOS {
		t.Fatalf("activeSubmenu = %v, want submenuIOS", model.activeSubmenu)
	}
	// iOS submenu: 0=Add, 1=Edit, 2=Remove, 3=Clear all, 4=Back.
	model.cursor = 1
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorSpecs {
		t.Fatalf("mode = %v, want editorSpecs", model.mode)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorFields {
		t.Fatalf("mode = %v, want editorFields", model.mode)
	}
	model.fieldIdx = 2 // Count.
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	model.edit.SetValue("3")
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	model.fieldIdx = len(model.fields)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)

	if got := model.state.iosSpecs[0].Count; got != 3 {
		t.Fatalf("count = %d, want 3", got)
	}
	if model.mode != editorSpecs {
		t.Fatalf("mode = %v, want editorSpecs", model.mode)
	}
}

func TestBubbleEditorChoiceSelectUpdatesField(t *testing.T) {
	t.Parallel()

	model := editorModel{
		mode:       editorFields,
		inputKind:  actionAddIOS,
		inputTitle: "Add iOS simulator",
		fields: []editorField{
			{
				label:    "Device",
				value:    "iPhone 16",
				required: true,
				choices: []editorChoice{
					{label: "iPhone 16", value: "iPhone 16"},
					{label: "iPhone 17", value: "iPhone 17"},
				},
			},
			{label: "Runtime", value: "iOS 26.1", required: true},
			{label: "Count", value: "1", required: true, count: true},
		},
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorSelect {
		t.Fatalf("mode = %v, want editorSelect", model.mode)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyDown})
	model = next.(editorModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)

	if got := model.fields[0].value; got != "iPhone 17" {
		t.Fatalf("field value = %q, want iPhone 17", got)
	}
	if model.mode != editorFields {
		t.Fatalf("mode = %v, want editorFields", model.mode)
	}
}

func TestBubbleEditorChoiceSelectWindowIsCapped(t *testing.T) {
	t.Parallel()

	model := editorModel{
		mode:      editorSelect,
		width:     72,
		height:    20,
		selectIdx: 12,
		fields: []editorField{
			{
				label: "Device",
				value: "iPhone 12",
				choices: []editorChoice{
					{label: "iPhone 00", value: "iPhone 00"},
					{label: "iPhone 01", value: "iPhone 01"},
					{label: "iPhone 02", value: "iPhone 02"},
					{label: "iPhone 03", value: "iPhone 03"},
					{label: "iPhone 04", value: "iPhone 04"},
					{label: "iPhone 05", value: "iPhone 05"},
					{label: "iPhone 06", value: "iPhone 06"},
					{label: "iPhone 07", value: "iPhone 07"},
					{label: "iPhone 08", value: "iPhone 08"},
					{label: "iPhone 09", value: "iPhone 09"},
					{label: "iPhone 10", value: "iPhone 10"},
					{label: "iPhone 11", value: "iPhone 11"},
					{label: "iPhone 12", value: "iPhone 12"},
					{label: "iPhone 13", value: "iPhone 13"},
					{label: "iPhone 14", value: "iPhone 14"},
					{label: "iPhone 15", value: "iPhone 15"},
					{label: "iPhone 16", value: "iPhone 16"},
					{label: "iPhone 17", value: "iPhone 17"},
					{label: "iPhone 18", value: "iPhone 18"},
					{label: "iPhone 19", value: "iPhone 19"},
					{label: "iPhone 20", value: "iPhone 20"},
					{label: "iPhone 21", value: "iPhone 21"},
					{label: "iPhone 22", value: "iPhone 22"},
					{label: "iPhone 23", value: "iPhone 23"},
				},
			},
		},
	}

	view := model.selectPanelView()
	if got := strings.Count(view, "iPhone "); got > model.selectVisibleRows() {
		t.Fatalf("rendered %d choices, want <= %d\n%s", got, model.selectVisibleRows(), view)
	}
	if !strings.Contains(view, "more above") || !strings.Contains(view, "more below") {
		t.Fatalf("view does not show scroll markers:\n%s", view)
	}
}

func TestBubbleEditorRemoveIOSSpec(t *testing.T) {
	t.Parallel()

	model := editorModel{
		configDir: "/tmp/app",
		state: wizardState{
			iosSpecs: []contract.IosSpec{
				{Device: "iPhone 16", Runtime: "iOS 26.1", Count: 1},
				{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 2},
			},
		},
		mode:   editorMenu,
		cursor: 2, // iOS matrix… (descend).
	}

	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	model.cursor = 2 // Remove simulator inside iOS submenu.
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorSpecs || model.inputKind != actionRemoveIOS {
		t.Fatalf("mode=%v inputKind=%q, want editorSpecs/remove-ios", model.mode, model.inputKind)
	}

	// Highlight the second spec and confirm removal.
	model.fieldIdx = 1
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.mode != editorConfirm || model.confirmKind != actionRemoveIOS {
		t.Fatalf("mode=%v confirmKind=%q, want editorConfirm/remove-ios", model.mode, model.confirmKind)
	}
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = next.(editorModel)

	if got := len(model.state.iosSpecs); got != 1 {
		t.Fatalf("iosSpecs length = %d, want 1", got)
	}
	if model.state.iosSpecs[0].Device != "iPhone 16" {
		t.Fatalf("remaining spec = %+v, want iPhone 16", model.state.iosSpecs[0])
	}
	if !model.dirty {
		t.Fatal("model.dirty = false, want true after removal")
	}
	if model.mode != editorSpecs {
		t.Fatalf("mode after removal = %v, want editorSpecs (list still has items)", model.mode)
	}

	// Remove the last one — should pop back to the iOS submenu.
	model.fieldIdx = 0
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	model = next.(editorModel)
	if len(model.state.iosSpecs) != 0 {
		t.Fatalf("iosSpecs length after second remove = %d, want 0", len(model.state.iosSpecs))
	}
	if model.mode != editorMenu {
		t.Fatalf("mode after final remove = %v, want editorMenu", model.mode)
	}
	if model.activeSubmenu != submenuIOS {
		t.Fatalf("activeSubmenu = %v, want submenuIOS (should stay in iOS submenu)", model.activeSubmenu)
	}
}

func TestBubbleEditorSubmenuBackRestoresCursor(t *testing.T) {
	t.Parallel()

	model := editorModel{mode: editorMenu, cursor: 3} // Android matrix… in the top menu.
	next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = next.(editorModel)
	if model.activeSubmenu != submenuAndroid {
		t.Fatalf("activeSubmenu = %v, want submenuAndroid", model.activeSubmenu)
	}
	if model.cursor != 0 {
		t.Fatalf("submenu cursor = %d, want 0", model.cursor)
	}

	// Press esc — should pop and restore the parent cursor (3).
	next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEsc})
	model = next.(editorModel)
	if model.activeSubmenu != submenuNone {
		t.Fatalf("activeSubmenu after back = %v, want submenuNone", model.activeSubmenu)
	}
	if model.cursor != 3 {
		t.Fatalf("cursor after back = %d, want 3", model.cursor)
	}
}

func TestBubbleEditorQuitWithoutSaveCancels(t *testing.T) {
	t.Parallel()

	model := editorModel{mode: editorMenu}
	next, cmd := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	model = next.(editorModel)
	if model.err != errEditorCancelled {
		t.Fatalf("err = %v, want %v", model.err, errEditorCancelled)
	}
	if cmd == nil {
		t.Fatal("quit command is nil")
	}
}

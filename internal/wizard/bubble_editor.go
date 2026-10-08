package wizard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/discover"
)

var errEditorCancelled = errors.New("equip: cancelled")

type editorMode int

const (
	editorMenu editorMode = iota
	editorSpecs
	editorFields
	editorSelect
	editorValue
	editorConfirm
)

type editorAction string

const (
	actionSave          editorAction = "save"
	actionProject       editorAction = "project"
	actionIOSMatrix     editorAction = "ios-matrix"
	actionAndroidMatrix editorAction = "android-matrix"
	actionEditEnv       editorAction = "edit-env"
	actionBack          editorAction = "back"
	actionAddIOS        editorAction = "add-ios"
	actionEditIOS       editorAction = "edit-ios"
	actionRemoveIOS     editorAction = "remove-ios"
	actionAddAndroid    editorAction = "add-android"
	actionEditAndroid   editorAction = "edit-android"
	actionRemoveAndroid editorAction = "remove-android"
	actionAddPhysical   editorAction = "add-physical"
	actionClearIOS      editorAction = "clear-ios"
	actionClearAndroid  editorAction = "clear-android"
)

type submenuID int

const (
	submenuNone submenuID = iota
	submenuIOS
	submenuAndroid
)

type editorField struct {
	label    string
	value    string
	required bool
	count    bool
	choices  []editorChoice
}

type editorChoice struct {
	label string
	value string
}

type editorModel struct {
	configDir string
	state     wizardState
	width     int
	height    int

	mode          editorMode
	cursor        int
	activeSubmenu submenuID
	parentCursor  int
	message       string
	err           error
	saved         bool
	dirty         bool

	inputTitle string
	inputKind  editorAction
	fields     []editorField
	fieldIdx   int
	specIdx    int
	selectIdx  int
	edit       textinput.Model

	confirmTitle string
	confirmKind  editorAction
}

type menuItem struct {
	label  string
	group  string
	action editorAction
}

var editorMenuItems = []menuItem{
	{group: "Save", label: "Save and exit", action: actionSave},
	{group: "Project", label: "Project settings", action: actionProject},
	{group: "Matrix", label: "iOS matrix…", action: actionIOSMatrix},
	{group: "Matrix", label: "Android matrix…", action: actionAndroidMatrix},
	{group: "Env", label: "Edit env", action: actionEditEnv},
}

var iosSubmenuItems = []menuItem{
	{group: "iOS", label: "Add simulator", action: actionAddIOS},
	{group: "iOS", label: "Edit simulator", action: actionEditIOS},
	{group: "iOS", label: "Remove simulator", action: actionRemoveIOS},
	{group: "iOS", label: "Clear all", action: actionClearIOS},
	{group: "Nav", label: "Back", action: actionBack},
}

var androidSubmenuItems = []menuItem{
	{group: "Android", label: "Add emulator", action: actionAddAndroid},
	{group: "Android", label: "Add physical devices", action: actionAddPhysical},
	{group: "Android", label: "Edit spec", action: actionEditAndroid},
	{group: "Android", label: "Remove spec", action: actionRemoveAndroid},
	{group: "Android", label: "Clear all", action: actionClearAndroid},
	{group: "Nav", label: "Back", action: actionBack},
}

func (m editorModel) currentMenuItems() []menuItem {
	switch m.activeSubmenu {
	case submenuIOS:
		return iosSubmenuItems
	case submenuAndroid:
		return androidSubmenuItems
	default:
		return editorMenuItems
	}
}

var (
	editorTitleStyle     = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("230"))
	editorTitleAccent    = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	editorSeparatorStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("238"))
	editorDimStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("245"))
	editorSubtleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
	editorWarnStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true)
	editorOKStyle        = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	editorChipStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("236")).Padding(0, 1)
	editorDirtyStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("230")).Background(lipgloss.Color("214")).Bold(true).Padding(0, 1)
	editorPanelStyle     = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("240")).
				Padding(0, 1)
	editorActivePanelStyle = editorPanelStyle.BorderForeground(lipgloss.Color("208"))
	editorSelectedStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	editorCursorBarStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("208")).Bold(true)
	editorValueStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("253"))
	editorPlaceholderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Italic(true)
)

func runBubbleEditor(state *wizardState, configDir string) error {
	model := editorModel{
		configDir: configDir,
		state:     *state,
		mode:      editorMenu,
		width:     96,
	}
	program := tea.NewProgram(model, tea.WithInput(os.Stdin), tea.WithOutput(os.Stderr))
	finalModel, err := program.Run()
	if err != nil {
		return err
	}
	final, ok := finalModel.(editorModel)
	if !ok {
		return errors.New("equip: editor returned unexpected model")
	}
	if final.err != nil {
		return final.err
	}
	if !final.saved {
		return errEditorCancelled
	}
	*state = final.state
	return nil
}

func (m editorModel) Init() tea.Cmd { return nil }

func (m editorModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	case tea.KeyMsg:
		return m.updateKey(msg)
	default:
		return m, nil
	}
}

func (m editorModel) updateKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "ctrl+c":
		m.err = errEditorCancelled
		return m, tea.Quit
	case "s":
		if m.mode == editorMenu {
			m.saved = true
			return m, tea.Quit
		}
	case "esc":
		switch m.mode {
		case editorMenu:
			if m.activeSubmenu != submenuNone {
				return m.popSubmenu(), nil
			}
			m.err = errEditorCancelled
			return m, tea.Quit
		case editorValue:
			m.mode = editorFields
			m.message = "edit cancelled"
			return m, nil
		case editorSelect:
			m.mode = editorFields
			m.message = "selection cancelled"
			return m, nil
		case editorFields:
			m.backFromFields()
			return m, nil
		case editorSpecs:
			m.mode = editorMenu
			m.message = ""
			return m, nil
		case editorConfirm:
			if isRemoveAction(m.confirmKind) {
				m.mode = editorSpecs
				m.message = ""
				return m, nil
			}
			m.mode = editorMenu
			m.message = ""
			return m, nil
		default:
			m.mode = editorMenu
			m.message = ""
			return m, nil
		}
	}

	switch m.mode {
	case editorMenu:
		return m.updateMenu(key)
	case editorSpecs:
		return m.updateSpecs(key)
	case editorFields:
		return m.updateFields(key)
	case editorSelect:
		return m.updateSelect(key)
	case editorValue:
		return m.updateValue(key)
	case editorConfirm:
		return m.updateConfirm(key)
	default:
		return m, nil
	}
}

func (m editorModel) updateMenu(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	items := m.currentMenuItems()
	switch key.String() {
	case "up", "k":
		if m.cursor > 0 {
			m.cursor--
		}
	case "down", "j":
		if m.cursor < len(items)-1 {
			m.cursor++
		}
	case "enter":
		return m.startAction(items[m.cursor].action)
	case "q":
		if m.activeSubmenu != submenuNone {
			return m.popSubmenu(), nil
		}
		m.err = errEditorCancelled
		return m, tea.Quit
	}
	return m, nil
}

func (m editorModel) pushSubmenu(sub submenuID) editorModel {
	m.parentCursor = m.cursor
	m.activeSubmenu = sub
	m.cursor = 0
	m.message = ""
	return m
}

func (m editorModel) popSubmenu() editorModel {
	m.activeSubmenu = submenuNone
	m.cursor = m.parentCursor
	m.parentCursor = 0
	m.message = ""
	return m
}

func (m editorModel) startAction(action editorAction) (tea.Model, tea.Cmd) {
	m.message = ""
	switch action {
	case actionSave:
		m.saved = true
		return m, tea.Quit
	case actionIOSMatrix:
		return m.pushSubmenu(submenuIOS), nil
	case actionAndroidMatrix:
		return m.pushSubmenu(submenuAndroid), nil
	case actionBack:
		return m.popSubmenu(), nil
	case actionRemoveIOS:
		if len(m.state.iosSpecs) == 0 {
			m.message = "iOS matrix is empty"
			return m, nil
		}
		m.mode = editorSpecs
		m.inputTitle = "Remove iOS simulator"
		m.inputKind = action
		m.fieldIdx = 0
	case actionRemoveAndroid:
		if len(m.state.androidSpec) == 0 {
			m.message = "Android matrix is empty"
			return m, nil
		}
		m.mode = editorSpecs
		m.inputTitle = "Remove Android spec"
		m.inputKind = action
		m.fieldIdx = 0
	case actionProject:
		m.mode = editorFields
		m.inputTitle = "Project settings"
		m.inputKind = action
		m.fieldIdx = 0
		m.fields = []editorField{
			{label: "iOS repo", value: m.state.iosRepo},
			{label: "Android repo", value: m.state.androidRepo},
			{label: "iOS scheme", value: m.state.iosScheme},
			{label: "Gradle task", value: m.state.gradleTask},
		}
	case actionEditEnv:
		m.mode = editorFields
		m.inputTitle = "Edit env (clear a value to remove that key)"
		m.inputKind = action
		m.fieldIdx = 0
		// One field per existing key (alphabetised), plus one trailing
		// "Add new" field where the user can type KEY=VALUE to introduce a
		// fresh entry. Empty existing-value commits a delete; the new-key
		// field parses on save.
		keys := make([]string, 0, len(m.state.env))
		for k := range m.state.env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		fields := make([]editorField, 0, len(keys)+1)
		for _, k := range keys {
			fields = append(fields, editorField{label: k, value: m.state.env[k]})
		}
		fields = append(fields, editorField{label: "Add new (KEY=VALUE)", value: ""})
		m.fields = fields
	case actionAddIOS:
		m.mode = editorFields
		m.inputTitle = "Add iOS simulator"
		m.inputKind = action
		m.fieldIdx = 0
		device := defaultIOSDevice(m.state)
		runtime := defaultIOSRuntime(m.state)
		m.fields = []editorField{
			{label: "Device", value: device, required: true, choices: iosDeviceChoices(device)},
			{label: "Runtime", value: runtime, required: true, choices: iosRuntimeChoices(runtime)},
			{label: "Count", value: "1", required: true, count: true},
		}
	case actionEditIOS:
		if len(m.state.iosSpecs) == 0 {
			m.message = "iOS matrix is empty"
			return m, nil
		}
		m.mode = editorSpecs
		m.inputTitle = "Edit iOS matrix"
		m.inputKind = action
		m.fieldIdx = 0
	case actionAddAndroid:
		m.mode = editorFields
		m.inputTitle = "Add Android emulator"
		m.inputKind = action
		m.fieldIdx = 0
		device := defaultAndroidDevice(m.state)
		image := defaultAndroidImage(m.state)
		m.fields = []editorField{
			{label: "Device profile", value: device, required: true, choices: androidDeviceChoices(device)},
			{label: "System image", value: image, required: true, choices: androidImageChoices(image)},
			{label: "Count", value: "1", required: true, count: true},
		}
	case actionEditAndroid:
		if len(m.state.androidSpec) == 0 {
			m.message = "Android matrix is empty"
			return m, nil
		}
		m.mode = editorSpecs
		m.inputTitle = "Edit Android matrix"
		m.inputKind = action
		m.fieldIdx = 0
	case actionAddPhysical:
		m.mode = editorFields
		m.inputTitle = "Add physical Android devices"
		m.inputKind = action
		m.fieldIdx = 0
		m.fields = []editorField{{label: "Count", value: "1", required: true, count: true}}
	case actionClearIOS:
		m.mode = editorConfirm
		m.confirmTitle = "Clear all iOS simulator specs?"
		m.confirmKind = action
	case actionClearAndroid:
		m.mode = editorConfirm
		m.confirmTitle = "Clear all Android specs?"
		m.confirmKind = action
	}
	return m, nil
}

func (m editorModel) updateSpecs(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	doneIdx := m.specCount()
	switch key.String() {
	case "up", "k":
		if m.fieldIdx > 0 {
			m.fieldIdx--
		}
	case "down", "j":
		if m.fieldIdx < doneIdx {
			m.fieldIdx++
		}
	case "enter":
		if m.fieldIdx == doneIdx {
			m.mode = editorMenu
			m.message = ""
			return m, nil
		}
		m.specIdx = m.fieldIdx
		if isRemoveAction(m.inputKind) {
			m.confirmKind = m.inputKind
			m.confirmTitle = m.removeConfirmTitle()
			m.mode = editorConfirm
			return m, nil
		}
		m.startSpecEdit()
	case "q":
		m.mode = editorMenu
		m.message = ""
	}
	return m, nil
}

func isRemoveAction(action editorAction) bool {
	return action == actionRemoveIOS || action == actionRemoveAndroid
}

func (m editorModel) specCount() int {
	switch m.inputKind {
	case actionEditIOS, actionRemoveIOS:
		return len(m.state.iosSpecs)
	case actionEditAndroid, actionRemoveAndroid:
		return len(m.state.androidSpec)
	default:
		return 0
	}
}

func (m editorModel) removeConfirmTitle() string {
	switch m.inputKind {
	case actionRemoveIOS:
		if m.specIdx < 0 || m.specIdx >= len(m.state.iosSpecs) {
			return "Remove iOS simulator?"
		}
		spec := m.state.iosSpecs[m.specIdx]
		return fmt.Sprintf("Remove iOS simulator %d — %s / %s ×%d?", m.specIdx+1, spec.Device, spec.Runtime, spec.Count)
	case actionRemoveAndroid:
		if m.specIdx < 0 || m.specIdx >= len(m.state.androidSpec) {
			return "Remove Android spec?"
		}
		spec := m.state.androidSpec[m.specIdx]
		if spec.Target == contract.TargetPhysical {
			return fmt.Sprintf("Remove physical Android devices ×%d?", spec.Count)
		}
		return fmt.Sprintf("Remove Android spec %d — %s / %s ×%d?", m.specIdx+1, spec.Device, spec.Image, spec.Count)
	default:
		return "Remove selected?"
	}
}

func (m *editorModel) startSpecEdit() {
	m.mode = editorFields
	m.fieldIdx = 0
	m.message = ""
	switch m.inputKind {
	case actionEditIOS:
		spec := m.state.iosSpecs[m.specIdx]
		m.inputTitle = fmt.Sprintf("Edit iOS simulator %d", m.specIdx+1)
		m.fields = []editorField{
			{label: "Device", value: spec.Device, required: true, choices: iosDeviceChoices(spec.Device)},
			{label: "Runtime", value: spec.Runtime, required: true, choices: iosRuntimeChoices(spec.Runtime)},
			{label: "Count", value: strconv.Itoa(spec.Count), required: true, count: true},
		}
	case actionEditAndroid:
		spec := m.state.androidSpec[m.specIdx]
		if spec.Target == contract.TargetPhysical {
			m.inputTitle = fmt.Sprintf("Edit physical Android devices %d", m.specIdx+1)
			m.fields = []editorField{{label: "Count", value: strconv.Itoa(spec.Count), required: true, count: true}}
			return
		}
		m.inputTitle = fmt.Sprintf("Edit Android emulator %d", m.specIdx+1)
		m.fields = []editorField{
			{label: "Device profile", value: spec.Device, required: true, choices: androidDeviceChoices(spec.Device)},
			{label: "System image", value: spec.Image, required: true, choices: androidImageChoices(spec.Image)},
			{label: "Count", value: strconv.Itoa(spec.Count), required: true, count: true},
		}
	}
}

func (m editorModel) updateFields(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	doneIdx := len(m.fields)
	switch key.String() {
	case "up", "k":
		if m.fieldIdx > 0 {
			m.fieldIdx--
		}
	case "down", "j":
		if m.fieldIdx < doneIdx {
			m.fieldIdx++
		}
	case "enter":
		if m.fieldIdx == doneIdx {
			if err := m.applyInput(); err != nil {
				m.message = err.Error()
				return m, nil
			}
			if isEditMatrixAction(m.inputKind) {
				m.mode = editorSpecs
				m.inputTitle = editMatrixTitle(m.inputKind)
				m.fieldIdx = m.specIdx
			} else {
				m.mode = editorMenu
			}
			m.message = actionDoneMessage(m.inputKind)
			return m, nil
		}
		m.startValueEdit()
	case "q":
		m.backFromFields()
	}
	return m, nil
}

func (m *editorModel) backFromFields() {
	if isEditMatrixAction(m.inputKind) {
		m.mode = editorSpecs
		m.inputTitle = editMatrixTitle(m.inputKind)
		m.fieldIdx = m.specIdx
	} else {
		m.mode = editorMenu
	}
	m.message = ""
}

func (m *editorModel) startValueEdit() {
	if len(m.fields[m.fieldIdx].choices) > 0 {
		m.startChoiceSelect()
		return
	}
	m.startTextEdit()
}

func (m *editorModel) startChoiceSelect() {
	m.selectIdx = len(m.fields[m.fieldIdx].choices)
	for i, choice := range m.fields[m.fieldIdx].choices {
		if choice.value == m.fields[m.fieldIdx].value {
			m.selectIdx = i
			break
		}
	}
	m.mode = editorSelect
	m.message = ""
}

func (m *editorModel) startTextEdit() {
	input := textinput.New()
	input.SetValue(m.fields[m.fieldIdx].value)
	input.CursorEnd()
	input.CharLimit = 0
	input.Width = inputPanelWidth(m.width) - 8
	input.Focus()
	m.edit = input
	m.mode = editorValue
	m.message = ""
}

func (m editorModel) updateSelect(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	field := m.fields[m.fieldIdx]
	customIdx := len(field.choices)
	switch key.String() {
	case "up", "k":
		if m.selectIdx > 0 {
			m.selectIdx--
		}
	case "down", "j":
		if m.selectIdx < customIdx {
			m.selectIdx++
		}
	case "pgup", "b":
		m.selectIdx -= m.selectVisibleRows()
		if m.selectIdx < 0 {
			m.selectIdx = 0
		}
	case "pgdown", "f":
		m.selectIdx += m.selectVisibleRows()
		if m.selectIdx > customIdx {
			m.selectIdx = customIdx
		}
	case "enter":
		if m.selectIdx == customIdx {
			m.startTextEdit()
			return m, nil
		}
		if err := m.applyFieldValue(field.choices[m.selectIdx].value); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.mode = editorFields
		m.message = "updated " + field.label
	case "q":
		m.mode = editorFields
		m.message = "selection cancelled"
	}
	return m, nil
}

func (m editorModel) updateValue(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "enter":
		if err := m.applyFieldValue(m.edit.Value()); err != nil {
			m.message = err.Error()
			return m, nil
		}
		m.mode = editorFields
		m.message = "updated " + m.fields[m.fieldIdx].label
		return m, nil
	default:
		var cmd tea.Cmd
		m.edit, cmd = m.edit.Update(key)
		return m, cmd
	}
}

func (m *editorModel) applyFieldValue(raw string) error {
	value := strings.TrimSpace(raw)
	if err := validateEditorField(m.fields[m.fieldIdx], value); err != nil {
		return err
	}
	m.fields[m.fieldIdx].value = value
	if m.inputKind == actionProject {
		return m.applyInput()
	}
	return nil
}

func validateEditorField(field editorField, value string) error {
	if field.required && value == "" {
		return fmt.Errorf("%s is required", field.label)
	}
	if field.count {
		if _, err := positiveInt(value); err != nil {
			return errors.New("count must be a positive integer")
		}
	}
	return nil
}

func (m *editorModel) applyInput() error {
	for _, field := range m.fields {
		if err := validateEditorField(field, strings.TrimSpace(field.value)); err != nil {
			return err
		}
	}
	switch m.inputKind {
	case actionProject:
		m.state.iosRepo = strings.TrimSpace(m.fields[0].value)
		m.state.androidRepo = strings.TrimSpace(m.fields[1].value)
		m.state.iosScheme = strings.TrimSpace(m.fields[2].value)
		m.state.gradleTask = strings.TrimSpace(m.fields[3].value)
	case actionEditEnv:
		// All fields except the last are existing-key entries; the last is
		// the "Add new (KEY=VALUE)" field. Empty values on existing rows
		// delete the key.
		if m.state.env == nil && len(m.fields) > 1 {
			m.state.env = map[string]string{}
		}
		newFieldIdx := len(m.fields) - 1
		for i := 0; i < newFieldIdx; i++ {
			key := m.fields[i].label
			val := m.fields[i].value
			if strings.TrimSpace(val) == "" {
				delete(m.state.env, key)
			} else {
				m.state.env[key] = val
			}
		}
		raw := strings.TrimSpace(m.fields[newFieldIdx].value)
		if raw != "" {
			idx := strings.Index(raw, "=")
			if idx <= 0 {
				return fmt.Errorf("new env entry %q: expected KEY=VALUE", raw)
			}
			key := strings.TrimSpace(raw[:idx])
			if key == "" {
				return errors.New("new env entry: key must be non-empty")
			}
			if m.state.env == nil {
				m.state.env = map[string]string{}
			}
			m.state.env[key] = raw[idx+1:]
		}
		if len(m.state.env) == 0 {
			m.state.env = nil
		}
	case actionAddIOS:
		count, err := positiveInt(m.fields[2].value)
		if err != nil {
			return err
		}
		m.state.iosSpecs = append(m.state.iosSpecs, contract.IosSpec{
			Device:  strings.TrimSpace(m.fields[0].value),
			Runtime: strings.TrimSpace(m.fields[1].value),
			Count:   count,
		})
	case actionEditIOS:
		count, err := positiveInt(m.fields[2].value)
		if err != nil {
			return err
		}
		if m.specIdx < 0 || m.specIdx >= len(m.state.iosSpecs) {
			return errors.New("selected iOS spec no longer exists")
		}
		m.state.iosSpecs[m.specIdx] = contract.IosSpec{
			Device:  strings.TrimSpace(m.fields[0].value),
			Runtime: strings.TrimSpace(m.fields[1].value),
			Count:   count,
		}
	case actionAddAndroid:
		count, err := positiveInt(m.fields[2].value)
		if err != nil {
			return err
		}
		m.state.androidSpec = append(m.state.androidSpec, contract.AndroidSpec{
			Device: strings.TrimSpace(m.fields[0].value),
			Image:  strings.TrimSpace(m.fields[1].value),
			Count:  count,
			Target: contract.TargetEmulator,
		})
	case actionEditAndroid:
		if m.specIdx < 0 || m.specIdx >= len(m.state.androidSpec) {
			return errors.New("selected Android spec no longer exists")
		}
		countIdx := len(m.fields) - 1
		count, err := positiveInt(m.fields[countIdx].value)
		if err != nil {
			return err
		}
		current := m.state.androidSpec[m.specIdx]
		if current.Target == contract.TargetPhysical {
			current.Count = count
			m.state.androidSpec[m.specIdx] = current
			m.dirty = true
			return nil
		}
		m.state.androidSpec[m.specIdx] = contract.AndroidSpec{
			Device: strings.TrimSpace(m.fields[0].value),
			Image:  strings.TrimSpace(m.fields[1].value),
			Count:  count,
			Target: contract.TargetEmulator,
		}
	case actionAddPhysical:
		count, err := positiveInt(m.fields[0].value)
		if err != nil {
			return err
		}
		m.state.androidSpec = append(m.state.androidSpec, contract.AndroidSpec{
			Device: "physical",
			Image:  "physical",
			Count:  count,
			Target: contract.TargetPhysical,
		})
	}
	m.dirty = true
	return nil
}

func actionDoneMessage(action editorAction) string {
	switch action {
	case actionProject:
		return "updated project settings"
	case actionEditEnv:
		return "updated env"
	case actionAddIOS:
		return "added iOS simulator"
	case actionEditIOS:
		return "updated iOS simulator"
	case actionAddAndroid:
		return "added Android emulator"
	case actionEditAndroid:
		return "updated Android spec"
	case actionAddPhysical:
		return "added physical Android devices"
	default:
		return "updated " + string(action)
	}
}

func doneLabel(action editorAction) string {
	switch action {
	case actionProject:
		return "Back to actions"
	case actionEditEnv:
		return "Save env"
	case actionAddIOS:
		return "Add iOS simulator"
	case actionEditIOS:
		return "Update selected simulator"
	case actionAddAndroid:
		return "Add Android emulator"
	case actionEditAndroid:
		return "Update selected Android spec"
	case actionAddPhysical:
		return "Add physical devices"
	default:
		return "Done"
	}
}

func isEditMatrixAction(action editorAction) bool {
	return action == actionEditIOS || action == actionEditAndroid
}

func editMatrixTitle(action editorAction) string {
	switch action {
	case actionEditIOS:
		return "Edit iOS matrix"
	case actionEditAndroid:
		return "Edit Android matrix"
	default:
		return ""
	}
}

func (m editorModel) updateConfirm(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "y", "Y":
		switch m.confirmKind {
		case actionClearIOS:
			m.state.iosSpecs = nil
			m.dirty = true
			m.mode = editorMenu
			m.message = "cleared iOS matrix"
		case actionClearAndroid:
			m.state.androidSpec = nil
			m.dirty = true
			m.mode = editorMenu
			m.message = "cleared Android matrix"
		case actionRemoveIOS:
			if m.specIdx >= 0 && m.specIdx < len(m.state.iosSpecs) {
				m.state.iosSpecs = append(m.state.iosSpecs[:m.specIdx], m.state.iosSpecs[m.specIdx+1:]...)
				m.dirty = true
				m.message = fmt.Sprintf("removed iOS simulator %d", m.specIdx+1)
			}
			if len(m.state.iosSpecs) == 0 {
				m.mode = editorMenu
				m.inputKind = ""
			} else {
				if m.specIdx >= len(m.state.iosSpecs) {
					m.specIdx = len(m.state.iosSpecs) - 1
				}
				m.fieldIdx = m.specIdx
				m.mode = editorSpecs
			}
		case actionRemoveAndroid:
			if m.specIdx >= 0 && m.specIdx < len(m.state.androidSpec) {
				m.state.androidSpec = append(m.state.androidSpec[:m.specIdx], m.state.androidSpec[m.specIdx+1:]...)
				m.dirty = true
				m.message = fmt.Sprintf("removed Android spec %d", m.specIdx+1)
			}
			if len(m.state.androidSpec) == 0 {
				m.mode = editorMenu
				m.inputKind = ""
			} else {
				if m.specIdx >= len(m.state.androidSpec) {
					m.specIdx = len(m.state.androidSpec) - 1
				}
				m.fieldIdx = m.specIdx
				m.mode = editorSpecs
			}
		}
	case "n", "N", "enter":
		if isRemoveAction(m.confirmKind) {
			m.mode = editorSpecs
			m.message = "remove cancelled"
		} else {
			m.mode = editorMenu
			m.message = "clear cancelled"
		}
	}
	return m, nil
}

func (m editorModel) View() string {
	var b strings.Builder
	b.WriteString(m.headerView())
	b.WriteString("\n\n")
	switch m.mode {
	case editorMenu:
		b.WriteString(m.menuView())
	case editorSpecs:
		b.WriteString(m.specsView())
	case editorFields:
		b.WriteString(m.fieldsView())
	case editorSelect:
		b.WriteString(m.selectView())
	case editorValue:
		b.WriteString(m.valueView())
	case editorConfirm:
		b.WriteString(m.confirmView())
	}
	if m.message != "" {
		b.WriteString("\n" + renderEditorMessage(m.message) + "\n")
	}
	b.WriteString("\n" + editorDimStyle.Render(m.helpText()) + "\n")
	return b.String()
}

func (m editorModel) headerView() string {
	project := filepath.Base(m.configDir)
	if project == "." || project == string(filepath.Separator) {
		project = "project"
	}
	var left strings.Builder
	left.WriteString(editorTitleStyle.Render("simsquad"))
	left.WriteString(" ")
	left.WriteString(editorChipStyle.Render("equip"))
	left.WriteString("  ")
	left.WriteString(editorSelectedStyle.Render(project))
	if m.dirty {
		left.WriteString("  ")
		left.WriteString(editorDirtyStyle.Render("edited"))
	}
	left.WriteString("\n")
	left.WriteString(editorDimStyle.Render(m.configDir))
	return left.String()
}

func (m editorModel) helpText() string {
	switch m.mode {
	case editorMenu:
		if m.activeSubmenu != submenuNone {
			return "up/down or j/k move  enter select  q/esc back"
		}
		return "up/down or j/k move  enter select  s save  q cancel  esc cancel"
	case editorSpecs:
		if m.inputKind == actionRemoveIOS || m.inputKind == actionRemoveAndroid {
			return "up/down or j/k move  enter remove selected  q/esc back"
		}
		return "up/down or j/k move  enter edit selected  q/esc back"
	case editorFields:
		return "up/down or j/k move  enter edit selected  q/esc back"
	case editorSelect:
		return "up/down or j/k move  pgup/pgdown jump  enter choose  q/esc back"
	case editorValue:
		return "enter accept  esc cancel edit"
	case editorConfirm:
		return "y confirm  n/enter cancel  esc back"
	default:
		return ""
	}
}

func (m editorModel) menuView() string {
	actionWidth := 34
	if m.width > 0 && m.width < 88 {
		return m.compactMenuView()
	}
	previewWidth := m.width - actionWidth - 8
	if previewWidth < 48 {
		previewWidth = 48
	}
	actions := editorActivePanelStyle.Width(actionWidth).Render(m.actionsView(actionWidth - 4))
	preview := editorPanelStyle.Width(previewWidth).Render(m.summaryView(previewWidth - 4))
	return lipgloss.JoinHorizontal(lipgloss.Top, actions, "  ", preview)
}

func (m editorModel) compactMenuView() string {
	var b strings.Builder
	b.WriteString(m.summaryView(inputPanelWidth(m.width)))
	b.WriteString("\n")
	b.WriteString(m.actionsView(inputPanelWidth(m.width)))
	return b.String()
}

func (m editorModel) actionsView(width int) string {
	var b strings.Builder
	b.WriteString(renderPanelTitle(m.menuTitle(), m.menuSubtitle(), width))
	b.WriteString("\n")
	currentGroup := ""
	for i, item := range m.currentMenuItems() {
		if item.group != currentGroup {
			if currentGroup != "" {
				b.WriteString("\n")
			}
			b.WriteString(editorSubtleStyle.Render(item.group) + "\n")
			currentGroup = item.group
		}
		line := actionLabel(item)
		selected := i == m.cursor
		if selected {
			line = editorSelectedStyle.Render(truncateDisplay(line, width-3))
		} else {
			line = truncateDisplay(line, width-3)
		}
		b.WriteString(cursorPrefix(selected) + line + "\n")
	}
	return b.String()
}

func actionLabel(item menuItem) string {
	if item.action == actionSave {
		return "Save changes"
	}
	return item.label
}

func (m editorModel) menuTitle() string {
	switch m.activeSubmenu {
	case submenuIOS:
		return "iOS matrix"
	case submenuAndroid:
		return "Android matrix"
	default:
		return "Actions"
	}
}

func (m editorModel) menuSubtitle() string {
	switch m.activeSubmenu {
	case submenuIOS:
		return "add / edit / remove simulators"
	case submenuAndroid:
		return "add / edit / remove specs"
	default:
		return "configure + save"
	}
}

func (m editorModel) summaryView(width int) string {
	var b strings.Builder
	b.WriteString(editorTitleStyle.Render("Current Configuration"))
	b.WriteString(" ")
	b.WriteString(editorSubtleStyle.Render(configHealth(m.state)))
	b.WriteString("\n\n")
	b.WriteString(sectionTitle("Project") + "\n")
	b.WriteString(projectRow("iOS repo", shortPath(m.state.iosRepo), width) + "\n")
	b.WriteString(projectRow("Android repo", shortPath(m.state.androidRepo), width) + "\n")
	b.WriteString(projectRow("iOS scheme", m.state.iosScheme, width) + "\n")
	b.WriteString(projectRow("Gradle task", m.state.gradleTask, width) + "\n\n")
	b.WriteString(sectionTitle("iOS matrix") + "\n")
	if len(m.state.iosSpecs) == 0 {
		b.WriteString("  " + editorDimStyle.Render("No iOS simulators configured") + "\n")
	} else {
		b.WriteString(matrixHeader(width) + "\n")
	}
	for i, spec := range m.state.iosSpecs {
		b.WriteString(iosMatrixRow(i+1, spec, width) + "\n")
	}
	b.WriteString("\n" + sectionTitle("Android matrix") + "\n")
	if len(m.state.androidSpec) == 0 {
		b.WriteString("  " + editorDimStyle.Render("No Android targets configured") + "\n")
	} else {
		b.WriteString(matrixHeader(width) + "\n")
	}
	for i, spec := range m.state.androidSpec {
		b.WriteString(androidMatrixRow(i+1, spec, width) + "\n")
	}
	return b.String()
}

func configHealth(state wizardState) string {
	parts := []string{
		fmt.Sprintf("%d iOS", len(state.iosSpecs)),
		fmt.Sprintf("%d Android", len(state.androidSpec)),
	}
	return strings.Join(parts, "  ")
}

func sectionTitle(title string) string {
	return editorSelectedStyle.Render(title)
}

func projectRow(label, value string, width int) string {
	const labelWidth = 13
	if value == "" {
		value = "not configured"
	}
	available := width - labelWidth - 4
	if available < 16 {
		available = 16
	}
	return "  " + padRight(editorSubtleStyle.Render(label), labelWidth) + " " + truncateDisplay(value, available)
}

func shortPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	base := filepath.Base(path)
	if base == "." || base == string(filepath.Separator) {
		return path
	}
	return base
}

func matrixHeader(width int) string {
	return editorSubtleStyle.Render(truncateDisplay("  #   status   target                    runtime/image                  count", width))
}

func iosMatrixRow(index int, spec contract.IosSpec, width int) string {
	deviceWidth := 24
	runtimeWidth := 18
	if width < 72 {
		deviceWidth = 18
		runtimeWidth = 14
	}
	return truncateDisplay(matrixRow(
		index,
		editorOKStyle.Render("ready"),
		truncateDisplay(spec.Device, deviceWidth),
		deviceWidth,
		truncateDisplay(spec.Runtime, runtimeWidth),
		runtimeWidth,
		spec.Count,
	), width)
}

func androidMatrixRow(index int, spec contract.AndroidSpec, width int) string {
	deviceWidth := 24
	imageWidth := width - 48
	if imageWidth < 18 {
		imageWidth = 18
	}
	if spec.Target == contract.TargetPhysical {
		return truncateDisplay(matrixRow(
			index,
			editorOKStyle.Render("ready"),
			"physical",
			deviceWidth,
			"attached devices",
			imageWidth,
			spec.Count,
		), width)
	}
	status := editorOKStyle.Render("ready")
	if dottedAndroidImageRx.MatchString(spec.Image) {
		status = editorWarnStyle.Render("risk")
	}
	return truncateDisplay(matrixRow(
		index,
		status,
		truncateDisplay(spec.Device, deviceWidth),
		deviceWidth,
		truncateDisplay(spec.Image, imageWidth),
		imageWidth,
		spec.Count,
	), width)
}

func matrixRow(index int, status, target string, targetWidth int, detail string, detailWidth int, count int) string {
	return fmt.Sprintf("  %-3d ", index) +
		padRight(status, 8) + " " +
		padRight(target, targetWidth) + " " +
		padRight(detail, detailWidth) +
		fmt.Sprintf(" x%d", count)
}

func padRight(value string, width int) string {
	padding := width - lipgloss.Width(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func (m editorModel) specsView() string {
	content := m.specsPanelView()
	return editorActivePanelStyle.Width(inputPanelWidth(m.width)).Render(content)
}

func (m editorModel) specsPanelView() string {
	var b strings.Builder
	width := inputPanelWidth(m.width) - 4
	b.WriteString(renderEditorTitle(m.inputTitle, width))
	b.WriteString("\n\n")
	switch m.inputKind {
	case actionEditIOS, actionRemoveIOS:
		for i, spec := range m.state.iosSpecs {
			m.writeSpecLine(&b, i, fmt.Sprintf("%s / %s x%d", spec.Device, spec.Runtime, spec.Count))
		}
	case actionEditAndroid, actionRemoveAndroid:
		for i, spec := range m.state.androidSpec {
			if spec.Target == contract.TargetPhysical {
				m.writeSpecLine(&b, i, fmt.Sprintf("physical x%d", spec.Count))
				continue
			}
			line := fmt.Sprintf("%s / %s x%d", spec.Device, spec.Image, spec.Count)
			if dottedAndroidImageRx.MatchString(spec.Image) {
				line += " " + editorWarnStyle.Render("known avdmanager risk")
			}
			m.writeSpecLine(&b, i, line)
		}
	}
	b.WriteString("\n")
	selected := m.fieldIdx == m.specCount()
	label := "Back"
	if selected {
		label = editorSelectedStyle.Render(label)
	}
	b.WriteString(cursorPrefix(selected) + label + "\n")
	b.WriteString("\n")
	hint := "select a spec, then edit only the fields you need"
	if isRemoveAction(m.inputKind) {
		hint = "select a spec to remove — you'll confirm before it's deleted"
	}
	b.WriteString(editorDimStyle.Render(hint) + "\n")
	return b.String()
}

func (m editorModel) writeSpecLine(b *strings.Builder, idx int, line string) {
	selected := idx == m.fieldIdx
	if selected {
		line = editorSelectedStyle.Render(line)
	}
	fmt.Fprintf(b, "%s%d. %s\n", cursorPrefix(selected), idx+1, line)
}

func (m editorModel) fieldsView() string {
	content := m.fieldsPanelView()
	return editorActivePanelStyle.Width(inputPanelWidth(m.width)).Render(content)
}

func (m editorModel) fieldsPanelView() string {
	var b strings.Builder
	width := inputPanelWidth(m.width) - 4
	b.WriteString(renderEditorTitle(m.inputTitle, width))
	b.WriteString("\n\n")
	for i, field := range m.fields {
		selected := i == m.fieldIdx
		rows := m.fieldRow(field, width-2, selected)
		for j, row := range rows {
			prefix := "  "
			if j == 0 {
				prefix = cursorPrefix(selected)
			}
			b.WriteString(prefix + row + "\n")
		}
	}
	b.WriteString("\n")
	doneSelected := m.fieldIdx == len(m.fields)
	label := doneLabel(m.inputKind)
	if doneSelected {
		label = editorSelectedStyle.Render(label)
	}
	b.WriteString(cursorPrefix(doneSelected) + label + "\n")
	b.WriteString("\n")
	b.WriteString(editorDimStyle.Render("choose one field, edit it, return here") + "\n")
	return b.String()
}

// fieldRow returns one or more visual rows for a single field. Long values
// (typically paths) wrap onto continuation lines aligned under the value
// column instead of being truncated, so the user can see the whole value.
func (m editorModel) fieldRow(field editorField, width int, selected bool) []string {
	labelWidth := 14
	if width < 58 {
		labelWidth = 11
	}
	valueWidth := width - labelWidth - 2
	if valueWidth < 16 {
		valueWidth = 16
	}
	labelStyle := editorSubtleStyle
	if selected {
		labelStyle = editorSelectedStyle
	}
	label := padRight(labelStyle.Render(field.label), labelWidth)

	var value string
	if strings.TrimSpace(field.value) == "" {
		value = editorPlaceholderStyle.Render("not configured")
	} else {
		value = editorValueStyle.Render(displayPath(field.value))
	}

	lines := softWrap(value, valueWidth)
	if len(lines) == 0 {
		lines = []string{""}
	}

	out := make([]string, 0, len(lines))
	out = append(out, label+" "+lines[0])
	if len(lines) == 1 {
		return out
	}
	pad := strings.Repeat(" ", labelWidth+1)
	for _, more := range lines[1:] {
		out = append(out, pad+more)
	}
	return out
}

func (m editorModel) selectView() string {
	content := m.selectPanelView()
	return editorActivePanelStyle.Width(inputPanelWidth(m.width)).Render(content)
}

func (m editorModel) selectPanelView() string {
	var b strings.Builder
	field := m.fields[m.fieldIdx]
	rows := m.selectVisibleRows()
	start, end := selectionWindow(len(field.choices), m.selectIdx, rows)
	width := inputPanelWidth(m.width) - 4
	lineWidth := width - 4
	b.WriteString(renderEditorTitle(field.label, width))
	b.WriteString("\n\n")
	if start > 0 {
		b.WriteString("  " + editorDimStyle.Render(fmt.Sprintf("... %d more above", start)) + "\n")
	}
	for i := start; i < end; i++ {
		choice := field.choices[i]
		line := choice.label
		if choice.value == field.value {
			line += " current"
		}
		line = truncateDisplay(line, lineWidth)
		selected := i == m.selectIdx
		switch {
		case selected:
			line = editorSelectedStyle.Render(line)
		case choice.value == field.value:
			line = editorDimStyle.Render(line)
		}
		b.WriteString(cursorPrefix(selected) + line + "\n")
	}
	if end < len(field.choices) {
		b.WriteString("  " + editorDimStyle.Render(fmt.Sprintf("... %d more below", len(field.choices)-end)) + "\n")
	}
	b.WriteString("\n")
	selected := m.selectIdx == len(field.choices)
	label := "Custom value..."
	if selected {
		label = editorSelectedStyle.Render(label)
	}
	b.WriteString(cursorPrefix(selected) + label + "\n")
	return b.String()
}

func (m editorModel) selectVisibleRows() int {
	rows := 12
	if m.height > 0 {
		rows = m.height - 13
	}
	if rows < 5 {
		return 5
	}
	if rows > 12 {
		return 12
	}
	return rows
}

func selectionWindow(total, selected, rows int) (int, int) {
	if total <= 0 {
		return 0, 0
	}
	if rows >= total {
		return 0, total
	}
	if selected >= total {
		selected = total - 1
	}
	start := selected - rows/2
	if start < 0 {
		start = 0
	}
	if start+rows > total {
		start = total - rows
	}
	return start, start + rows
}

func truncateDisplay(value string, maxWidth int) string {
	if maxWidth < 4 || lipgloss.Width(value) <= maxWidth {
		return value
	}
	var b strings.Builder
	for _, r := range value {
		next := b.String() + string(r)
		if lipgloss.Width(next+"...") > maxWidth {
			break
		}
		b.WriteRune(r)
	}
	return b.String() + "..."
}

func (m editorModel) valueView() string {
	width := inputPanelWidth(m.width) - 4
	var b strings.Builder
	b.WriteString(renderEditorTitle(m.inputTitle, width))
	b.WriteString("\n\n")
	b.WriteString(editorSubtleStyle.Render(m.fields[m.fieldIdx].label) + "\n")
	b.WriteString(m.edit.View() + "\n")
	return editorActivePanelStyle.Width(inputPanelWidth(m.width)).Render(b.String())
}

func (m editorModel) confirmView() string {
	width := inputPanelWidth(m.width) - 4
	content := renderEditorTitle(m.confirmTitle, width) +
		"\n\n" +
		editorWarnStyle.Render("y") + " yes   " + editorDimStyle.Render("n") + " no\n"
	return editorActivePanelStyle.Width(inputPanelWidth(m.width)).Render(content)
}

// inputPanelWidth grows with the terminal so long values (notably absolute
// repo paths) fit without truncation. A small margin keeps the rounded border
// from kissing the terminal edge.
func inputPanelWidth(width int) int {
	if width <= 0 {
		return 96
	}
	const margin = 4
	const minWidth = 56
	if width-margin < minWidth {
		if width < minWidth {
			return width
		}
		return minWidth
	}
	return width - margin
}

// cursorPrefix returns a two-cell prefix: a coloured left bar when selected,
// blank otherwise. Keeping the width constant means rows below the cursor stay
// aligned with rows above without re-rendering the entire row background.
func cursorPrefix(selected bool) string {
	if selected {
		return editorCursorBarStyle.Render("▌") + " "
	}
	return "  "
}

// renderEditorTitle draws the modal title followed by a thin separator across
// the panel. The separator gives titles enough hierarchy to read at a glance
// without resorting to a heavy background fill.
func renderEditorTitle(title string, width int) string {
	titleLine := editorTitleAccent.Render(title)
	if width < 4 {
		return titleLine
	}
	sep := editorSeparatorStyle.Render(strings.Repeat("─", width))
	return titleLine + "\n" + sep
}

// renderPanelTitle is the inline header used by the side-by-side menu and
// summary panels — title plus a dim subtitle, no separator line because the
// panel border already supplies the boundary.
func renderPanelTitle(title, subtitle string, width int) string {
	head := editorTitleAccent.Render(title)
	if subtitle != "" {
		head += " " + editorSubtleStyle.Render(subtitle)
	}
	if width < 4 {
		return head
	}
	return head
}

// displayPath rewrites $HOME prefixes to '~' so absolute repo paths read
// naturally in the matrix. The stored value is unchanged; this only affects
// presentation.
func displayPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || !strings.HasPrefix(value, "/") {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return value
	}
	if value == home {
		return "~"
	}
	if strings.HasPrefix(value, home+"/") {
		return "~" + strings.TrimPrefix(value, home)
	}
	return value
}

// softWrap breaks `value` at the given visual width, preferring to break at
// path separators ('/') so wrapped paths stay readable. Callers must ensure
// `value` has no embedded newlines.
func softWrap(value string, width int) []string {
	if width <= 0 {
		return []string{value}
	}
	if lipgloss.Width(value) <= width {
		return []string{value}
	}
	// Wrapping ANSI-coloured strings reliably is hard; fall back to plain text
	// for the wrap calculation and re-render in the caller's style by handing
	// back the plain segments. Since fieldRow is the only caller and uses a
	// single style across the value, the visual loss is the value colour on
	// continuation lines — acceptable trade-off vs. truncation.
	plain := stripANSI(value)
	if lipgloss.Width(plain) <= width {
		return []string{value}
	}
	var lines []string
	var current strings.Builder
	flush := func() {
		if current.Len() > 0 {
			lines = append(lines, editorValueStyle.Render(current.String()))
			current.Reset()
		}
	}
	for _, r := range plain {
		if current.Len() > 0 && current.Len()+1 > width {
			flush()
		}
		current.WriteRune(r)
		if r == '/' && current.Len() >= width-4 {
			flush()
		}
	}
	flush()
	if len(lines) == 0 {
		return []string{value}
	}
	return lines
}

// stripANSI removes lipgloss escape sequences for wrap calculations. The
// implementation is intentionally minimal — it only handles the SGR sequences
// lipgloss emits.
func stripANSI(value string) string {
	var b strings.Builder
	b.Grow(len(value))
	i := 0
	for i < len(value) {
		if value[i] == 0x1b && i+1 < len(value) && value[i+1] == '[' {
			j := i + 2
			for j < len(value) && value[j] != 'm' {
				j++
			}
			if j < len(value) {
				i = j + 1
				continue
			}
		}
		b.WriteByte(value[i])
		i++
	}
	return b.String()
}

func renderEditorMessage(message string) string {
	if strings.HasPrefix(message, "updated") || strings.HasPrefix(message, "added") || strings.HasPrefix(message, "cleared") {
		return editorOKStyle.Render(message)
	}
	return editorWarnStyle.Render(message)
}

func positiveInt(value string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 {
		return 0, errors.New("must be a positive integer")
	}
	return n, nil
}

func iosDeviceChoices(current string) []editorChoice {
	devices := discover.ListIosDeviceTypes()
	choices := make([]editorChoice, 0, len(devices))
	for _, device := range devices {
		choices = append(choices, editorChoice{label: device.Name, value: device.Name})
	}
	return withCurrentChoice(choices, current)
}

func iosRuntimeChoices(current string) []editorChoice {
	runtimes := discover.ListIosRuntimes()
	choices := make([]editorChoice, 0, len(runtimes))
	for _, runtime := range runtimes {
		label := runtime.Name
		if !runtime.Available {
			label += " (unavailable)"
		}
		choices = append(choices, editorChoice{label: label, value: runtime.Name})
	}
	return withCurrentChoice(choices, current)
}

func androidDeviceChoices(current string) []editorChoice {
	profiles := discover.ListAVDDeviceProfiles()
	sort.Strings(profiles)
	choices := make([]editorChoice, 0, len(profiles))
	for _, profile := range profiles {
		choices = append(choices, editorChoice{label: profile, value: profile})
	}
	return withCurrentChoice(choices, current)
}

func androidImageChoices(current string) []editorChoice {
	images := preferredAndroidImages(discover.ListInstalledAndroidImages())
	choices := make([]editorChoice, 0, len(images))
	for _, image := range images {
		label := image.Identifier
		if dottedAndroidImageRx.MatchString(image.Identifier) {
			label += " (known avdmanager risk)"
		}
		choices = append(choices, editorChoice{label: label, value: image.Identifier})
	}
	return withCurrentChoice(choices, current)
}

func withCurrentChoice(choices []editorChoice, current string) []editorChoice {
	current = strings.TrimSpace(current)
	if current == "" {
		return choices
	}
	for _, choice := range choices {
		if choice.value == current {
			return choices
		}
	}
	return append([]editorChoice{{label: current + " (current)", value: current}}, choices...)
}

func defaultIOSDevice(state wizardState) string {
	if len(state.iosSpecs) > 0 {
		return state.iosSpecs[len(state.iosSpecs)-1].Device
	}
	return "iPhone 17"
}

func defaultIOSRuntime(state wizardState) string {
	if len(state.iosSpecs) > 0 {
		return state.iosSpecs[len(state.iosSpecs)-1].Runtime
	}
	return "iOS 26.4"
}

func defaultAndroidDevice(state wizardState) string {
	for i := len(state.androidSpec) - 1; i >= 0; i-- {
		if state.androidSpec[i].Target == contract.TargetEmulator {
			return state.androidSpec[i].Device
		}
	}
	return "pixel_7"
}

func defaultAndroidImage(state wizardState) string {
	for i := len(state.androidSpec) - 1; i >= 0; i-- {
		if state.androidSpec[i].Target == contract.TargetEmulator {
			return state.androidSpec[i].Image
		}
	}
	return "system-images;android-34;google_apis;arm64-v8a"
}

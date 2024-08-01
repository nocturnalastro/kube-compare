package editpatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/reflow/wordwrap"
	"github.com/openshift/kube-compare/pkg/compare"
	"golang.org/x/term"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

const indent = "    "

type data struct {
	loaded   []*compare.UserOverride
	accepted []*compare.UserOverride
	index    int
	diff     Diff
}

func (d *data) acceptCurrent() {
	d.accepted = append(d.accepted, d.getCurrentPatch())
}

func (d *data) resetPatch() {
	d.diff.ClearPatch()
}
func (d *data) getCurrentPatch() *compare.UserOverride {
	return d.loaded[d.index]
}

func (d *data) getPatchValue() string {
	return d.getCurrentPatch().Patch
}

func (d data) ViewPatch() string {
	var prettyJSON bytes.Buffer
	json.Indent(&prettyJSON, []byte(d.getPatchValue()), "", "    ")

	lines := strings.Split(prettyJSON.String(), "\n")

	var b strings.Builder
	patchedString := ""
	if d.diff.IsModified() {
		patchedString = " (modified) "
	}
	b.WriteString(fmt.Sprintf("========== Patch%s=======\n", patchedString))

	railFormat := "%" + fmt.Sprintf("-%dd| ", len(fmt.Sprint(len(lines))))

	for i, s := range lines {
		b.WriteString(fmt.Sprintf(railFormat, i))
		b.WriteString(fmt.Sprintln(s))
	}
	return b.String()
}

func (d data) ViewDiff() string {
	var b strings.Builder
	patchedString := "unpatched"
	if d.diff.IsModified() {
		patchedString = "patched"
	}
	b.WriteString(fmt.Sprintf("========== Diff (%s) =======\n", patchedString))
	diffOutput, err := d.diff.Run()
	if err != nil {
		b.WriteString(fmt.Sprintf("Failed to compute diff: %s\n", err))
	}
	if diffOutput.Len() == 0 {
		b.WriteString("<Nothing>\n")
	} else {
		b.Write(diffOutput.Bytes())
	}
	return b.String()

}

type model struct {
	data          data
	savePath      string
	editPatchArea textarea.Model
	err           error
	showPatch     bool
	width         int
	height        int
}

func initialModel(inputPath, savePath string) (model, error) {
	loaded, err := compare.LoadUserOverrides(inputPath)
	if err != nil {
		return model{}, fmt.Errorf("%w", err)
	}
	ti := textarea.New()
	ti.ShowLineNumbers = true

	m := model{
		data: data{
			loaded:   loaded,
			accepted: []*compare.UserOverride{},
		},
		savePath:      savePath,
		showPatch:     true,
		editPatchArea: ti,
	}
	err = m.updateDiff()
	if err != nil {
		return model{}, err
	}
	return m, nil
}

func (m model) Init() tea.Cmd {
	// TODO remove thing once next version is released
	w, h, err := term.GetSize(int(os.Stdin.Fd()))
	if err != nil {
		return nil
	}
	return func() tea.Msg {
		return tea.WindowSizeMsg{
			Width:  w,
			Height: h,
		}
	}

}

func (m model) saveAndExit() (tea.Model, tea.Cmd) {
	f, err := os.Create(m.savePath)
	if err != nil {
		m.err = err
		return m, nil
	}
	compare.DumpOverrides(m.data.accepted, f)
	return m, tea.Quit
}

func (m *model) clearError() {
	m.err = nil
}

func (m *model) updateDiff() error {
	current := m.data.getCurrentPatch()
	clusterValue := make(map[string]any)
	err := json.Unmarshal([]byte(current.ClusterValue), &clusterValue)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	referenceValue := make(map[string]any)
	err = json.Unmarshal([]byte(current.ReferenceValue), &referenceValue)
	if err != nil {
		return fmt.Errorf("%w", err)
	}

	patch := current.Clone()

	m.data.diff = Diff{
		clusterValue:   &unstructured.Unstructured{Object: clusterValue},
		referenceValue: &unstructured.Unstructured{Object: referenceValue},
		name:           current.Name,
		patchOrigonal:  &patch,
		IOStreams:      genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr},
	}
	m.data.diff.Run()
	return nil
}

func (m *model) setIndex(index int) (tea.Model, tea.Cmd) {
	if index >= len(m.data.loaded) {
		return m.saveAndExit()
	}
	m.data.index = index

	for m.updateDiff() != nil {
		index += 1
		if index >= len(m.data.loaded) {
			return m.saveAndExit()
		}
		m.data.index = index
	}

	return m, nil
}

const (
	AcceptKey          = "a"
	RejectKey          = "d"
	QuitKey            = "q"
	ToggleShowPatchKey = "p"
	EditPatchKey       = "e"
	ResetPatchKey      = "r"
	saveAndExitKey     = "x"
)

func (m model) UpdateEditMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { // nolint

	case tea.KeyMsg:
		switch msg.Type { // nolint
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			err := m.data.diff.UpdatePatch(m.editPatchArea.Value())
			if err != nil {
				m.err = err
			} else {
				m.editPatchArea.Blur()
			}
		case tea.KeyCtrlR:
			m.clearError()
			m.data.resetPatch()
		}
	}
	var cmd tea.Cmd
	initalValue := m.editPatchArea.Value()
	m.editPatchArea, cmd = m.editPatchArea.Update(msg)
	if initalValue != m.editPatchArea.Value() {
		m.data.diff.UpdatePatch(m.editPatchArea.Value())
	}
	return m, cmd
}

func (m model) UdpateNormalMode(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { // nolint
	// Is it a key press?
	case tea.KeyMsg:
		// Cool, what was the actual key pressed?
		switch msg.String() {

		// These keys should exit the program.
		case "ctrl+c", QuitKey:
			return m, tea.Quit

		// Accept current patch
		case AcceptKey:
			m.clearError()
			m.data.acceptCurrent()
			return m.setIndex(m.data.index + 1)
		// Drop patch current patch
		case RejectKey:
			m.clearError()
			return m.setIndex(m.data.index + 1)
		case ToggleShowPatchKey:
			m.showPatch = !m.showPatch
			return m, nil
		case ResetPatchKey:
			m.clearError()
			m.data.resetPatch()
		// modify patch
		case EditPatchKey:
			var prettyJSON bytes.Buffer
			json.Indent(&prettyJSON, []byte(m.data.getPatchValue()), "", indent)
			m.editPatchArea.SetValue(prettyJSON.String())

			lines := strings.Split(prettyJSON.String(), "\n")
			m.editPatchArea.SetHeight(len(lines))
			m.editPatchArea.SetWidth(m.width - 10)

			return m, tea.Batch(
				m.editPatchArea.Focus(),
				textarea.Blink,
			)
		// Save and exit
		case saveAndExitKey:
			m.clearError()
			return m.saveAndExit()
		}
	}

	return m, nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { // nolint
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.editPatchArea.Focused() {
			m.editPatchArea.SetWidth(msg.Width - 10)
		}
		return m, tea.ClearScreen
	}

	if m.editPatchArea.Focused() {
		return m.UpdateEditMode(msg)
	}
	return m.UdpateNormalMode(msg)
}

func editModeHelp() string {
	return strings.Join(
		[]string{
			"esc) Submit patch",
			"ctrl+r) Reset Patch",
			"ctrl+c) quit",
		},
		" ",
	)
}

func normalModeHelp() string {
	return strings.Join(
		[]string{
			fmt.Sprintf("%s) quit", QuitKey),
			fmt.Sprintf("%s) accept patch", AcceptKey),
			fmt.Sprintf("%s) drop patch", RejectKey),
			fmt.Sprintf("%s) save and exit", saveAndExitKey),
			fmt.Sprintf("%s) toggle patch visbility", ToggleShowPatchKey),
			fmt.Sprintf("%s) edit", EditPatchKey),
			fmt.Sprintf("%s) reset patch", ResetPatchKey),
		},
		" ",
	)
}

var boxedStyle = lipgloss.NewStyle().BorderStyle(lipgloss.RoundedBorder()).Padding(0, 1).Render

func (m model) View() string {
	parts := make([]string, 0)

	parts = append(parts,
		boxedStyle("Do you want to keep this patch?"),
		boxedStyle(m.data.ViewDiff()),
	)

	if m.showPatch && !m.editPatchArea.Focused() {
		parts = append(parts, boxedStyle(m.data.ViewPatch()))
	}
	if m.editPatchArea.Focused() {
		parts = append(parts, boxedStyle(m.editPatchArea.View()))
	}

	if m.err != nil {
		parts = append(parts, boxedStyle(fmt.Sprintf("Error: %s\n", m.err)))
	}

	if !m.editPatchArea.Focused() {
		parts = append(parts, boxedStyle(normalModeHelp()))
	} else {
		parts = append(parts, boxedStyle(editModeHelp()))
	}
	return wordwrap.String(strings.Join(parts, "\n"), m.width)
}

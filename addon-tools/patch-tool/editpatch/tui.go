package editpatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/reflow/wordwrap"
	"github.com/openshift/kube-compare/pkg/compare"
	"golang.org/x/term"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/genericiooptions"
)

const indent = "    "

type model struct {
	diffs         []*compare.UserOverride
	currentIndex  int
	acceptedDiffs []*compare.UserOverride
	savePath      string
	editPatchArea textarea.Model
	err           error
	showPatch     bool
	diff          Diff
	width         int
	height        int
}

func initialModel(inputPath, savePath string) (model, error) {
	diffs, err := compare.LoadUserOverrides(inputPath)
	if err != nil {
		return model{}, fmt.Errorf("%w", err)
	}
	ti := textarea.New()
	ti.ShowLineNumbers = true

	m := model{
		diffs:         diffs,
		savePath:      savePath,
		acceptedDiffs: []*compare.UserOverride{},
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
	compare.DumpOverrides(m.acceptedDiffs, f)
	return m, tea.Quit
}

func (m *model) clearError() {
	m.err = nil
}

func (m *model) updateDiff() error {
	current := m.getCurrent()
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

	m.diff = Diff{
		clusterValue:   &unstructured.Unstructured{Object: clusterValue},
		referenceValue: &unstructured.Unstructured{Object: referenceValue},
		name:           current.Name,
		patchOrigonal:  &patch,
		IOStreams:      genericiooptions.IOStreams{In: os.Stdin, Out: os.Stdout, ErrOut: os.Stderr},
	}
	m.diff.Run()
	return nil
}

func (m *model) setIndex(index int) (tea.Model, tea.Cmd) {
	if index >= len(m.diffs) {
		return m.saveAndExit()
	}
	m.currentIndex = index

	for m.updateDiff() != nil {
		index += 1
		if index >= len(m.diffs) {
			return m.saveAndExit()
		}
		m.currentIndex = index
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) { // nolint
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		if m.editPatchArea.Focused() {
			m.editPatchArea.SetWidth(msg.Width)
		}
		return m, tea.ClearScreen
	}

	if m.editPatchArea.Focused() {
		switch msg := msg.(type) { // nolint

		case tea.KeyMsg:
			switch msg.Type { // nolint
			case tea.KeyCtrlC:
				return m, tea.Quit
			case tea.KeyEsc:
				if m.editPatchArea.Focused() {
					err := m.diff.UpdatePatch(m.editPatchArea.Value())
					if err != nil {
						m.err = err
					} else {
						m.editPatchArea.Blur()
					}
				}
			}

		}
		var cmd tea.Cmd
		initalValue := m.editPatchArea.Value()
		m.editPatchArea, cmd = m.editPatchArea.Update(msg)
		if initalValue != m.editPatchArea.Value() {
			m.diff.UpdatePatch(m.editPatchArea.Value())
		}
		return m, cmd
	}

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
			m.acceptedDiffs = append(m.acceptedDiffs, m.diffs[m.currentIndex])
			return m.setIndex(m.currentIndex + 1)
		// Drop patch current patch
		case RejectKey:
			m.clearError()
			return m.setIndex(m.currentIndex + 1)
		case ToggleShowPatchKey:
			m.showPatch = !m.showPatch
			return m, nil
		case ResetPatchKey:
			m.clearError()
			m.diff.ClearPatch()
		// modify patch
		case EditPatchKey:
			var prettyJSON bytes.Buffer

			json.Indent(&prettyJSON, []byte(m.diff.GetPatch().Patch), "", indent)
			m.editPatchArea.SetValue(prettyJSON.String())
			m.editPatchArea.SetCursor(0)
			lines := strings.Split(prettyJSON.String(), "\n")
			m.editPatchArea.SetHeight(len(lines))
			m.editPatchArea.SetWidth(m.width)

			return m, tea.Batch(
				m.editPatchArea.Focus(),
				textarea.Blink,
			)
		// Save and exit
		case "x":
			m.clearError()
			return m.saveAndExit()
		default:
			return m, nil
		}
	}

	// Return the updated model to the Bubble Tea runtime for processing.
	// Note that we're not returning a command.
	return m, nil
}

func displayPatch(m model) string {
	var prettyJSON bytes.Buffer
	json.Indent(&prettyJSON, []byte(m.diff.GetPatch().Patch), "", "    ")

	lines := strings.Split(prettyJSON.String(), "\n")

	var b strings.Builder
	patchedString := ""
	if m.diff.patch != nil {
		patchedString = " (modified) "
	}
	b.WriteString(fmt.Sprintf("========== Patch %s=======\n", patchedString))

	railFormat := "%" + fmt.Sprintf("-%dd| ", len(fmt.Sprint(len(lines))))

	for i, s := range lines {
		b.WriteString(fmt.Sprintf(railFormat, i))
		b.WriteString(fmt.Sprintln(s))
	}
	return b.String()
}

func displayDiff(m model) string {
	var b strings.Builder
	patchedString := "unpatched"
	if m.diff.patch != nil {
		patchedString = "patched"
	}
	b.WriteString(fmt.Sprintf("========== Diff (%s) =======\n", patchedString))
	diffOutput, err := m.diff.Run()
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

func (m model) getCurrent() *compare.UserOverride {
	return m.diffs[m.currentIndex]
}

func (m model) View() string {
	// The header
	s := "Do you want to keep this patch?\n"
	s += displayDiff(m)
	if m.showPatch && !m.editPatchArea.Focused() {
		s += displayPatch(m)
	}
	if m.editPatchArea.Focused() {
		s += fmt.Sprintf("\n%s\n", m.editPatchArea.View())
	}

	// // The footer
	if m.err != nil {
		s += fmt.Sprintf("Error: %s\n", m.err)
	}
	if !m.editPatchArea.Focused() {
		s += "\n" + strings.Join(
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
	} else {
		s += "\n" + strings.Join(
			[]string{
				"esc) Submit patch",
				"ctrl+c) quit",
			},
			" ",
		)
		s += "\nesc) submit patch r) reset ctrl+c) quit"
	}
	return wordwrap.String(s, m.width)
}

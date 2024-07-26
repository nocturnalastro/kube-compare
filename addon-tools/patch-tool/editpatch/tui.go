package editpatch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/openshift/kube-compare/pkg/compare"
)

type model struct {
	diffs         []*compare.UserOverride
	currentIndex  int
	acceptedDiffs []*compare.UserOverride
	savePath      string
	err           error
	showPatch     bool
}

func initialModel(inputPath, savePath string) (model, error) {
	diffs, err := compare.LoadUserOverrides(inputPath)
	if err != nil {
		return model{}, err
	}

	return model{diffs: diffs, savePath: savePath, acceptedDiffs: []*compare.UserOverride{}, showPatch: true}, nil
}

func (m model) Init() tea.Cmd {
	// Just return `nil`, which means "no I/O right now, please."
	return nil
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

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {

	switch msg := msg.(type) {

	// Is it a key press?
	case tea.KeyMsg:

		// Cool, what was the actual key pressed?
		switch msg.String() {

		// These keys should exit the program.
		case "ctrl+c", "q":
			return m, tea.Quit

		// Accept current patch
		case "a":
			m.clearError()
			m.acceptedDiffs = append(m.acceptedDiffs, m.diffs[m.currentIndex])
			if m.currentIndex+1 >= len(m.diffs) {
				return m.saveAndExit()
			}
			m.currentIndex += 1
			return m, nil
		// Drop patch current patch
		case "d":
			m.clearError()
			if m.currentIndex+1 >= len(m.diffs) {
				return m.saveAndExit()
			}
			m.currentIndex += 1
			return m, nil

		case "p":
			m.showPatch = !m.showPatch
			return m, nil

		// // split patch
		// case "s":

		// Save and exit
		case "e":
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

func displayPatch(current *compare.UserOverride) string {

	var prettyJSON bytes.Buffer
	json.Indent(&prettyJSON, []byte(current.Patch), "", "\t")

	lines := strings.Split(prettyJSON.String(), "\n")

	var b strings.Builder
	railFormat := "%" + fmt.Sprintf("-%dd| ", len(fmt.Sprint(len(lines))))

	for i, s := range lines {
		b.WriteString(fmt.Sprintf(railFormat, i))
		b.WriteString(fmt.Sprintln(s))
	}
	return b.String()
}

func (m model) View() string {
	// The header
	s := "Do you want to keep this patch?\n"

	current := m.diffs[m.currentIndex]

	if current.DiffOuput != "" {
		// Display diff output for context
		s += "Diff Output:\n"
		s += current.DiffOuput
	}

	if m.showPatch && current.DiffOuput != "" {
		s += "\n\n"
	}

	if m.showPatch || current.DiffOuput == "" {
		s += displayPatch(current)
	}

	// // The footer
	if m.err != nil {
		s += fmt.Sprintf("Error: %s\n", m.err)
	}
	s += "\nq) quit a) accept d) drop e) save and exit p) toggle patch \n"

	return s
}

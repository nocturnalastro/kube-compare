package editpatch

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/spf13/cobra"
)

type Options struct {
	patchFile  string
	outputFile string
}

func NewCmd() *cobra.Command {
	options := Options{}
	cmd := &cobra.Command{
		Use:   "edit-patch -j <patch file>",
		Short: "edit-patch: A CLI tool modifing user override files",
		Long:  "edit-patch: A CLI tool modifing user override files",
		RunE: func(cmd *cobra.Command, args []string) error {
			if options.outputFile == "" {
				options.outputFile = options.patchFile
			}
			m, err := initialModel(options.patchFile, options.outputFile)
			if err != nil {
				return err
			}
			p := tea.NewProgram(m)
			if _, err := p.Run(); err != nil {
				fmt.Printf("Alas, there's been an error: %v", err)
				os.Exit(1)
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&options.patchFile, "patch-file", "f", "", "Path to patch-file")
	cmd.Flags().StringVarP(&options.outputFile, "output", "o", "", "Path to save edited patch-file by default will overwrite")
	return cmd
}

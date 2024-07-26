package main

import (
	"os"

	"github.com/openshift/kube-compare/addon-tools/patch-tool/editpatch"
)

func main() {
	patchCmd := editpatch.NewCmd()
	if err := patchCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

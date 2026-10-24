package llm

import (
	"os"

	"antennae.space/ide/lib/i18n"
)

// The messages of the package are in the catalog of the AI plugin, added by its package in a pod.
func init() { i18n.Add(os.DirFS("../i18n")) }

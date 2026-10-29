// Command tofi-settings-registry writes the files generated from the settings
// registry: ui/src/generated/settingsRegistry.json and the reference pages of
// the product manual under internal/manual/generated. Run it with
// `go generate ./internal/app` (the working directory is internal/app).
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/JackZhao98/tofibot/internal/app"
)

func main() {
	root := "."
	if len(os.Args) > 1 {
		root = os.Args[1]
	} else if wd, err := os.Getwd(); err == nil && filepath.Base(wd) == "app" {
		root = filepath.Join(wd, "..", "..")
	}
	if err := app.WriteSettingsRegistryArtifacts(root); err != nil {
		fmt.Fprintln(os.Stderr, "tofi-settings-registry:", err)
		os.Exit(1)
	}
}

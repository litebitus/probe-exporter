// Command schemaassets writes the schema files a release attaches into the folder it is given: every minor, and each major as its newest minor.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/litebitus/probe-exporter/schemas"
)

func main() {
	if err := writeAssets(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "schemaassets:", err)
		os.Exit(1)
	}
}

func writeAssets(arguments []string) error {
	if len(arguments) != 1 {
		return fmt.Errorf("usage: schemaassets <folder>")
	}
	assets, err := schemas.ReleaseAssets()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(arguments[0], 0o755); err != nil {
		return err
	}
	for name, text := range assets {
		if err := os.WriteFile(filepath.Join(arguments[0], name), text, 0o644); err != nil {
			return err
		}
	}
	return nil
}

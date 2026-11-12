// Command schemagen fills the blackbox_exporter definitions of the configuration's newest minor schema in the folder it is given: blackbox's probers' settings, read from the blackbox_exporter module the exporter builds with. Every other part of the schema is written by hand and kept as it is.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/litebitus/probe-exporter/schemas"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	if len(arguments) != 1 {
		return fmt.Errorf("usage: schemagen <schemas folder>")
	}
	_, fileName := schemas.NewestMinor()
	path := filepath.Join(arguments[0], fileName)
	written, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	filled, err := filledSchema(written)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	return os.WriteFile(path, filled, 0o644)
}

// filledSchema is a schema's text with its blackbox definitions filled from the blackbox_exporter module the exporter builds with.
func filledSchema(written []byte) ([]byte, error) {
	parsed, err := parseOrdered(written)
	if err != nil {
		return nil, err
	}
	schema, isObject := parsed.(*orderedObject)
	if !isObject {
		return nil, fmt.Errorf("the schema is not a JSON object")
	}
	referenceFile, err := blackboxReferenceFile()
	if err != nil {
		return nil, err
	}
	reference, err := readConfigurationReference(referenceFile)
	if err != nil {
		return nil, err
	}
	filled, err := withBlackboxDefinitions(schema, reference)
	if err != nil {
		return nil, err
	}
	return indentedJSON(filled)
}

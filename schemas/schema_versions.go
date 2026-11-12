// Package schemas holds the configuration's JSON Schema, one file per minor version, and the versions a configuration may state.
package schemas

import (
	"embed"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
)

//go:embed probe-exporter-v*.schema.json
var schemaFiles embed.FS

var minorFileName = regexp.MustCompile(`^probe-exporter-v(\d+)\.(\d+)\.schema\.json$`)

// minorSchema is one minor version's schema file.
type minorSchema struct {
	major, minor int
	fileName     string
}

func (schema minorSchema) version() string { return fmt.Sprintf("%d.%d", schema.major, schema.minor) }

// minorSchemas are the schema files the exporter carries, oldest first.
var minorSchemas = mustReadMinorSchemas(schemaFiles)

func mustReadMinorSchemas(files fs.FS) []minorSchema {
	read, err := readMinorSchemas(files)
	if err != nil {
		panic(err)
	}
	return read
}

func readMinorSchemas(files fs.FS) ([]minorSchema, error) {
	entries, err := fs.ReadDir(files, ".")
	if err != nil {
		return nil, fmt.Errorf("failed to list the embedded schemas: %w", err)
	}
	var read []minorSchema
	for _, entry := range entries {
		named := minorFileName.FindStringSubmatch(entry.Name())
		if named == nil {
			return nil, fmt.Errorf("%s is not named probe-exporter-v<major>.<minor>.schema.json", entry.Name())
		}
		major, _ := strconv.Atoi(named[1])
		minor, _ := strconv.Atoi(named[2])
		read = append(read, minorSchema{major: major, minor: minor, fileName: entry.Name()})
	}
	slices.SortFunc(read, func(left, right minorSchema) int {
		if left.major != right.major {
			return left.major - right.major
		}
		return left.minor - right.minor
	})
	return read, nil
}

// NewestMinor is the newest minor version the exporter carries, and its schema file's name.
func NewestMinor() (version, fileName string) {
	newest := minorSchemas[len(minorSchemas)-1]
	return newest.version(), newest.fileName
}

// SchemaFor is the schema a configuration's stated version names, and the minor version it is: a minor names its own schema, and a major alone floats to its newest minor.
func SchemaFor(stated string) (text []byte, minorVersion string, err error) {
	for index := len(minorSchemas) - 1; index >= 0; index-- {
		schema := minorSchemas[index]
		if stated == schema.version() || stated == strconv.Itoa(schema.major) {
			text, err := fs.ReadFile(schemaFiles, schema.fileName)
			return text, schema.version(), err
		}
	}
	return nil, "", fmt.Errorf("%q is not a version this exporter reads", stated)
}

// StatedVersions are the versions a configuration may state: each major, which reads as its newest minor, then each minor.
func StatedVersions() []string {
	var stated []string
	for _, schema := range minorSchemas {
		if major := strconv.Itoa(schema.major); !slices.Contains(stated, major) {
			stated = append(stated, major)
		}
	}
	for _, schema := range minorSchemas {
		stated = append(stated, schema.version())
	}
	return stated
}

// ReleaseAssets are the schema files a release attaches, by asset name: every minor's file, and each major's as its newest minor, for a modeline to name.
func ReleaseAssets() (map[string][]byte, error) {
	assets := map[string][]byte{}
	for _, schema := range minorSchemas {
		text, err := schemaFiles.ReadFile(schema.fileName)
		if err != nil {
			return nil, err
		}
		assets[schema.fileName] = text
		assets[fmt.Sprintf("probe-exporter-v%d.schema.json", schema.major)] = text
	}
	return assets, nil
}

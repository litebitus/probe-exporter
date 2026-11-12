package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// fieldReference is what blackbox's configuration reference says of one field: its comment, and its default where it states one.
type fieldReference struct {
	description  string
	defaultValue string
}

// configurationReference is blackbox's CONFIGURATION.md, read as each section's fields.
type configurationReference map[string]map[string]fieldReference

var (
	sectionHeading  = regexp.MustCompile("^#{3,4} `<([a-z0-9_]+)>`")
	referencedField = regexp.MustCompile(`^\s*(?:-\s*)?(?:\[\s*)*(?:-\s*)?([a-z][a-z0-9_]*):`)
	statedDefault   = regexp.MustCompile(`default\s*[=:]\s*([^\]>]+?)\s*[\]>]`)
	trailingComment = regexp.MustCompile(`\]\s*#\s*(.+)$`)
)

// blackboxReferenceFile is blackbox's CONFIGURATION.md in the module the exporter builds with.
func blackboxReferenceFile() (string, error) {
	output, err := exec.Command("go", "list", "-m", "-f", "{{.Dir}}", "github.com/prometheus/blackbox_exporter").Output()
	if err != nil {
		return "", fmt.Errorf("failed to find the blackbox_exporter module: %w", err)
	}
	return filepath.Join(strings.TrimSpace(string(output)), "CONFIGURATION.md"), nil
}

func readConfigurationReference(path string) (configurationReference, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read blackbox's configuration reference: %w", err)
	}
	defer file.Close()
	reference := configurationReference{}
	var section string
	var inBlock bool
	var comments []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if heading := sectionHeading.FindStringSubmatch(line); heading != nil {
			section, inBlock, comments = heading[1], false, nil
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inBlock, comments = !inBlock, nil
			continue
		}
		if !inBlock || section == "" {
			continue
		}
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			comments = nil
		case strings.HasPrefix(trimmed, "#"):
			if text := strings.TrimSpace(strings.TrimPrefix(trimmed, "#")); text != "" {
				comments = append(comments, text)
			}
		default:
			field := referencedField.FindStringSubmatch(line)
			if field == nil {
				continue
			}
			if reference[section] == nil {
				reference[section] = map[string]fieldReference{}
			}
			if _, documented := reference[section][field[1]]; documented {
				comments = nil
				continue
			}
			read := fieldReference{description: strings.Join(comments, " ")}
			if read.description == "" {
				if trailing := trailingComment.FindStringSubmatch(line); trailing != nil {
					read.description = trailing[1]
				}
			}
			if stated := statedDefault.FindStringSubmatch(line); stated != nil {
				read.defaultValue = strings.Trim(stated[1], `"`)
			}
			reference[section][field[1]] = read
			comments = nil
		}
	}
	return reference, scanner.Err()
}

// lookUp is what the first of the sections that documents a field says of it.
func (reference configurationReference) lookUp(sections []string, field string) (fieldReference, bool) {
	for _, section := range sections {
		if read, documented := reference[section][field]; documented {
			return read, true
		}
	}
	return fieldReference{}, false
}

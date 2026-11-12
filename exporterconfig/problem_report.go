package exporterconfig

import (
	"cmp"
	"errors"
	"fmt"
	"slices"
	"strings"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
	"github.com/litebitus/yaml-dsl-spec/go/diagnostic_renderer"
)

// problemReport renders a configuration's problems as a compiler reports them: each message, where it is in the file, and the lines around it.
type problemReport struct {
	sourceName string
	text       string
}

func (report problemReport) of(problems []yamldslspec.Diagnostic) error {
	ordered := slices.Clone(problems)
	slices.SortStableFunc(ordered, compareProblemPositions)
	source := diagnostic_renderer.DiagnosticSource{Name: report.sourceName, Text: report.text}
	rendered := make([]string, 0, len(ordered)+1)
	for _, problem := range ordered {
		rendered = append(rendered, diagnostic_renderer.RenderDiagnostic(
			problem, diagnostic_renderer.SeverityError, source, diagnostic_renderer.RenderStyle{}))
	}
	problemCount := "1 error"
	if len(ordered) != 1 {
		problemCount = fmt.Sprintf("%d errors", len(ordered))
	}
	return errors.New(strings.Join(append(rendered, problemCount), "\n\n"))
}

func compareProblemPositions(first, second yamldslspec.Diagnostic) int {
	switch {
	case first.Position == nil && second.Position == nil:
		return 0
	case first.Position == nil:
		return -1
	case second.Position == nil:
		return 1
	}
	return cmp.Or(cmp.Compare(first.Position.Line, second.Position.Line), cmp.Compare(first.Position.Column, second.Position.Column))
}

// nodeAt is the node at a path of map keys and list positions under a root, where its key was written, and whether the path is in the file.
func nodeAt(root yamldslspec.Node, path ...any) (yamldslspec.Node, yamldslspec.Position, bool) {
	node, keyPosition := root, root.Position
	for _, step := range path {
		switch key := step.(type) {
		case string:
			index := slices.IndexFunc(node.Entries, func(entry yamldslspec.NodeEntry) bool { return entry.Key == key })
			if node.Kind != yamldslspec.NodeMap || index < 0 {
				return node, keyPosition, false
			}
			node, keyPosition = node.Entries[index].Value, node.Entries[index].KeyPosition
		case int:
			if node.Kind != yamldslspec.NodeList || key >= len(node.Items) {
				return node, keyPosition, false
			}
			node, keyPosition = node.Items[key], node.Items[key].Position
		}
	}
	return node, keyPosition, true
}

// placedAtValue is a problem at the value a path names, or at the nearest place written on the way to it.
func placedAtValue(root yamldslspec.Node, message string, path ...any) yamldslspec.Diagnostic {
	node, _, _ := nodeAt(root, path...)
	return yamldslspec.DiagnosticAt(node.Position, message)
}

// placedAtKey is a problem at the key a path names, or at the nearest place written on the way to it.
func placedAtKey(root yamldslspec.Node, message string, path ...any) yamldslspec.Diagnostic {
	_, keyPosition, _ := nodeAt(root, path...)
	return yamldslspec.DiagnosticAt(keyPosition, message)
}

package exporterconfig

import (
	"slices"
	"testing"

	yamldslspec "github.com/litebitus/yaml-dsl-spec/go"
)

func TestProblemsAreOrderedByWhereTheyAreAndUnplacedOnesFirst(t *testing.T) {
	at := func(line, column int, message string) yamldslspec.Diagnostic {
		return yamldslspec.DiagnosticAt(yamldslspec.Position{Line: line, Column: column}, message)
	}
	problems := []yamldslspec.Diagnostic{
		at(3, 5, "mock-c"), yamldslspec.UnplacedDiagnostic("mock-unplaced-a"), at(1, 9, "mock-b"),
		yamldslspec.UnplacedDiagnostic("mock-unplaced-b"), at(1, 2, "mock-a"),
	}
	slices.SortStableFunc(problems, compareProblemPositions)
	var order []string
	for _, problem := range problems {
		order = append(order, problem.Message)
	}
	if want := []string{"mock-unplaced-a", "mock-unplaced-b", "mock-a", "mock-b", "mock-c"}; !slices.Equal(order, want) {
		t.Errorf("order = %v, want %v", order, want)
	}
}

func TestAProblemOffThePathIsPlacedAtTheNearestPlaceWritten(t *testing.T) {
	root, _ := yamldslspec.ParseYAML(mockSource, "mock_list:\n  - mock_key: mock\nmock_scalar: mock\n", nil)
	for name, testCase := range map[string]struct {
		path   []any
		line   int
		column int
		found  bool
	}{
		"a key in a list item":   {[]any{"mock_list", 0, "mock_key"}, 2, 15, true},
		"a missing key":          {[]any{"mock_missing"}, 1, 1, false},
		"a position past a list": {[]any{"mock_list", 4}, 2, 3, false},
		"a key under a scalar":   {[]any{"mock_scalar", "mock_key"}, 3, 14, false},
		"a position in a map":    {[]any{0}, 1, 1, false},
	} {
		node, _, found := nodeAt(root, testCase.path...)
		if found != testCase.found || node.Position.Line != testCase.line || node.Position.Column != testCase.column {
			t.Errorf("%s: at %d:%d (%v), want %d:%d (%v)", name, node.Position.Line, node.Position.Column, found,
				testCase.line, testCase.column, testCase.found)
		}
	}
	if problem := placedAtKey(root, "mock", "mock_list", 0, "mock_key"); problem.Position.Line != 2 || problem.Position.Column != 5 {
		t.Errorf("placed at %+v", problem.Position)
	}
}

func TestValuesAreWrittenAsQuotedAlternatives(t *testing.T) {
	for want, values := range map[string][]string{
		"":                                nil,
		"`\"1\"`":                         {"1"},
		"`\"1\"` or `\"1.0\"`":            {"1", "1.0"},
		"`\"1\"`, `\"1.0\"` or `\"1.1\"`": {"1", "1.0", "1.1"},
	} {
		if got := quotedAlternatives(values); got != want {
			t.Errorf("%v: %s, want %s", values, got, want)
		}
	}
}

package main

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// blackboxDefinitionPrefix names the definitions the generator owns; every other part of the schema is written by hand and kept as it is.
const blackboxDefinitionPrefix = "blackbox_"

// withBlackboxDefinitions is a schema with its blackbox definitions replaced by those read from blackbox, appended after its own definitions, and every other part kept as written.
func withBlackboxDefinitions(schema *orderedObject, reference configurationReference) (*orderedObject, error) {
	definitions, isObject := schema.values["$defs"].(*orderedObject)
	if !isObject {
		return nil, fmt.Errorf("the schema has no `$defs` object")
	}
	for _, name := range slices.Clone(definitions.keys) {
		if strings.HasPrefix(name, blackboxDefinitionPrefix) {
			definitions.remove(name)
		}
	}
	blackbox := newBlackboxDefinitions(reference)
	for _, prober := range slices.Sorted(maps.Keys(blackboxProbers)) {
		blackbox.definitionReference(blackboxProbers[prober], nil)
	}
	for _, name := range blackbox.definitions.keys {
		definitions.set(name, blackbox.definitions.values[name])
	}
	return schema, nil
}

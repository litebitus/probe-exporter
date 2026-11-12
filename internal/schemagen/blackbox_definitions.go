package main

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/alecthomas/units"
	blackboxconfig "github.com/prometheus/blackbox_exporter/config"
	promconfig "github.com/prometheus/common/config"
)

// blackboxProbers are the module keys of blackbox's probers, each with its settings' type.
var blackboxProbers = map[string]reflect.Type{
	"dns":       reflect.TypeFor[blackboxconfig.DNSProbe](),
	"grpc":      reflect.TypeFor[blackboxconfig.GRPCProbe](),
	"http":      reflect.TypeFor[blackboxconfig.HTTPProbe](),
	"icmp":      reflect.TypeFor[blackboxconfig.ICMPProbe](),
	"tcp":       reflect.TypeFor[blackboxconfig.TCPProbe](),
	"unix":      reflect.TypeFor[blackboxconfig.UnixProbe](),
	"websocket": reflect.TypeFor[blackboxconfig.WebsocketProbe](),
}

// referenceSections are the sections of blackbox's configuration reference documenting a type's fields; a type without one is documented in the section of the type holding it.
var referenceSections = map[reflect.Type]string{
	reflect.TypeFor[blackboxconfig.DNSProbe]():       "dns_probe",
	reflect.TypeFor[blackboxconfig.GRPCProbe]():      "grpc_probe",
	reflect.TypeFor[blackboxconfig.HTTPProbe]():      "http_probe",
	reflect.TypeFor[blackboxconfig.ICMPProbe]():      "icmp_probe",
	reflect.TypeFor[blackboxconfig.TCPProbe]():       "tcp_probe",
	reflect.TypeFor[blackboxconfig.UnixProbe]():      "unix_probe",
	reflect.TypeFor[blackboxconfig.WebsocketProbe](): "websocket_probe",
	reflect.TypeFor[blackboxconfig.HeaderMatch]():    "http_header_match_spec",
	reflect.TypeFor[promconfig.HTTPClientConfig]():   "http_client_config",
	reflect.TypeFor[promconfig.TLSConfig]():          "tls_config",
	reflect.TypeFor[promconfig.OAuth2]():             "oauth2",
}

var durationPattern = `^([0-9]+(\.[0-9]+)?(ns|us|µs|ms|s|m|h))+$`

// scalarTypes are the types blackbox reads from a YAML scalar of their own form.
var scalarTypes = map[reflect.Type]func() *orderedObject{
	reflect.TypeFor[time.Duration]():             func() *orderedObject { return newObject("type", "string", "pattern", durationPattern) },
	reflect.TypeFor[blackboxconfig.Regexp]():     func() *orderedObject { return newObject("type", "string", "format", "regex") },
	reflect.TypeFor[blackboxconfig.CELProgram](): func() *orderedObject { return newObject("type", "string") },
	reflect.TypeFor[promconfig.Secret]():         func() *orderedObject { return newObject("type", "string") },
	reflect.TypeFor[promconfig.URL]():            func() *orderedObject { return newObject("type", "string", "format", "uri") },
	reflect.TypeFor[units.Base2Bytes]():          func() *orderedObject { return newObject("type", "string", "pattern", `^[0-9]+([KMGTPE]i?B|B)?$`) },
	reflect.TypeFor[promconfig.TLSVersion](): func() *orderedObject {
		return newObject("enum", slices.Sorted(maps.Keys(promconfig.TLSVersions)))
	},
}

// fieldValues are the values blackbox accepts for a field its type leaves open, as its configuration reference states them.
var fieldValues = map[string][]string{
	"compression":           {"", "br", "deflate", "gzip", "identity"},
	"preferred_ip_protocol": {"ip4", "ip6"},
	"transport_protocol":    {"tcp", "udp"},
}

// blackboxDefinitions builds the schema definitions of blackbox's probers' settings, read from their Go types and described by the configuration reference.
type blackboxDefinitions struct {
	configurationReference configurationReference
	definitions            *orderedObject
}

func newBlackboxDefinitions(reference configurationReference) *blackboxDefinitions {
	return &blackboxDefinitions{configurationReference: reference, definitions: newObject()}
}

var (
	acronymBoundary = regexp.MustCompile(`([A-Z]+)([A-Z][a-z])`)
	wordBoundary    = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

// definitionNames are the names of the types whose Go names split into words badly.
var definitionNames = map[reflect.Type]string{
	reflect.TypeFor[promconfig.OAuth2](): "oauth2",
}

func definitionName(goType reflect.Type) string {
	if name, named := definitionNames[goType]; named {
		return "blackbox_" + name
	}
	words := wordBoundary.ReplaceAllString(acronymBoundary.ReplaceAllString(goType.Name(), "${1}_${2}"), "${1}_${2}")
	return "blackbox_" + strings.ToLower(words)
}

// definitionReference is the schema reference to a struct type's definition, built the first time the type is reached.
func (builder *blackboxDefinitions) definitionReference(goType reflect.Type, sections []string) *orderedObject {
	name := definitionName(goType)
	if _, built := builder.definitions.values[name]; !built {
		builder.definitions.set(name, nil)
		builder.definitions.set(name, builder.structSchema(goType, sections))
	}
	return newObject("$ref", "#/$defs/"+name)
}

func (builder *blackboxDefinitions) structSchema(goType reflect.Type, sections []string) *orderedObject {
	if section, documented := referenceSections[goType]; documented {
		sections = append([]string{section}, sections...)
	}
	properties := newObject()
	var additional any = false
	builder.addFields(goType, sections, properties, &additional)
	schema := newObject("type", "object")
	if len(properties.keys) > 0 {
		schema.set("properties", properties)
	}
	return schema.set("additionalProperties", additional)
}

func (builder *blackboxDefinitions) addFields(goType reflect.Type, sections []string, properties *orderedObject, additional *any) {
	for index := range goType.NumField() {
		field := goType.Field(index)
		name, options, _ := strings.Cut(field.Tag.Get("yaml"), ",")
		if !field.IsExported() || name == "-" {
			continue
		}
		if strings.Contains(options, "inline") {
			inlined := derefType(field.Type)
			if inlined.Kind() == reflect.Map {
				*additional = builder.schemaFor(inlined.Elem(), sections)
				continue
			}
			if section, documented := referenceSections[inlined]; documented {
				builder.addFields(inlined, append(slices.Clone(sections), section), properties, additional)
			} else {
				builder.addFields(inlined, sections, properties, additional)
			}
			continue
		}
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		schema := builder.schemaFor(field.Type, sections)
		if values, closed := fieldValues[name]; closed {
			schema = newObject("enum", values)
		}
		if read, documented := builder.configurationReference.lookUp(sections, name); documented {
			schema = describe(schema, read)
		}
		properties.set(name, schema)
	}
}

func describe(schema *orderedObject, read fieldReference) *orderedObject {
	description := read.description
	if read.defaultValue != "" {
		description = strings.TrimSpace(fmt.Sprintf("%s `%s` by default.", ensureSentence(description), read.defaultValue))
	}
	if description == "" {
		return schema
	}
	if _, isReference := schema.values["$ref"]; isReference {
		return newObject("description", description, "$ref", schema.values["$ref"])
	}
	described := newObject("description", description)
	for _, key := range schema.keys {
		described.set(key, schema.values[key])
	}
	return described
}

func ensureSentence(text string) string {
	if text == "" || strings.HasSuffix(text, ".") {
		return text
	}
	return text + "."
}

func derefType(goType reflect.Type) reflect.Type {
	for goType.Kind() == reflect.Pointer {
		goType = goType.Elem()
	}
	return goType
}

func (builder *blackboxDefinitions) schemaFor(goType reflect.Type, sections []string) *orderedObject {
	goType = derefType(goType)
	if scalar, isScalar := scalarTypes[goType]; isScalar {
		return scalar()
	}
	switch goType.Kind() {
	case reflect.Bool:
		return newObject("type", "boolean")
	case reflect.String:
		return newObject("type", "string")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return newObject("type", "integer")
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return newObject("type", "integer", "minimum", 0)
	case reflect.Float32, reflect.Float64:
		return newObject("type", "number")
	case reflect.Slice, reflect.Array:
		return newObject("type", "array", "items", builder.schemaFor(goType.Elem(), sections))
	case reflect.Map:
		return newObject("type", "object", "additionalProperties", builder.schemaFor(goType.Elem(), sections))
	case reflect.Struct:
		return builder.definitionReference(goType, sections)
	default:
		return newObject()
	}
}

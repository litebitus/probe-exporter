package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
)

// orderedObject is a JSON object whose keys keep the order they were added in.
type orderedObject struct {
	keys   []string
	values map[string]any
}

func newObject(keysAndValues ...any) *orderedObject {
	object := &orderedObject{values: map[string]any{}}
	for index := 0; index < len(keysAndValues); index += 2 {
		object.set(keysAndValues[index].(string), keysAndValues[index+1])
	}
	return object
}

func (object *orderedObject) set(key string, value any) *orderedObject {
	if _, held := object.values[key]; !held {
		object.keys = append(object.keys, key)
	}
	object.values[key] = value
	return object
}

func (object *orderedObject) remove(key string) {
	delete(object.values, key)
	object.keys = slices.DeleteFunc(object.keys, func(held string) bool { return held == key })
}

// parseOrdered reads JSON text keeping every object's keys in the order written and every number as written.
func parseOrdered(text []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(text))
	decoder.UseNumber()
	value, err := parseOrderedValue(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("text follows the JSON value")
	}
	return value, nil
}

func parseOrderedValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch token {
	case json.Delim('{'):
		object := newObject()
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			value, err := parseOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			object.set(key.(string), value)
		}
		_, err := decoder.Token()
		return object, err
	case json.Delim('['):
		items := []any{}
		for decoder.More() {
			item, err := parseOrderedValue(decoder)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		_, err := decoder.Token()
		return items, err
	default:
		return token, nil
	}
}

func (object *orderedObject) MarshalJSON() ([]byte, error) {
	var buffer bytes.Buffer
	buffer.WriteByte('{')
	for index, key := range object.keys {
		if index > 0 {
			buffer.WriteByte(',')
		}
		encodedKey, err := marshalWithoutEscaping(key)
		if err != nil {
			return nil, err
		}
		encodedValue, err := marshalWithoutEscaping(object.values[key])
		if err != nil {
			return nil, err
		}
		buffer.Write(encodedKey)
		buffer.WriteByte(':')
		buffer.Write(encodedValue)
	}
	buffer.WriteByte('}')
	return buffer.Bytes(), nil
}

func marshalWithoutEscaping(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buffer.Bytes(), []byte("\n")), nil
}

// indentedJSON is a value as indented JSON, ending in a newline, with no HTML escaping.
func indentedJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

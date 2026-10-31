// Package jsonwire validates strict versioned snake-case JSON object contracts.
package jsonwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

// DecodeObject rejects duplicate/aliased keys, unknown fields, trailing data, excessive depth and invalid UTF-8.
func DecodeObject(raw []byte, target any) error {
	trimmed := bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("JSON object required")
	}
	structure := json.NewDecoder(bytes.NewReader(raw))
	structure.UseNumber()
	if err := checkValue(structure, 0, true); err != nil {
		return err
	}
	if _, err := structure.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

// DecodeExternalObject tolerates provider-owned fields/case while rejecting duplicate keys,
// trailing values, invalid UTF-8 and excessive depth. It is not the Workgraph API decoder.
func DecodeExternalObject(raw []byte, target any) error {
	return decodeExternal(raw, target, '{')
}

// DecodeExternalArray validates bounded provider list structure without imposing API-owned fields.
func DecodeExternalArray(raw []byte, target any) error { return decodeExternal(raw, target, '[') }
func decodeExternal(raw []byte, target any, root byte) error {
	trimmed := bytes.TrimSpace(raw)
	if !utf8.Valid(raw) || len(trimmed) == 0 || trimmed[0] != root {
		return errors.New("JSON object required")
	}
	structure := json.NewDecoder(bytes.NewReader(raw))
	structure.UseNumber()
	if err := checkValue(structure, 0, false); err != nil {
		return err
	}
	if _, err := structure.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return json.Unmarshal(raw, target)
}

func checkValue(decoder *json.Decoder, depth int, strict bool) error {
	if depth > 32 {
		return errors.New("JSON depth exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	switch delimiter {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok || (strict && key != strings.ToLower(key)) || seen[key] {
				return errors.New("duplicate JSON key")
			}
			seen[key] = true
			if err := checkValue(decoder, depth+1, strict); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := checkValue(decoder, depth+1, strict); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil || closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	return nil
}

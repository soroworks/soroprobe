package scval

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// Collections are written as JSON, because a list of arguments has no other
// unambiguous nesting syntax that survives a shell:
//
//	vec:["u32:1", "u32:2"]          a Vec<u32>
//	vec:[1, 2]                      bare elements are inferred, like bare args
//	map:[["sym:a", "u32:1"]]        a Map, as [key, value] pairs
//	vec:[["u32:1"], []]             nested collections are nested JSON arrays
//
// Each string element is itself an argument spec, encoded by the same
// Registry, so every registered type — and every type added later — works
// inside a collection for free. JSON numbers, booleans and null stand for
// the bare literals of the same spelling.
//
// Map entries are encoded in the order given. Soroban requires a map's keys
// to be sorted and unique and rejects one that is not, so write them in
// ascending order.

// registerCollections installs the vec and map encoders. They close over the
// Registry so that elements are encoded with whatever encoders it holds.
func registerCollections(r *Registry) {
	r.Register("vec", r.encodeVec)
	r.Register("map", r.encodeMap)
}

func (r *Registry) encodeVec(literal string) (xdr.ScVal, error) {
	items, err := parseJSONArray(literal, "vec")
	if err != nil {
		return xdr.ScVal{}, err
	}
	vec := make(xdr.ScVec, 0, len(items))
	for i, item := range items {
		v, err := r.encodeElement(item)
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("vec[%d]: %w", i, err)
		}
		vec = append(vec, v)
	}
	ptr := &vec
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &ptr}, nil
}

func (r *Registry) encodeMap(literal string) (xdr.ScVal, error) {
	items, err := parseJSONArray(literal, "map")
	if err != nil {
		return xdr.ScVal{}, err
	}
	entries := make(xdr.ScMap, 0, len(items))
	for i, item := range items {
		var pair []json.RawMessage
		if err := json.Unmarshal(item, &pair); err != nil || len(pair) != 2 {
			return xdr.ScVal{}, fmt.Errorf("map[%d]: each entry must be a [key, value] pair", i)
		}
		key, err := r.encodeElement(pair[0])
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("map[%d] key: %w", i, err)
		}
		val, err := r.encodeElement(pair[1])
		if err != nil {
			return xdr.ScVal{}, fmt.Errorf("map[%d] value: %w", i, err)
		}
		entries = append(entries, xdr.ScMapEntry{Key: key, Val: val})
	}
	ptr := &entries
	return xdr.ScVal{Type: xdr.ScValTypeScvMap, Map: &ptr}, nil
}

// encodeElement encodes one JSON element of a collection.
func (r *Registry) encodeElement(raw json.RawMessage) (xdr.ScVal, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return xdr.ScVal{}, errors.New("empty element")
	}

	switch raw[0] {
	case '"':
		var spec string
		if err := json.Unmarshal(raw, &spec); err != nil {
			return xdr.ScVal{}, err
		}
		return r.Encode(spec)
	case '[':
		// A nested array is a nested vec.
		return r.encodeVec(string(raw))
	case '{':
		return xdr.ScVal{}, errors.New(`JSON objects are not supported; write a map as map:[["key", "value"], ...]`)
	}

	// A number, true, false or null: the bare literal of that spelling.
	literal := string(raw)
	if literal != "true" && literal != "false" && literal != "null" && !isIntegerLiteral(literal) {
		return xdr.ScVal{}, fmt.Errorf("%s is not an integer; Soroban has no floating point type", literal)
	}
	return r.Encode(literal)
}

// parseJSONArray reads the value half of a vec: or map: spec.
func parseJSONArray(literal, kind string) ([]json.RawMessage, error) {
	literal = strings.TrimSpace(literal)
	if !strings.HasPrefix(literal, "[") {
		return nil, fmt.Errorf("a %s must be a JSON array, like %s:[...]", kind, kind)
	}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(literal), &items); err != nil {
		return nil, fmt.Errorf("%s is not valid JSON: %w", kind, err)
	}
	return items, nil
}

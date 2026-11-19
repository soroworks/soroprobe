// Package xdrjson decodes base64 Stellar XDR into readable JSON, and backs
// `soroprobe decode`.
//
// The SDK's XDR types marshal to JSON as raw Go structs: hashes become arrays
// of numbers, addresses become nested key structs and enums become integers.
// ToJSON walks a decoded value instead and renders each of those the way a
// person reads them: hashes and byte strings as hex, accounts and contracts
// as strkeys, enums by name and ScVals in the scval package's own notation.
package xdrjson

import (
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"unicode"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/scval"
)

// Type names accepted by Decode.
const (
	TypeEnvelope        = "envelope"
	TypeResult          = "result"
	TypeMeta            = "meta"
	TypeLedgerKey       = "ledger-key"
	TypeLedgerEntry     = "ledger-entry"
	TypeLedgerEntryData = "ledger-entry-data"
	TypeScVal           = "scval"
	TypeSorobanData     = "soroban-data"
	TypeAuthEntry       = "auth-entry"
	// TypeAuto asks Decode to work the type out.
	TypeAuto = "auto"
)

// ErrAmbiguous reports a blob that decodes cleanly as more than one type, so
// auto-detection would have to guess.
var ErrAmbiguous = errors.New("xdr could be more than one type")

// ErrUnknownType reports a type name Decode does not know.
var ErrUnknownType = errors.New("unknown xdr type")

// kinds lists the decodable types. The order is the order auto-detection
// tries them in and the order errors list them in.
var kinds = []struct {
	name string
	doc  string
	new  func() any
}{
	{TypeEnvelope, "a TransactionEnvelope, as submitted", func() any { return &xdr.TransactionEnvelope{} }},
	{TypeResult, "a TransactionResult", func() any { return &xdr.TransactionResult{} }},
	{TypeMeta, "a TransactionMeta, the changes a transaction made", func() any { return &xdr.TransactionMeta{} }},
	{TypeLedgerKey, "a LedgerKey", func() any { return &xdr.LedgerKey{} }},
	{TypeLedgerEntry, "a LedgerEntry", func() any { return &xdr.LedgerEntry{} }},
	{TypeLedgerEntryData, "a LedgerEntryData, as getLedgerEntries returns it", func() any { return &xdr.LedgerEntryData{} }},
	{TypeScVal, "an ScVal: a contract argument, return value or storage value", func() any { return &xdr.ScVal{} }},
	{TypeSorobanData, "a SorobanTransactionData: footprint, resources and resource fee", func() any { return &xdr.SorobanTransactionData{} }},
	{TypeAuthEntry, "a SorobanAuthorizationEntry", func() any { return &xdr.SorobanAuthorizationEntry{} }},
}

// Types lists the type names Decode accepts, each with a short description.
func Types() [][2]string {
	out := make([][2]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, [2]string{k.name, k.doc})
	}
	return out
}

// Decoded is a decoded XDR blob.
type Decoded struct {
	// Type is the type the blob was decoded as.
	Type string `json:"type"`
	// Value is the readable rendering of the decoded value.
	Value any `json:"value"`
}

// Decode decodes the base64 blob as the named type, or with TypeAuto (or an
// empty name) as whichever type it decodes cleanly as. A blob that decodes
// as several types is ambiguous and reported as ErrAmbiguous, naming them.
func Decode(typeName, b64 string) (*Decoded, error) {
	b64 = strings.TrimSpace(b64)
	if b64 == "" {
		return nil, errors.New("no xdr given")
	}
	typeName = strings.ToLower(strings.TrimSpace(typeName))
	if typeName == "" || typeName == TypeAuto {
		return decodeAuto(b64)
	}
	for _, k := range kinds {
		if k.name != typeName {
			continue
		}
		v := k.new()
		if err := xdr.SafeUnmarshalBase64(b64, v); err != nil {
			return nil, fmt.Errorf("decode %s: %w", k.name, err)
		}
		return &Decoded{Type: k.name, Value: ToJSON(v)}, nil
	}
	return nil, fmt.Errorf("%w %q (known: %s)", ErrUnknownType, typeName, strings.Join(names(), ", "))
}

func names() []string {
	out := make([]string, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, k.name)
	}
	return out
}

func decodeAuto(b64 string) (*Decoded, error) {
	var matched []string
	var value any
	for _, k := range kinds {
		v := k.new()
		if err := xdr.SafeUnmarshalBase64(b64, v); err != nil {
			continue
		}
		matched = append(matched, k.name)
		value = v
	}
	switch len(matched) {
	case 0:
		return nil, fmt.Errorf("the xdr does not decode as any known type (%s)", strings.Join(names(), ", "))
	case 1:
		return &Decoded{Type: matched[0], Value: ToJSON(value)}, nil
	default:
		return nil, fmt.Errorf("%w: %s; name the type", ErrAmbiguous, strings.Join(matched, ", "))
	}
}

var (
	scValType     = reflect.TypeFor[xdr.ScVal]()
	accountIDType = reflect.TypeFor[xdr.AccountId]()
	muxedType     = reflect.TypeFor[xdr.MuxedAccount]()
	scAddressType = reflect.TypeFor[xdr.ScAddress]()
	stringerType  = reflect.TypeFor[fmt.Stringer]()
)

// ToJSON renders a decoded XDR value as plain JSON-marshalable data.
func ToJSON(v any) any {
	return render(reflect.ValueOf(v))
}

func render(v reflect.Value) any {
	if !v.IsValid() {
		return nil
	}
	if v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface {
		if v.IsNil() {
			return nil
		}
		return render(v.Elem())
	}

	switch v.Type() {
	case scValType:
		val := v.Interface().(xdr.ScVal)
		if decoded, err := scval.Decode(val); err == nil {
			return decoded
		}
	case accountIDType:
		id := v.Interface().(xdr.AccountId)
		if addr, err := id.GetAddress(); err == nil {
			return addr
		}
	case muxedType:
		m := v.Interface().(xdr.MuxedAccount)
		if addr, err := m.GetAddress(); err == nil {
			return addr
		}
	case scAddressType:
		a := v.Interface().(xdr.ScAddress)
		if addr, err := a.String(); err == nil {
			return addr
		}
	}

	switch v.Kind() {
	case reflect.Array, reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return hexOf(v)
		}
		out := make([]any, v.Len())
		for i := range out {
			out[i] = render(v.Index(i))
		}
		return out
	case reflect.Struct:
		return renderStruct(v)
	case reflect.Map:
		out := map[string]any{}
		for _, key := range v.MapKeys() {
			out[fmt.Sprint(key.Interface())] = render(v.MapIndex(key))
		}
		return out
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32:
		// XDR enums are int32 types with a String method.
		if v.Type().Implements(stringerType) && v.Type().PkgPath() != "" {
			return v.Interface().(fmt.Stringer).String()
		}
		return v.Int()
	case reflect.Int64:
		return v.Int()
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint()
	case reflect.Bool:
		return v.Bool()
	case reflect.String:
		return v.String()
	}
	return fmt.Sprint(v.Interface())
}

func hexOf(v reflect.Value) string {
	b := make([]byte, v.Len())
	for i := range b {
		b[i] = byte(v.Index(i).Uint()) //nolint:gosec // the element kind is uint8, so it fits a byte
	}
	return hex.EncodeToString(b)
}

// renderStruct renders the struct's fields under snake_case names. Nil
// pointers are left out, which is what turns an XDR union into its
// discriminant plus the one arm that is set.
func renderStruct(v reflect.Value) any {
	t := v.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		fv := v.Field(i)
		if (fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface) && fv.IsNil() {
			continue
		}
		out[snake(f.Name)] = render(fv)
	}
	return out
}

func snake(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		if unicode.IsUpper(r) {
			// Start a new word at an upper-case letter that follows a
			// lower-case one, or that starts a word after an acronym
			// ("XDRValue" -> "xdr_value").
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1]) ||
				(i+1 < len(runes) && unicode.IsLower(runes[i+1]) && unicode.IsUpper(runes[i-1]))) {
				b.WriteByte('_')
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// EncodeScVal encodes an argument in SoroProbe's typed syntax ("u32:5",
// "addr:G...", `vec:[...]`) as base64 ScVal XDR.
func EncodeScVal(codec *scval.Registry, spec string) (string, error) {
	if codec == nil {
		codec = scval.NewRegistry()
	}
	v, err := codec.Encode(spec)
	if err != nil {
		return "", err
	}
	out, err := xdr.MarshalBase64(v)
	if err != nil {
		return "", fmt.Errorf("encode scval: %w", err)
	}
	return out, nil
}

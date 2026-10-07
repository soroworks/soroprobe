// Package abi types simulate arguments from a contract's published interface.
//
// SoroProbe cannot read a contract's interface from the ledger cheaply, so on
// its own it infers argument types from their spelling, and a bare "5" becomes
// an i128. A contract that expects u32 then rejects the call with a host error
// that says nothing about the argument. When an interface source such as a
// SoroVault registry is configured, Annotate replaces that guess with the type
// the contract actually declares, and catches a wrong argument count or a
// misspelled function name before anything is simulated.
package abi

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrUnknownContract reports that the interface source has no record of the
// contract. Callers fall back to inference rather than failing, because an
// unregistered contract is still a contract worth probing.
var ErrUnknownContract = errors.New("abi: contract is not in the interface registry")

// ErrNoSuchFunction reports that the contract's interface does not export the
// requested function. Unlike ErrUnknownContract this is a definite answer: a
// simulation could only fail.
var ErrNoSuchFunction = errors.New("abi: contract does not export that function")

// ErrArgumentCount reports a call with more or fewer arguments than the
// function declares.
var ErrArgumentCount = errors.New("abi: wrong number of arguments")

// Type is a value type in a contract interface. It mirrors the subset of
// SoroVault's ABI JSON that argument typing needs.
type Type struct {
	Kind    string `json:"kind"`
	Display string `json:"display"`
	// Inner is the wrapped type of an option.
	Inner *Type `json:"inner,omitempty"`
}

// Param is a named function input.
type Param struct {
	Name string `json:"name"`
	Type Type   `json:"type"`
}

// Function is a contract entry point.
type Function struct {
	Name    string  `json:"name"`
	Doc     string  `json:"doc,omitempty"`
	Inputs  []Param `json:"inputs"`
	Outputs []Type  `json:"outputs"`
}

// Interface is the part of a contract interface Annotate needs.
type Interface struct {
	Functions []Function `json:"functions"`
}

// Lookup returns the named function, or an ErrNoSuchFunction error listing
// the functions that do exist, so a typo is a one-step fix.
func (i *Interface) Lookup(name string) (*Function, error) {
	names := make([]string, 0, len(i.Functions))
	for idx := range i.Functions {
		if i.Functions[idx].Name == name {
			return &i.Functions[idx], nil
		}
		names = append(names, i.Functions[idx].Name)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("%w: %q (available: %s)", ErrNoSuchFunction, name, strings.Join(names, ", "))
}

// Signature renders the function in Rust-like notation, as SoroVault does:
// "fn transfer(from: Address, to: Address, amount: i128)".
func (f *Function) Signature() string {
	var b strings.Builder
	b.WriteString("fn ")
	b.WriteString(f.Name)
	b.WriteByte('(')
	for i, p := range f.Inputs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(p.Name)
		b.WriteString(": ")
		b.WriteString(p.Type.Display)
	}
	b.WriteByte(')')
	if len(f.Outputs) > 0 {
		b.WriteString(" -> ")
		b.WriteString(f.Outputs[0].Display)
	}
	return b.String()
}

// Source supplies contract interfaces.
type Source interface {
	// Interface returns the current interface of contractID on the given
	// network. It returns an error wrapping ErrUnknownContract when the
	// source has no record of the contract.
	Interface(ctx context.Context, network, contractID string) (*Interface, error)
}

// prefixes maps an interface kind onto the scval argument prefix that encodes
// it. Kinds missing here — vec, map, tuple, user-defined types — have no
// single-literal argument form, so their arguments are passed through and
// must be written explicitly.
var prefixes = map[string]string{
	"bool":      "bool",
	"void":      "void",
	"u32":       "u32",
	"i32":       "i32",
	"u64":       "u64",
	"i64":       "i64",
	"u128":      "u128",
	"i128":      "i128",
	"u256":      "u256",
	"i256":      "i256",
	"timepoint": "timepoint",
	"duration":  "duration",
	"bytes":     "bytes",
	"bytes_n":   "bytes",
	"string":    "string",
	"symbol":    "symbol",
	"address":   "address",
}

// Annotate checks args against fn's declared inputs and gives each untyped
// argument the prefix of its declared type.
//
// hasType reports whether an argument already names its type; those are left
// exactly as written, so an explicit prefix always wins over the interface.
// An Option<T> parameter takes "void"/"null" as None and otherwise types the
// literal as T.
func Annotate(fn *Function, args []string, hasType func(string) bool) ([]string, error) {
	if len(args) != len(fn.Inputs) {
		return nil, fmt.Errorf("%w: %s takes %d argument(s), got %d",
			ErrArgumentCount, fn.Signature(), len(fn.Inputs), len(args))
	}

	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = arg
		if hasType(arg) {
			continue
		}
		if prefix, ok := prefixFor(fn.Inputs[i].Type, arg); ok {
			out[i] = prefix + ":" + arg
		}
	}
	return out, nil
}

func prefixFor(t Type, literal string) (string, bool) {
	if t.Kind == "option" {
		switch strings.ToLower(literal) {
		case "void", "null":
			return "void", true
		}
		if t.Inner == nil {
			return "", false
		}
		return prefixFor(*t.Inner, literal)
	}
	p, ok := prefixes[t.Kind]
	return p, ok
}

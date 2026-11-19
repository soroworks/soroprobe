// Package wasm reads the parts of a Soroban contract's WebAssembly module
// SoroProbe reports on: its size, exports, imported host functions, the
// contractmetav0 and contractenvmetav0 custom sections, and the call graph
// of its code.
//
// It is a small, defensive section parser rather than a full WebAssembly
// implementation: every length is bounds-checked, so a malformed or hostile
// module produces an error, never a panic.
package wasm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrMalformed reports a module that is not well-formed WebAssembly.
var ErrMalformed = errors.New("malformed wasm")

// Section ids.
const (
	secCustom = 0
	secImport = 2
	secExport = 7
	secCode   = 10
)

// External kinds of imports and exports.
const (
	KindFunction = "function"
	KindTable    = "table"
	KindMemory   = "memory"
	KindGlobal   = "global"
)

var kindNames = map[byte]string{0: KindFunction, 1: KindTable, 2: KindMemory, 3: KindGlobal}

// Import is one imported item. A Soroban host function is a function import
// from one of the host's short module names.
type Import struct {
	Module string `json:"module"`
	Name   string `json:"name"`
	Kind   string `json:"kind"`
	// HostFunction is the host function's documented name, when SoroProbe
	// knows it.
	HostFunction string `json:"host_function,omitempty"`
}

// Export is one exported item.
type Export struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Index is the item's index in its index space; for a function, the
	// function index, which counts imported functions first.
	Index uint32 `json:"index"`
}

// EnvVersion is the Soroban environment interface a contract was built for.
type EnvVersion struct {
	Protocol uint32 `json:"protocol"`
	// PreRelease is non-zero for a build against a pre-release of the
	// environment, which only a network running that pre-release accepts.
	PreRelease uint32 `json:"pre_release"`
}

// Module is what SoroProbe reads from a contract's Wasm.
type Module struct {
	Size    int      `json:"size_bytes"`
	Imports []Import `json:"imports"`
	Exports []Export `json:"exports"`
	// Meta is the contractmetav0 key/value metadata: the Rust and SDK
	// versions and any custom entries.
	Meta map[string]string `json:"meta"`
	// Env is the contractenvmetav0 interface version, when present.
	Env *EnvVersion `json:"env,omitempty"`
	// HasSpec reports a contractspecv0 section.
	HasSpec bool `json:"has_spec"`
	// CustomSections lists every custom section by name, in order.
	CustomSections []string `json:"custom_sections"`

	importedFuncs uint32
	funcBodies    [][]byte
}

// ImportedFunctions counts function imports, which take the first function
// indices.
func (m *Module) ImportedFunctions() uint32 { return m.importedFuncs }

type reader struct {
	b   []byte
	pos int
}

func (r *reader) eof() bool { return r.pos >= len(r.b) }

func (r *reader) byte() (byte, error) {
	if r.pos >= len(r.b) {
		return 0, fmt.Errorf("%w: unexpected end of data", ErrMalformed)
	}
	c := r.b[r.pos]
	r.pos++
	return c, nil
}

func (r *reader) u32() (uint32, error) {
	var v uint64
	for shift := uint(0); shift < 35; shift += 7 {
		c, err := r.byte()
		if err != nil {
			return 0, err
		}
		v |= uint64(c&0x7f) << shift
		if c&0x80 == 0 {
			if v > 0xffffffff {
				return 0, fmt.Errorf("%w: integer overflows 32 bits", ErrMalformed)
			}
			return uint32(v), nil
		}
	}
	return 0, fmt.Errorf("%w: integer too long", ErrMalformed)
}

// leb skips a signed or unsigned LEB128 of at most max bytes.
func (r *reader) leb(max int) error {
	for i := 0; i < max; i++ {
		c, err := r.byte()
		if err != nil {
			return err
		}
		if c&0x80 == 0 {
			return nil
		}
	}
	return fmt.Errorf("%w: integer too long", ErrMalformed)
}

func (r *reader) bytes(n uint32) ([]byte, error) {
	if int64(n) > int64(len(r.b)-r.pos) {
		return nil, fmt.Errorf("%w: length %d runs past the end", ErrMalformed, n)
	}
	out := r.b[r.pos : r.pos+int(n)]
	r.pos += int(n)
	return out, nil
}

func (r *reader) name() (string, error) {
	n, err := r.u32()
	if err != nil {
		return "", err
	}
	b, err := r.bytes(n)
	return string(b), err
}

// Parse reads a module.
func Parse(module []byte) (*Module, error) {
	if len(module) < 8 || !bytes.Equal(module[:4], []byte{0, 'a', 's', 'm'}) {
		return nil, fmt.Errorf("%w: missing the \\0asm header", ErrMalformed)
	}
	if v := binary.LittleEndian.Uint32(module[4:8]); v != 1 {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformed, v)
	}
	m := &Module{Size: len(module), Imports: []Import{}, Exports: []Export{}, Meta: map[string]string{}, CustomSections: []string{}}
	r := &reader{b: module, pos: 8}
	for !r.eof() {
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		size, err := r.u32()
		if err != nil {
			return nil, err
		}
		body, err := r.bytes(size)
		if err != nil {
			return nil, err
		}
		sr := &reader{b: body}
		switch id {
		case secCustom:
			err = m.custom(sr)
		case secImport:
			err = m.imports(sr)
		case secExport:
			err = m.exports(sr)
		case secCode:
			err = m.code(sr)
		}
		if err != nil {
			return nil, fmt.Errorf("section %d: %w", id, err)
		}
	}
	return m, nil
}

func (m *Module) custom(r *reader) error {
	name, err := r.name()
	if err != nil {
		return err
	}
	m.CustomSections = append(m.CustomSections, name)
	payload := r.b[r.pos:]
	switch name {
	case "contractspecv0":
		m.HasSpec = true
	case "contractmetav0":
		rd := bytes.NewReader(payload)
		for rd.Len() > 0 {
			var e xdr.ScMetaEntry
			if _, err := xdr.Unmarshal(rd, &e); err != nil {
				return fmt.Errorf("%w: contractmetav0: %w", ErrMalformed, err)
			}
			if v0, ok := e.GetV0(); ok {
				m.Meta[v0.Key] = v0.Val
			}
		}
	case "contractenvmetav0":
		rd := bytes.NewReader(payload)
		for rd.Len() > 0 {
			var e xdr.ScEnvMetaEntry
			if _, err := xdr.Unmarshal(rd, &e); err != nil {
				return fmt.Errorf("%w: contractenvmetav0: %w", ErrMalformed, err)
			}
			if v, ok := e.GetInterfaceVersion(); ok {
				m.Env = &EnvVersion{Protocol: uint32(v.Protocol), PreRelease: uint32(v.PreRelease)}
			}
		}
	}
	return nil
}

func (m *Module) imports(r *reader) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		mod, err := r.name()
		if err != nil {
			return err
		}
		name, err := r.name()
		if err != nil {
			return err
		}
		kind, err := r.byte()
		if err != nil {
			return err
		}
		imp := Import{Module: mod, Name: name, Kind: kindNames[kind]}
		switch kind {
		case 0: // function: type index
			if _, err := r.u32(); err != nil {
				return err
			}
			imp.HostFunction = hostFunctionName(mod, name)
			m.importedFuncs++
		case 1: // table: reftype, limits
			if _, err := r.byte(); err != nil {
				return err
			}
			if err := r.limits(); err != nil {
				return err
			}
		case 2: // memory: limits
			if err := r.limits(); err != nil {
				return err
			}
		case 3: // global: valtype, mutability
			if _, err := r.bytes(2); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: unknown import kind %d", ErrMalformed, kind)
		}
		m.Imports = append(m.Imports, imp)
	}
	return nil
}

func (r *reader) limits() error {
	flag, err := r.byte()
	if err != nil {
		return err
	}
	if _, err := r.u32(); err != nil {
		return err
	}
	if flag&1 == 1 {
		_, err = r.u32()
	}
	return err
}

func (m *Module) exports(r *reader) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	for i := uint32(0); i < n; i++ {
		name, err := r.name()
		if err != nil {
			return err
		}
		kind, err := r.byte()
		if err != nil {
			return err
		}
		idx, err := r.u32()
		if err != nil {
			return err
		}
		k, ok := kindNames[kind]
		if !ok {
			return fmt.Errorf("%w: unknown export kind %d", ErrMalformed, kind)
		}
		m.Exports = append(m.Exports, Export{Name: name, Kind: k, Index: idx})
	}
	return nil
}

func (m *Module) code(r *reader) error {
	n, err := r.u32()
	if err != nil {
		return err
	}
	m.funcBodies = make([][]byte, 0, n)
	for i := uint32(0); i < n; i++ {
		size, err := r.u32()
		if err != nil {
			return err
		}
		body, err := r.bytes(size)
		if err != nil {
			return err
		}
		m.funcBodies = append(m.funcBodies, body)
	}
	return nil
}

// hostFunctions names the Soroban host functions SoroProbe recognizes by
// their import module and name, as soroban-env-common's env.json defines
// them. Only the ones a report reasons about are listed.
var hostFunctions = map[[2]string]string{
	{"a", "_"}: "require_auth_for_args",
	{"a", "0"}: "require_auth",
}

func hostFunctionName(module, name string) string {
	return hostFunctions[[2]string{module, name}]
}

// IsAuthImport reports whether an import is one of the require_auth host
// functions.
func IsAuthImport(imp Import) bool {
	return imp.HostFunction == "require_auth" || imp.HostFunction == "require_auth_for_args"
}

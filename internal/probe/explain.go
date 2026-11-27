package probe

import (
	"context"
	"fmt"
	"regexp"
	"strconv"

	"github.com/soroworks/soroprobe/internal/abi"
)

// Error kinds an ErrorExplanation can carry.
const (
	// ErrorKindContract is an error a contract raised itself, identified by
	// a number from the contract's own error enum.
	ErrorKindContract = "contract"
	// ErrorKindHost is an error the Soroban host raised, identified by an
	// error type and a code the protocol defines.
	ErrorKindHost = "host"
)

// ErrorExplanation turns an "Error(Type, Code)" from a simulation or a
// transaction result into something a person can act on without looking the
// code up by hand.
type ErrorExplanation struct {
	// Kind is ErrorKindContract or ErrorKindHost.
	Kind string `json:"kind"`
	// Raw is the error exactly as the network reported it, such as
	// "Error(Contract, #3)".
	Raw string `json:"raw"`
	// Type is the host error type, such as "Contract", "Storage" or "Auth".
	Type string `json:"type"`
	// Code is the contract error number. It is set only for contract errors.
	Code *uint32 `json:"code,omitempty"`
	// Name is the error enum case for a contract error ("InsufficientBalance"),
	// or the host error code ("MissingValue").
	Name string `json:"name,omitempty"`
	// Enum is the contract error enum the case belongs to.
	Enum string `json:"enum,omitempty"`
	// Doc is the doc comment on the error enum case, when the contract has one.
	Doc string `json:"doc,omitempty"`
	// Description says what the error type and code mean, for host errors.
	Description string `json:"description,omitempty"`
	// Note explains why a contract error could not be named.
	Note string `json:"note,omitempty"`
}

// errorPattern matches the "Error(Type, Code)" form the host uses in
// simulation errors, diagnostic events and transaction results. A contract
// error's code is a number written "#3"; a host error's is a name.
var errorPattern = regexp.MustCompile(`Error\(\s*([A-Za-z]+)\s*,\s*(#?[A-Za-z0-9]+)\s*\)`)

// hostErrorTypes describes each ScErrorType, by the name the host prints.
var hostErrorTypes = map[string]string{
	"Contract": "an error raised by the contract itself",
	"WasmVm":   "the Wasm virtual machine failed, such as a trap, an unreachable instruction or a bad module",
	"Context":  "the call context was invalid, such as a re-entrant call or a call frame that does not exist",
	"Storage":  "a ledger entry could not be read or written, such as one outside the footprint or one that does not exist",
	"Object":   "a host object was misused, such as an out-of-range handle or the wrong object type",
	"Crypto":   "a cryptographic operation failed, such as a bad signature or key",
	"Events":   "an event could not be recorded",
	"Budget":   "the call ran out of its CPU or memory budget",
	"Value":    "a value was malformed or of the wrong type, such as an unknown function name or a bad argument",
	"Auth":     "authorization failed: a required signature is missing or does not match",
}

// hostErrorCodes describes each ScErrorCode, by the name the host prints.
var hostErrorCodes = map[string]string{
	"ArithDomain":    "some arithmetic was undefined, such as an overflow or a division by zero",
	"IndexBounds":    "something was indexed beyond its bounds",
	"InvalidInput":   "the input was otherwise bad data",
	"MissingValue":   "a value was required but not provided",
	"ExistingValue":  "a value was provided where none is allowed",
	"ExceededLimit":  "a limit was hit, such as a resource budget",
	"InvalidAction":  "the data was valid but the requested action was not",
	"InternalError":  "the host detected an error in its own logic",
	"UnexpectedType": "a value had an unexpected type",
	"UnexpectedSize": "a value had an unexpected size",
}

// ParseError finds the first "Error(Type, Code)" in msg and explains it
// without a contract interface: a host error is fully described, while a
// contract error carries only its number. It returns nil when msg holds no
// such error.
func ParseError(msg string) *ErrorExplanation {
	m := errorPattern.FindStringSubmatch(msg)
	if m == nil {
		return nil
	}
	errType, code := m[1], m[2]
	out := &ErrorExplanation{Raw: m[0], Type: errType}

	if errType == "Contract" {
		out.Kind = ErrorKindContract
		n, err := strconv.ParseUint(trimHash(code), 10, 32)
		if err != nil {
			out.Note = fmt.Sprintf("contract error code %q is not a number", code)
			return out
		}
		value := uint32(n)
		out.Code = &value
		out.Description = hostErrorTypes[errType]
		return out
	}

	out.Kind = ErrorKindHost
	out.Name = code
	typeDesc, knownType := hostErrorTypes[errType]
	codeDesc, knownCode := hostErrorCodes[code]
	switch {
	case knownType && knownCode:
		out.Description = typeDesc + "; " + codeDesc
	case knownType:
		out.Description = typeDesc
	case knownCode:
		out.Description = codeDesc
	default:
		out.Note = "unrecognized host error type and code"
	}
	return out
}

func trimHash(s string) string {
	if len(s) > 0 && s[0] == '#' {
		return s[1:]
	}
	return s
}

// ExplainError explains the first error in msg, naming a contract error from
// the error enums in iface. iface may be nil, in which case a contract error
// keeps only its number and a note says why.
func ExplainError(msg string, iface *abi.Interface) *ErrorExplanation {
	out := ParseError(msg)
	if out == nil || out.Kind != ErrorKindContract || out.Code == nil {
		return out
	}
	if iface == nil {
		out.Note = "the contract interface is not available, so the code cannot be named"
		return out
	}
	enums := 0
	for _, udt := range iface.Types {
		if udt.Kind != abi.UDTErrorEnum {
			continue
		}
		enums++
		for _, c := range udt.Cases {
			if c.Value == *out.Code {
				out.Enum = udt.Name
				out.Name = c.Name
				out.Doc = c.Doc
				out.Note = ""
				return out
			}
		}
	}
	if enums == 0 {
		out.Note = "the contract declares no error enum, so the code cannot be named"
	} else {
		out.Note = fmt.Sprintf("code %d is not a case of the contract's error enum", *out.Code)
	}
	return out
}

// explainFailure explains a failed call against contractID. A contract
// error is looked up in the interface stored on chain, which is what the
// deployed code actually raises; a lookup that fails leaves the code
// unnamed with a note, because the explanation is advisory.
func (p *Prober) explainFailure(ctx context.Context, contractID, msg string) *ErrorExplanation {
	out := ParseError(msg)
	if out == nil || out.Kind != ErrorKindContract || out.Code == nil {
		return out
	}
	iface, err := p.chain.Interface(ctx, p.network, contractID)
	if err != nil {
		p.log.Debug("could not read the contract interface to name an error", "contract", contractID, "err", err)
		explained := ExplainError(msg, nil)
		explained.Note = "the contract interface could not be read, so the code cannot be named: " + err.Error()
		return explained
	}
	return ExplainError(msg, iface)
}

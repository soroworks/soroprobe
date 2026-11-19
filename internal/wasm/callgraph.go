package wasm

import (
	"fmt"
	"sort"
	"strings"
)

// CallGraph records, for every defined function, the functions it calls
// directly and whether it makes indirect calls.
type CallGraph struct {
	// Calls maps a function index to the function indices it calls.
	Calls map[uint32][]uint32
	// Indirect marks functions that use call_indirect, whose targets
	// cannot be known without running the code.
	Indirect map[uint32]bool
}

// CallGraph decodes the code section's instructions and collects every
// direct call. It returns an error for an instruction it cannot decode,
// such as a SIMD opcode, which Soroban does not allow.
func (m *Module) CallGraph() (*CallGraph, error) {
	g := &CallGraph{Calls: map[uint32][]uint32{}, Indirect: map[uint32]bool{}}
	for i, body := range m.funcBodies {
		idx := m.importedFuncs + uint32(i)
		calls, indirect, err := scanBody(body)
		if err != nil {
			return nil, fmt.Errorf("function %d: %w", idx, err)
		}
		g.Calls[idx] = calls
		if indirect {
			g.Indirect[idx] = true
		}
	}
	return g, nil
}

// scanBody walks one function body's instructions.
func scanBody(body []byte) ([]uint32, bool, error) {
	r := &reader{b: body}
	groups, err := r.u32()
	if err != nil {
		return nil, false, err
	}
	for i := uint32(0); i < groups; i++ {
		if _, err := r.u32(); err != nil {
			return nil, false, err
		}
		if _, err := r.byte(); err != nil {
			return nil, false, err
		}
	}
	var calls []uint32
	indirect := false
	u32s := func(n int) error {
		for i := 0; i < n; i++ {
			if _, err := r.u32(); err != nil {
				return err
			}
		}
		return nil
	}
	for !r.eof() {
		op, err := r.byte()
		if err != nil {
			return nil, false, err
		}
		switch {
		case op == 0x00, op == 0x01, op == 0x05, op == 0x0b, op == 0x0f, op == 0x1a, op == 0x1b,
			op == 0xd1, op >= 0x45 && op <= 0xc4:
			// No immediates.
		case op >= 0x02 && op <= 0x04: // block, loop, if: a block type
			bt, err := r.byte()
			if err != nil {
				return nil, false, err
			}
			if bt != 0x40 && (bt < 0x6f || bt > 0x7f) {
				r.pos--
				if err := r.leb(5); err != nil {
					return nil, false, err
				}
			}
		case op == 0x0c, op == 0x0d: // br, br_if
			err = u32s(1)
		case op == 0x0e: // br_table
			var n uint32
			if n, err = r.u32(); err == nil {
				err = u32s(int(n) + 1)
			}
		case op == 0x10, op == 0x12: // call, return_call
			var callee uint32
			if callee, err = r.u32(); err == nil {
				calls = append(calls, callee)
			}
		case op == 0x11, op == 0x13: // call_indirect, return_call_indirect
			indirect = true
			err = u32s(2)
		case op == 0x1c: // select with types
			var n uint32
			if n, err = r.u32(); err == nil {
				_, err = r.bytes(n)
			}
		case op >= 0x20 && op <= 0x26: // locals, globals, table.get/set
			err = u32s(1)
		case op >= 0x28 && op <= 0x3e: // loads and stores: memarg
			err = u32s(2)
		case op == 0x3f, op == 0x40: // memory.size, memory.grow
			_, err = r.byte()
		case op == 0x41:
			err = r.leb(5)
		case op == 0x42:
			err = r.leb(10)
		case op == 0x43:
			_, err = r.bytes(4)
		case op == 0x44:
			_, err = r.bytes(8)
		case op == 0xd0: // ref.null
			_, err = r.byte()
		case op == 0xd2: // ref.func
			err = u32s(1)
		case op == 0xfc:
			err = r.prefixed()
		default:
			return nil, false, fmt.Errorf("%w: unsupported opcode 0x%02x", ErrMalformed, op)
		}
		if err != nil {
			return nil, false, err
		}
	}
	return calls, indirect, nil
}

// prefixed skips a 0xfc-prefixed instruction's immediates.
func (r *reader) prefixed() error {
	sub, err := r.u32()
	if err != nil {
		return err
	}
	switch {
	case sub <= 7: // saturating truncations
		return nil
	case sub == 8: // memory.init
		if _, err := r.u32(); err != nil {
			return err
		}
		_, err = r.byte()
		return err
	case sub == 9, sub == 13, sub == 15, sub == 16, sub == 17: // data.drop, elem.drop, table.grow/size/fill
		_, err = r.u32()
		return err
	case sub == 10: // memory.copy
		_, err = r.bytes(2)
		return err
	case sub == 11: // memory.fill
		_, err = r.byte()
		return err
	case sub == 12, sub == 14: // table.init, table.copy
		if _, err := r.u32(); err != nil {
			return err
		}
		_, err = r.u32()
		return err
	}
	return fmt.Errorf("%w: unsupported 0xfc instruction %d", ErrMalformed, sub)
}

// DefaultPrivilegedNames are name fragments that suggest a function changes
// who controls a contract or moves its funds.
var DefaultPrivilegedNames = []string{
	"upgrade", "set_admin", "set_owner", "transfer_ownership", "withdraw",
	"mint", "burn", "pause", "unpause", "set_config", "migrate", "destroy",
}

// Auth verdicts.
const (
	VerdictRequiresAuth = "requires_auth"
	VerdictNoAuth       = "no_auth_found"
	VerdictUnknown      = "unknown"
)

// AuthFinding is the heuristic verdict on one privileged-looking function.
type AuthFinding struct {
	Function string `json:"function"`
	// Matched is the name fragment that marked it privileged.
	Matched string `json:"matched"`
	// Verdict is VerdictRequiresAuth, VerdictNoAuth or VerdictUnknown.
	Verdict string `json:"verdict"`
	// Path is the call chain to a require_auth host function, when one
	// exists.
	Path []string `json:"path,omitempty"`
	// Reasoning explains the verdict in words.
	Reasoning string `json:"reasoning"`
}

// AuthLint checks every exported function whose name contains one of the
// privileged fragments for a call path to require_auth or
// require_auth_for_args.
//
// It is a heuristic: a path in the call graph does not prove the call
// happens on every route through the function, and authorization done some
// other way (a host function SoroProbe does not recognize, or a check
// against stored state) is not seen. An indirect call may reach code the
// graph cannot follow, so a function without a direct path but with an
// indirect call is reported as unknown rather than unauthenticated.
func (m *Module) AuthLint(privileged []string) ([]AuthFinding, error) {
	if len(privileged) == 0 {
		privileged = DefaultPrivilegedNames
	}
	g, err := m.CallGraph()
	if err != nil {
		return nil, err
	}

	authImports := map[uint32]string{}
	var fnIdx uint32
	for _, imp := range m.Imports {
		if imp.Kind != KindFunction {
			continue
		}
		if IsAuthImport(imp) {
			authImports[fnIdx] = imp.HostFunction
		}
		fnIdx++
	}
	names := map[uint32]string{}
	for _, e := range m.Exports {
		if e.Kind == KindFunction {
			names[e.Index] = e.Name
		}
	}
	name := func(idx uint32) string {
		if n, ok := authImports[idx]; ok {
			return "host:" + n
		}
		if n, ok := names[idx]; ok {
			return n
		}
		return fmt.Sprintf("func[%d]", idx)
	}

	var findings []AuthFinding
	for _, e := range m.Exports {
		if e.Kind != KindFunction {
			continue
		}
		matched := ""
		lower := strings.ToLower(e.Name)
		for _, p := range privileged {
			if p != "" && strings.Contains(lower, strings.ToLower(p)) {
				matched = p
				break
			}
		}
		if matched == "" {
			continue
		}

		f := AuthFinding{Function: e.Name, Matched: matched}
		path, sawIndirect := shortestPathToAuth(g, e.Index, authImports)
		switch {
		case path != nil:
			f.Verdict = VerdictRequiresAuth
			for _, idx := range path {
				f.Path = append(f.Path, name(idx))
			}
			f.Reasoning = "a call path reaches " + f.Path[len(f.Path)-1] + ": " + strings.Join(f.Path, " -> ")
		case len(authImports) == 0:
			f.Verdict = VerdictNoAuth
			f.Reasoning = "the module imports no require_auth host function, so no function in it can require authorization that way"
		case sawIndirect:
			f.Verdict = VerdictUnknown
			f.Reasoning = "no direct call path reaches require_auth, but an indirect call on the way could"
		default:
			f.Verdict = VerdictNoAuth
			f.Reasoning = "no call path from this function reaches require_auth or require_auth_for_args"
		}
		findings = append(findings, f)
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].Function < findings[j].Function })
	return findings, nil
}

// shortestPathToAuth searches breadth first from start for an auth import.
func shortestPathToAuth(g *CallGraph, start uint32, auth map[uint32]string) ([]uint32, bool) {
	prev := map[uint32]uint32{}
	seen := map[uint32]bool{start: true}
	queue := []uint32{start}
	sawIndirect := false
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if g.Indirect[cur] {
			sawIndirect = true
		}
		for _, next := range g.Calls[cur] {
			if seen[next] {
				continue
			}
			seen[next] = true
			prev[next] = cur
			if _, ok := auth[next]; ok {
				path := []uint32{next}
				for at := next; at != start; {
					at = prev[at]
					path = append([]uint32{at}, path...)
				}
				return path, sawIndirect
			}
			queue = append(queue, next)
		}
	}
	return nil, sawIndirect
}

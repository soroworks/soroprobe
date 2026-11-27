package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/wasm"
)

// wasmReport is what `soroprobe wasm` prints.
type wasmReport struct {
	// Source is the file or contract the module came from.
	Source string `json:"source"`
	// Hash is the module's SHA-256 in hex: its code entry's key.
	Hash string `json:"hash"`
	*wasm.Module
	// ExportedFunctions are the exported function names, sorted.
	ExportedFunctions []string `json:"exported_functions"`
	// HostFunctions are the imported functions, as "module.name" with the
	// host function's name when SoroProbe knows it.
	HostFunctions []string `json:"host_functions"`
	// Privileged is the heuristic require_auth check on functions whose
	// names suggest privilege. It is empty when none match.
	Privileged []wasm.AuthFinding `json:"privileged"`
	// PrivilegedNote explains a check that could not run.
	PrivilegedNote string `json:"privileged_note,omitempty"`
}

func newWasmCmd(opts *options) *cobra.Command {
	var privileged []string

	cmd := &cobra.Command{
		Use:   "wasm <contract-id | file.wasm>",
		Short: "Analyze a contract's WASM: size, exports, host imports and metadata",
		Long: `Read a contract's WebAssembly module and report its size, exported
functions, imported host functions, the contractmetav0 metadata (Rust and SDK
versions, any custom entries) and the contractenvmetav0 interface version.

The module comes from a local .wasm file, or from the code entry of a
deployed contract ("C..."). A Stellar Asset Contract has no WASM.

It also flags privileged functions that never require authorization. For
every exported function whose name contains one of the --privileged
fragments (by default upgrade, set_admin, withdraw, mint, burn, pause and
similar), it searches the call graph built from the code section for a path
to the require_auth or require_auth_for_args host function, and reports:

  requires_auth  a path exists, shown as the chain of calls
  no_auth_found  no path exists: an admin action anyone could call
  unknown        no direct path, but an indirect call could lead to one

This is a heuristic. A path does not prove every route through the function
authorizes, and authorization done another way is not seen.

A malformed module is reported as an error, never a crash.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			code, source, err := loadWasm(cmd, opts, args[0])
			if err != nil {
				return err
			}
			m, err := wasm.Parse(code)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(code)
			report := &wasmReport{Source: source, Hash: hex.EncodeToString(sum[:]), Module: m}
			for _, e := range m.Exports {
				if e.Kind == wasm.KindFunction {
					report.ExportedFunctions = append(report.ExportedFunctions, e.Name)
				}
			}
			sort.Strings(report.ExportedFunctions)
			for _, imp := range m.Imports {
				if imp.Kind != wasm.KindFunction {
					continue
				}
				name := imp.Module + "." + imp.Name
				if imp.HostFunction != "" {
					name += " (" + imp.HostFunction + ")"
				}
				report.HostFunctions = append(report.HostFunctions, name)
			}
			findings, err := m.AuthLint(privileged)
			if err != nil {
				report.PrivilegedNote = "the call graph could not be built: " + err.Error()
			}
			report.Privileged = findings
			if report.Privileged == nil {
				report.Privileged = []wasm.AuthFinding{}
			}

			if opts.jsonOut {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			return renderWasm(cmd.OutOrStdout(), report)
		},
	}
	cmd.Flags().StringSliceVar(&privileged, "privileged", nil,
		"name fragments marking a function privileged (default: "+strings.Join(wasm.DefaultPrivilegedNames, ",")+")")
	return cmd
}

// loadWasm reads a local .wasm file, or a deployed contract's code.
func loadWasm(cmd *cobra.Command, opts *options, target string) ([]byte, string, error) {
	if _, err := os.Stat(target); err == nil || strings.HasSuffix(target, ".wasm") {
		code, err := os.ReadFile(target) //nolint:gosec // the operator names the module to read; reading it is the feature.
		if err != nil {
			return nil, "", fmt.Errorf("read wasm: %w", err)
		}
		return code, target, nil
	}
	p, cleanup, err := opts.prober()
	if err != nil {
		return nil, "", err
	}
	defer cleanup()
	code, _, err := p.Code(cmd.Context(), target)
	if errors.Is(err, abi.ErrNoWasm) {
		return nil, "", fmt.Errorf("%s is a Stellar Asset Contract, which is built into the host and has no WASM", target)
	}
	if err != nil {
		return nil, "", err
	}
	return code, target, nil
}

func renderWasm(w io.Writer, r *wasmReport) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	bw.printf("%s %s\n", s.bold("source  "), r.Source)
	bw.printf("%s %s\n", s.bold("hash    "), r.Hash)
	bw.printf("%s %d bytes\n", s.bold("size    "), r.Size)
	if r.Env != nil {
		pre := ""
		if r.Env.PreRelease != 0 {
			pre = fmt.Sprintf(", pre-release %d", r.Env.PreRelease)
		}
		bw.printf("%s protocol %d%s\n", s.bold("env     "), r.Env.Protocol, pre)
	} else {
		bw.printf("%s %s\n", s.bold("env     "), s.dim("no contractenvmetav0 section"))
	}

	if len(r.Meta) > 0 {
		bw.printf("\n%s\n", s.bold("metadata (contractmetav0)"))
		keys := make([]string, 0, len(r.Meta))
		for k := range r.Meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			bw.printf("  %-14s %s\n", k, r.Meta[k])
		}
	}

	bw.printf("\n%s (%d)\n", s.bold("exported functions"), len(r.ExportedFunctions))
	for _, name := range r.ExportedFunctions {
		bw.printf("  %s\n", name)
	}
	bw.printf("\n%s (%d)\n", s.bold("imported host functions"), len(r.HostFunctions))
	for _, name := range r.HostFunctions {
		bw.printf("  %s\n", name)
	}

	bw.printf("\n%s %s\n", s.bold("privileged functions"), s.dim("(heuristic)"))
	if r.PrivilegedNote != "" {
		bw.printf("  %s\n", s.dim("note: "+r.PrivilegedNote))
	}
	if len(r.Privileged) == 0 && r.PrivilegedNote == "" {
		bw.printf("  %s\n", s.dim("no exported function has a privileged-looking name"))
	}
	for _, f := range r.Privileged {
		verdict := f.Verdict
		switch f.Verdict {
		case wasm.VerdictRequiresAuth:
			verdict = s.green(verdict)
		case wasm.VerdictNoAuth:
			verdict = s.red(verdict)
		}
		bw.printf("  %-24s %s\n", f.Function, verdict)
		bw.printf("  %-24s %s\n", "", s.dim(f.Reasoning))
	}
	return bw.err
}

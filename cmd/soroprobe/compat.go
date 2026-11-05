package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar"
)

func newCompatCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "compat <old: contract-id|wasm> <new: contract-id|wasm>",
		Short: "Compare two contract interfaces and report breaking changes",
		Long: `Compare an old contract interface against a new one and list breaking and
non-breaking changes.

Each argument may be either a path to a compiled .wasm module or a deployed
Soroban contract address ("C..."). User-defined types (structs, unions, enums,
error enums) are compared structurally rather than by name alone.

Exits 0 when the new interface is backward-compatible with the old one, 1 when
one or more breaking changes are found, and 2 if an interface could not be
loaded or parsed.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			oldSpec, err := loadCompatTarget(cmd.Context(), opts, args[0])
			if err != nil {
				return fmt.Errorf("loading old interface (%s): %w", args[0], err)
			}
			newSpec, err := loadCompatTarget(cmd.Context(), opts, args[1])
			if err != nil {
				return fmt.Errorf("loading new interface (%s): %w", args[1], err)
			}

			report := abi.CompareSpecs(oldSpec, newSpec)
			if opts.jsonOut {
				if err := writeJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else {
				if err := renderCompat(cmd.OutOrStdout(), args[0], args[1], report); err != nil {
					return err
				}
			}

			if !report.Compatible {
				return &checkFailedError{
					msg: fmt.Sprintf("%d breaking interface change(s) detected", len(report.Breaking)),
				}
			}
			return nil
		},
	}
	return cmd
}

func loadCompatTarget(ctx context.Context, opts *options, target string) (*abi.Spec, error) {
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		raw, err := os.ReadFile(target) //nolint:gosec // CLI operator-supplied path.
		if err != nil {
			return nil, err
		}
		return abi.ParseWasmSpec(raw)
	}
	if _, err := stellar.ParseContractID(target); err == nil {
		log := opts.cfg.Logger(os.Stderr)
		client, err := stellar.New(stellar.Options{
			URL:     opts.cfg.RPCURL,
			Timeout: opts.cfg.Timeout,
			Logger:  log,
		})
		if err != nil {
			return nil, err
		}
		defer func() { _ = client.Close() }()
		return abi.FetchChainSpec(ctx, client, target)
	}
	raw, err := os.ReadFile(target) //nolint:gosec // fallback to file error for clear message
	if err != nil {
		return nil, err
	}
	return abi.ParseWasmSpec(raw)
}

func renderCompat(w io.Writer, oldTarget, newTarget string, report abi.CompatReport) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	bw.printf("%s %s\n", s.bold("old"), oldTarget)
	bw.printf("%s %s\n\n", s.bold("new"), newTarget)

	if len(report.Breaking) > 0 {
		bw.printf("%s (%d)\n", s.red("BREAKING CHANGES"), len(report.Breaking))
		for _, c := range report.Breaking {
			bw.printf("  - [%s] %s\n", c.Category, c.Summary)
		}
		bw.printf("\n")
	}

	if len(report.NonBreaking) > 0 {
		bw.printf("%s (%d)\n", s.bold("non-breaking changes"), len(report.NonBreaking))
		for _, c := range report.NonBreaking {
			bw.printf("  + [%s] %s\n", c.Category, c.Summary)
		}
		bw.printf("\n")
	}

	if len(report.Breaking) == 0 && len(report.NonBreaking) == 0 {
		bw.printf("No interface changes.\n\n")
	}

	bw.printf("%s ", s.bold("result"))
	if report.Compatible {
		bw.printf("%s\n", s.green("COMPATIBLE"))
	} else {
		bw.printf("%s\n", s.red("INCOMPATIBLE"))
	}
	return bw.err
}

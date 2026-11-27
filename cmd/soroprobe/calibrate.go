package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/probe"
)

func newCalibrateCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "calibrate <tx-hash>",
		Short: "Compare a transaction's simulated resources with what it really used",
		Long: `Compare what simulation estimates with what a submitted transaction actually
consumed, to see how much margin a fee strategy needs.

The transaction is fetched with getTransaction, its invocation is simulated
again, and each resource is reported side by side: the estimate, the actual
figure, and the difference as an absolute number and a percentage of the
actual. A positive difference means simulation over-estimates.

The actual figures come from the network's core_metrics diagnostic events
when the transaction carries them, and the resource fee from the fee it was
charged. Without diagnostic events, the limits the transaction declared stand
in, since they bound what it used; the report marks each figure's source.

Simulation runs against current state. When a ledger entry the transaction
touched has changed since it ran, the report says so: the comparison is then
approximate. A transaction older than the RPC's retention window cannot be
fetched.

Nothing is submitted.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, cleanup, err := opts.prober()
			if err != nil {
				return err
			}
			defer cleanup()

			res, err := p.Calibrate(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if opts.jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return renderCalibrate(cmd.OutOrStdout(), res)
		},
	}
}

func renderCalibrate(w io.Writer, r *probe.CalibrateResult) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	bw.printf("%s %s (ledger %d, %s)\n", s.bold("transaction"), r.Hash, r.Ledger, r.Status)
	if r.ContractID != "" {
		bw.printf("%s %s\n", s.bold("contract   "), r.ContractID)
		bw.printf("%s %s\n", s.bold("function   "), r.Function)
	}
	if !r.SimulationSuccess {
		bw.printf("\n%s\n", s.red("the invocation fails when simulated now"))
		renderErrorExplanation(bw, s, r.SimulationErrorExplanation)
		bw.printf("%s\n", indent(r.SimulationError, "  "))
	}

	bw.printf("\n  %-24s %14s %14s %14s %9s  %s\n", "resource", "estimated", "actual", "delta", "delta %", "actual from")
	for _, d := range r.Resources {
		pct := "n/a"
		if d.Percent != nil {
			pct = fmt.Sprintf("%+.1f%%", *d.Percent)
		}
		bw.printf("  %-24s %14d %14d %+14d %9s  %s\n", d.Name, d.Estimated, d.Actual, d.Delta, pct, d.Source)
	}

	if r.StateChanged {
		bw.printf("\n%s\n", s.bold("state changed since the transaction ran:"))
		for _, e := range r.ChangedEntries {
			bw.printf("  %s\n", e)
		}
	}
	for _, n := range r.Notes {
		bw.printf("%s\n", s.dim("note: "+n))
	}
	return bw.err
}

package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/probe"
)

func newLimitsCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "limits",
		Short: "Show the network's current Soroban resource limits",
		Long: `Show the Soroban resource limits the network enforces right now: CPU
instructions, memory, disk reads, writes, footprint entries, events and
transaction size, per transaction and per ledger, plus entry size limits.

The values are read from the ledger's config setting entries, so they are
always the ones validators last voted for, not a copy from documentation.

A setting the network did not return, or one whose type this version of
SoroProbe does not know, is listed rather than causing an error.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, cleanup, err := opts.prober()
			if err != nil {
				return err
			}
			defer cleanup()

			limits, err := p.Limits(cmd.Context())
			if err != nil {
				return err
			}
			if opts.jsonOut {
				return writeJSON(cmd.OutOrStdout(), limits)
			}
			return renderLimits(cmd.OutOrStdout(), limits)
		},
	}
}

func renderLimits(w io.Writer, l *probe.NetworkLimits) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	scope := ""
	for _, limit := range l.Limits {
		if limit.Scope != scope {
			scope = limit.Scope
			bw.printf("\n%s\n", s.bold("per "+scope))
		}
		bw.printf("  %-30s %14d %-12s %s\n", limit.Name, limit.Value, limit.Unit, s.dim(limit.Description))
	}
	for _, name := range l.Missing {
		bw.printf("%s\n", s.dim("note: the network returned no "+name+" setting"))
	}
	for _, name := range l.Unrecognized {
		bw.printf("%s\n", s.dim("note: "+name+" has a value this version of SoroProbe cannot decode"))
	}
	bw.printf("\n%s\n", s.dim(fmt.Sprintf("read at ledger %d", l.LatestLedger)))
	return bw.err
}

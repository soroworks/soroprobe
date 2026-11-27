package main

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/probe"
)

func newTxCmd(opts *options) *cobra.Command {
	return &cobra.Command{
		Use:   "tx <hash>",
		Short: "Explain a submitted transaction: call, result, cost and events",
		Long: `Fetch a submitted transaction with getTransaction and explain it the way
simulate explains a dry run: which contract function ran with which
arguments, what it returned, the resources it declared and the fees it was
charged, and the contract events it emitted.

A failed transaction shows its result code, the invokeHostFunction result
and the host or contract error from its diagnostic events.

The RPC only keeps recent ledgers (about a week by default); an older
transaction gives a clear error naming the window the RPC covers.

--json prints the same report as JSON.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, cleanup, err := opts.prober()
			if err != nil {
				return err
			}
			defer cleanup()

			report, err := p.Tx(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if opts.jsonOut {
				return writeJSON(cmd.OutOrStdout(), report)
			}
			return renderTx(cmd.OutOrStdout(), report)
		},
	}
}

func renderTx(w io.Writer, r *probe.TxReport) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	bw.printf("%s %s\n", s.bold("transaction"), r.Hash)
	when := ""
	if !r.ClosedAt.IsZero() {
		when = ", " + r.ClosedAt.Format("2006-01-02 15:04:05 UTC")
	}
	bw.printf("%s %d%s\n", s.bold("ledger     "), r.Ledger, when)
	src := r.Source
	if r.FeeBump {
		src += s.dim(" (fee bump)")
	}
	bw.printf("%s %s\n", s.bold("source     "), src)

	if r.ContractID != "" {
		bw.printf("%s %s\n", s.bold("contract   "), r.ContractID)
		fn := r.Function
		if r.Signature != "" {
			fn = r.Signature
		}
		bw.printf("%s %s\n", s.bold("function   "), fn)
		if len(r.Args) > 0 {
			args, err := json.Marshal(r.Args)
			if err != nil {
				return err
			}
			bw.printf("%s %s\n", s.bold("args       "), args)
		}
	} else {
		bw.printf("%s %s\n", s.bold("operation  "), r.HostFunction)
	}
	bw.printf("\n")

	if r.Failure == nil {
		bw.printf("%s\n", s.green("SUCCESS"))
		if r.ReturnValue != nil {
			value, err := json.Marshal(r.ReturnValue)
			if err != nil {
				return err
			}
			bw.printf("\n%s\n  %s\n", s.bold("return value"), value)
		}
	} else {
		bw.printf("%s %s\n", s.red("FAILED "), r.Failure.Result)
		if r.Failure.Operation != "" {
			bw.printf("  operation result  %s\n", r.Failure.Operation)
		}
		if r.Failure.Error != "" {
			bw.printf("  error             %s\n", r.Failure.Error)
		}
	}

	if res := r.Resources; res != nil {
		bw.printf("\n%s\n", s.bold("resources (declared)"))
		bw.printf("  cpu instructions   %d\n", res.Instructions)
		bw.printf("  disk read bytes    %d\n", res.DiskReadBytes)
		bw.printf("  write bytes        %d\n", res.WriteBytes)
		bw.printf("  footprint          %d read-only, %d read-write\n", res.ReadOnlyEntries, res.ReadWriteEntries)
		bw.printf("  resource fee       %s offered", stroops(res.ResourceFee))
		if res.ResourceFeeCharged != nil {
			bw.printf(", %s charged", stroops(*res.ResourceFeeCharged))
		}
		bw.printf("\n  fee charged        %s\n", stroops(res.FeeCharged))
	}

	if len(r.Events) > 0 {
		bw.printf("\n%s (%d)\n", s.bold("contract events"), len(r.Events))
		for _, ev := range r.Events {
			topics, err := json.Marshal(ev.Topics)
			if err != nil {
				return err
			}
			data, err := json.Marshal(ev.Data)
			if err != nil {
				return err
			}
			bw.printf("  %s %s\n    %s\n", s.cyan(ev.ContractID), topics, strings.TrimSpace(fmt.Sprint(string(data))))
		}
	}
	return bw.err
}

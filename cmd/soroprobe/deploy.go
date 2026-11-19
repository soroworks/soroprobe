package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/probe"
)

func newEstimateDeployCmd(opts *options) *cobra.Command {
	var source string

	cmd := &cobra.Command{
		Use:   "estimate-deploy <contract.wasm> [constructor-arg...]",
		Short: "Price uploading and instantiating a contract, without deploying it",
		Long: `Price a deployment before running it: simulate uploading the WASM and
instantiating it, and report the resources and fees of each step.

Constructor arguments follow the file, in the syntax simulate takes. They are
typed from the WASM's own interface, so a bare "5" becomes whatever the
constructor declares.

A WASM that is already on chain is detected by its hash, and its upload is
shown as costing nothing. The size is checked against the network's contract
size limit.

The new contract's address depends on the deploying account (--source,
default the configured source account) and a salt. A random salt is used, so
the reported address is an example: a real deploy gets a different one.

Instantiating needs the WASM on chain, so for a new WASM the network may not
be able to price that step until the upload has happened; the report says so.

Nothing is ever submitted, and no secret key is needed.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			wasm, err := os.ReadFile(args[0]) //nolint:gosec // the path is the operator's own file; reading it is the feature.
			if err != nil {
				return fmt.Errorf("read wasm: %w", err)
			}

			p, cleanup, err := opts.prober()
			if err != nil {
				return err
			}
			defer cleanup()

			res, err := p.EstimateDeploy(cmd.Context(), probe.DeployEstimateRequest{
				Wasm:            wasm,
				Deployer:        source,
				ConstructorArgs: args[1:],
			})
			if err != nil {
				return err
			}
			if opts.jsonOut {
				return writeJSON(cmd.OutOrStdout(), res)
			}
			return renderDeployEstimate(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&source, "source", "", "account that would deploy the contract (default the configured source account)")
	return cmd
}

func renderDeployEstimate(w io.Writer, r *probe.DeployEstimate) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	bw.printf("%s %s\n", s.bold("wasm    "), r.WasmHash)
	switch {
	case r.FitsSizeLimit == nil:
		bw.printf("%s %d bytes %s\n", s.bold("size    "), r.WasmSize, s.dim("(network limit unknown)"))
	case *r.FitsSizeLimit:
		bw.printf("%s %d bytes, within the %d-byte limit\n", s.bold("size    "), r.WasmSize, r.MaxWasmSize)
	default:
		bw.printf("%s %d bytes %s\n", s.bold("size    "), r.WasmSize, s.red(fmt.Sprintf("exceeds the %d-byte limit", r.MaxWasmSize)))
	}
	bw.printf("%s %s\n", s.bold("contract"), r.ContractID)
	bw.printf("%s\n", s.dim(fmt.Sprintf("  example address: deployer %s, salt %s; a real deploy gets a different one", r.Deployer, r.Salt)))
	if r.Constructor != "" {
		bw.printf("%s %s\n", s.bold("ctor    "), r.Constructor)
	}
	if r.ABINote != "" {
		bw.printf("%s\n", s.dim("note: "+r.ABINote))
	}

	renderDeployStep(bw, s, "upload", r.Upload)
	renderDeployStep(bw, s, "instantiate", r.Instantiate)

	bw.printf("\n%s %d stroops\n", s.bold("total estimated fee"), r.TotalEstimatedFee)
	bw.printf("%s\n", s.dim("nothing was submitted"))
	return bw.err
}

func renderDeployStep(bw *bufWriter, s styler, name string, step probe.DeployStep) {
	bw.printf("\n%s\n", s.bold(name))
	switch {
	case !step.Simulated:
		bw.printf("  %s\n", s.green("SKIPPED"))
	case step.Success:
		bw.printf("  %s\n", s.green("OK"))
	default:
		bw.printf("  %s\n", s.red("FAILED"))
		renderErrorExplanation(bw, s, step.ErrorExplanation)
		bw.printf("%s\n", indent(step.Error, "  "))
	}
	if step.Simulated {
		c := step.Cost
		bw.printf("  cpu instructions  %d\n", c.Instructions)
		bw.printf("  disk read bytes   %d\n", c.DiskReadBytes)
		bw.printf("  write bytes       %d\n", c.WriteBytes)
		bw.printf("  resource fee      %d stroops\n", c.ResourceFee)
		bw.printf("  estimated fee     %d stroops\n", c.EstimatedTotalFee)
	}
	if step.Note != "" {
		bw.printf("  %s\n", s.dim("note: "+step.Note))
	}
}

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"github.com/stellar/go-stellar-sdk/network"

	"github.com/soroworks/soroprobe/internal/config"
	"github.com/soroworks/soroprobe/internal/probe"
)

func newCompareCmd(opts *options) *cobra.Command {
	var testnetID string
	var mainnetID string
	var net1 string
	var contract1 string
	var net2 string
	var contract2 string
	var function string
	var callArgs []string
	var format string
	var quiet bool

	cmd := &cobra.Command{
		Use:   "compare (--testnet <id> --mainnet <id> | --net1 <net> --contract1 <id> --net2 <net> --contract2 <id>)",
		Short: "Compare the same contract across networks, highlighting differences",
		Long: `Compare a contract deployed across two networks (such as testnet and mainnet).

Reports WASM hash equality, per-entry TTL health comparison, and read-only simulation results.
The exit code reflects whether the WASMs match:
  0: WASM hashes match across both networks
  1: WASM hashes differ (or state mismatch)
  2: Could not execute comparison (network or configuration error)
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Resolve network and contract pairs
			targetNet1, targetID1 := net1, contract1
			targetNet2, targetID2 := net2, contract2

			if testnetID != "" || mainnetID != "" {
				if testnetID == "" || mainnetID == "" {
					return fmt.Errorf("both --testnet <id> and --mainnet <id> are required when comparing testnet to mainnet")
				}
				targetNet1, targetID1 = "testnet", testnetID
				targetNet2, targetID2 = "mainnet", mainnetID
			}

			if targetID1 == "" || targetID2 == "" {
				return fmt.Errorf("give --testnet <id> and --mainnet <id>, or provide two networks via --contract1 and --contract2")
			}
			if targetNet1 == "" {
				targetNet1 = "testnet"
			}
			if targetNet2 == "" {
				targetNet2 = "mainnet"
			}

			p1, cleanup1, err := resolveProberFor(opts, targetNet1)
			if err != nil {
				return err
			}
			defer cleanup1()

			p2, cleanup2, err := resolveProberFor(opts, targetNet2)
			if err != nil {
				return err
			}
			defer cleanup2()

			res, err := probe.Compare(cmd.Context(), probe.CompareRequest{
				NetworkA:  targetNet1,
				ContractA: targetID1,
				ProberA:   p1,
				NetworkB:  targetNet2,
				ContractB: targetID2,
				ProberB:   p2,
				Function:  function,
				Args:      callArgs,
			})
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if !quiet {
				switch format {
				case "json":
					if err := writeJSON(out, res); err != nil {
						return err
					}
				case "markdown":
					renderMarkdownCompare(out, res)
				default:
					renderTextCompare(out, res)
				}
			}

			if !res.WasmMatch {
				return &checkFailedError{
					msg:  fmt.Sprintf("WASM mismatch between %s and %s", targetNet1, targetNet2),
					code: 1,
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&testnetID, "testnet", "", "contract ID deployed on testnet")
	cmd.Flags().StringVar(&mainnetID, "mainnet", "", "contract ID deployed on mainnet")
	cmd.Flags().StringVar(&net1, "net1", "testnet", "first network or profile name")
	cmd.Flags().StringVar(&contract1, "contract1", "", "contract ID on first network")
	cmd.Flags().StringVar(&net2, "net2", "mainnet", "second network or profile name")
	cmd.Flags().StringVar(&contract2, "contract2", "", "contract ID on second network")
	cmd.Flags().StringVar(&function, "fn", "", "optional read-only function to simulate on both networks")
	cmd.Flags().StringArrayVar(&callArgs, "arg", nil, "arguments for --fn (repeatable)")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, json, or markdown")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress output; exit code reflects whether WASMs match")

	return cmd
}

func resolveProberFor(opts *options, netOrProfile string) (*probe.Prober, func(), error) {
	cfg := opts.cfg
	if opts.configPath != "" || netOrProfile != "" {
		if pcfg, err := config.LoadWithProfile(opts.configPath, netOrProfile); err == nil {
			cfg = pcfg
		} else {
			switch strings.ToLower(netOrProfile) {
			case "testnet":
				cfg.RPCURL = config.DefaultRPCURL
				cfg.NetworkPassphrase = network.TestNetworkPassphrase
			case "mainnet", "public":
				cfg.RPCURL = "https://mainnet.sorobanrpc.com"
				cfg.NetworkPassphrase = network.PublicNetworkPassphrase
			case "futurenet":
				cfg.RPCURL = "https://rpc-futurenet.stellar.org"
				cfg.NetworkPassphrase = "Test SDF Future Network ; October 2022"
			}
		}
	}

	if opts.newProber != nil {
		return opts.newProber(cfg)
	}
	return newLiveProber(cfg, opts.abiFromChain)
}

func renderTextCompare(w io.Writer, r *probe.CompareResult) {
	fmt.Fprintf(w, "Cross-Network Contract Comparison:
")
	fmt.Fprintf(w, "  %s: %s
", r.NetworkA, r.ContractA)
	fmt.Fprintf(w, "  %s: %s

", r.NetworkB, r.ContractB)

	if r.WasmMatch {
		fmt.Fprintf(w, "WASM Code: MATCH (%s)
", r.WasmHashA)
	} else {
		fmt.Fprintf(w, "WASM Code: MISMATCH
")
		fmt.Fprintf(w, "  %s: %s
", r.NetworkA, r.WasmHashA)
		fmt.Fprintf(w, "  %s: %s
", r.NetworkB, r.WasmHashB)
	}
	fmt.Fprintln(w)

	if len(r.TTLDiffs) > 0 {
		fmt.Fprintf(w, "TTL Comparison:
")
		for _, diff := range r.TTLDiffs {
			label := diff.Kind
			if diff.Key != "" {
				label += ":" + diff.Key
			}
			fmt.Fprintf(w, "  %-12s %s=%d (%s), %s=%d (%s) [diff: %d ledgers]
",
				label, r.NetworkA, diff.LedgersA, diff.StatusA, r.NetworkB, diff.LedgersB, diff.StatusB, diff.DiffLedgers)
		}
		fmt.Fprintln(w)
	}

	if r.SimulateMatch != nil {
		status := "MISMATCH"
		if *r.SimulateMatch {
			status = "MATCH"
		}
		fmt.Fprintf(w, "Simulation: %s
", status)
		if r.SimulateA != nil {
			fmt.Fprintf(w, "  %s result: %s
", r.NetworkA, r.SimulateA.Result)
		}
		if r.SimulateB != nil {
			fmt.Fprintf(w, "  %s result: %s
", r.NetworkB, r.SimulateB.Result)
		}
		fmt.Fprintln(w)
	}

	if len(r.Differences) > 0 {
		fmt.Fprintf(w, "Differences Detected (%d):
", len(r.Differences))
		for _, d := range r.Differences {
			fmt.Fprintf(w, "  - %s
", d)
		}
	} else {
		fmt.Fprintf(w, "No differences detected across networks.
")
	}
}

func renderMarkdownCompare(w io.Writer, r *probe.CompareResult) {
	fmt.Fprintf(w, "## Cross-Network Contract Comparison

")
	fmt.Fprintf(w, "| Parameter | %s | %s |
", r.NetworkA, r.NetworkB)
	fmt.Fprintf(w, "| :--- | :--- | :--- |
")
	fmt.Fprintf(w, "| Contract ID | `%s` | `%s` |
", r.ContractA, r.ContractB)
	fmt.Fprintf(w, "| WASM Hash | `%s` | `%s` |
", r.WasmHashA, r.WasmHashB)
	for _, diff := range r.TTLDiffs {
		fmt.Fprintf(w, "| TTL (%s) | %d ledgers (%s) | %d ledgers (%s) |
", diff.Kind, diff.LedgersA, diff.StatusA, diff.LedgersB, diff.StatusB)
	}
	fmt.Fprintln(w)
	if r.WasmMatch {
		fmt.Fprintf(w, "**WASM Match:** Yes

")
	} else {
		fmt.Fprintf(w, "**WASM Match:** No

")
	}
}

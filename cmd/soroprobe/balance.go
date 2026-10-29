package main

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/probe"
)

func newBalanceCmd(opts *options) *cobra.Command {
	var filePath string
	var format string
	var quiet bool

	cmd := &cobra.Command{
		Use:   "balance <token-id> [<holder-address>...]",
		Short: "Query token balances for one or more holder addresses",
		Long: `Read and format token balances from a Soroban SEP-41 token or Stellar Asset Contract (SAC).

Queries the token's decimals and symbol and formats balances into human-readable amounts.
Accepts accounts (G...) and contract addresses (C...). Multiple addresses can be passed as
arguments or loaded from a file using --file.

Examples:
  soroprobe balance CDLZFC... GAAAAAAAAAAAAAAAA...
  soroprobe balance CDLZFC... --file holders.txt
  soroprobe balance CDLZFC... GAAA... GBBB... --json
`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 1 {
				return fmt.Errorf("token contract ID is required")
			}
			if len(args) == 1 && filePath == "" {
				return fmt.Errorf("give at least one holder address or pass --file")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			tokenID := args[0]
			var holders []string

			if len(args) > 1 {
				holders = append(holders, args[1:]...)
			}

			if filePath != "" {
				f, err := os.Open(filePath)
				if err != nil {
					return fmt.Errorf("open holders file: %w", err)
				}
				defer f.Close()

				fileHolders, err := probe.ReadHoldersFromFile(f)
				if err != nil {
					return fmt.Errorf("read holders file: %w", err)
				}
				holders = append(holders, fileHolders...)
			}

			if len(holders) == 0 {
				return fmt.Errorf("no holder addresses provided")
			}

			p, cleanup, err := opts.prober()
			if err != nil {
				return err
			}
			defer cleanup()

			report, err := p.GetTokenBalances(cmd.Context(), tokenID, holders)
			if err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			if !quiet {
				switch format {
				case "json":
					if err := writeJSON(out, report); err != nil {
						return err
					}
				case "markdown":
					renderMarkdownBalances(out, report)
				default:
					renderTextBalances(out, report)
				}
			}

			// If any balance failed to read, exit non-zero
			for _, h := range report.Holders {
				if h.Error != "" {
					return &checkFailedError{msg: fmt.Sprintf("failed to read balance for %s: %s", h.Address, h.Error), code: 1}
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVarP(&filePath, "file", "f", "", "path to file containing holder addresses, one per line")
	cmd.Flags().StringVar(&format, "format", "text", "output format: text, json, or markdown")
	cmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "suppress output; exit code reflects success")

	return cmd
}

func renderTextBalances(w io.Writer, r *probe.TokenBalancesReport) {
	fmt.Fprintf(w, "Token:    %s
", r.Token.ContractID)
	fmt.Fprintf(w, "Symbol:   %s
", r.Token.Symbol)
	fmt.Fprintf(w, "Decimals: %d

", r.Token.Decimals)

	fmt.Fprintf(w, "%-58s %s
", "HOLDER ADDRESS", "BALANCE")
	fmt.Fprintf(w, "%s
", strings.Repeat("-", 76))
	for _, h := range r.Holders {
		if h.Error != "" {
			fmt.Fprintf(w, "%-58s ERROR: %s
", h.Address, h.Error)
		} else {
			fmt.Fprintf(w, "%-58s %s
", h.Address, h.FormattedAmount)
		}
	}
}

func renderMarkdownBalances(w io.Writer, r *probe.TokenBalancesReport) {
	fmt.Fprintf(w, "### Token Balances: %s (%s)

", r.Token.Symbol, r.Token.ContractID)
	fmt.Fprintf(w, "| Holder Address | Balance | Raw Amount |
")
	fmt.Fprintf(w, "| :--- | :--- | :--- |
")
	for _, h := range r.Holders {
		if h.Error != "" {
			fmt.Fprintf(w, "| `%s` | *Error* | `%s` |
", h.Address, h.Error)
		} else {
			fmt.Fprintf(w, "| `%s` | **%s** | `%s` |
", h.Address, h.FormattedAmount, h.RawBalance)
		}
	}
}

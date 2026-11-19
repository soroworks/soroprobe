package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/scval"
	"github.com/soroworks/soroprobe/internal/xdrjson"
)

func newDecodeCmd() *cobra.Command {
	var typeList strings.Builder
	for _, t := range xdrjson.Types() {
		fmt.Fprintf(&typeList, "  %-18s %s\n", t[0], t[1])
	}

	return &cobra.Command{
		Use:   "decode [type] <base64>",
		Short: "Decode base64 Stellar XDR into readable JSON",
		Long: `Decode a base64 Stellar XDR blob into readable JSON.

<type> is one of:

` + typeList.String() + `
Leave the type out, or pass "auto", to have it worked out: the blob is tried
as each type, and decoded as the one it fits. A blob that fits more than one
is reported as ambiguous instead of guessed at; name the type then. "-" reads
the blob from standard input.

Hashes and byte strings print as hex, accounts and contracts as strkeys, enums
by name and ScVals in the same notation simulate uses for return values.

Decoding happens locally. Nothing is sent to the network.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			typeName, blob := xdrjson.TypeAuto, args[0]
			if len(args) == 2 {
				typeName, blob = args[0], args[1]
			}
			if blob == "-" {
				raw, err := io.ReadAll(cmd.InOrStdin())
				if err != nil {
					return fmt.Errorf("read xdr from stdin: %w", err)
				}
				blob = string(raw)
			}
			decoded, err := xdrjson.Decode(typeName, blob)
			if err != nil {
				return err
			}
			return writeJSON(cmd.OutOrStdout(), decoded)
		},
	}
}

func newEncodeCmd(opts *options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "encode",
		Short: "Encode values as base64 Stellar XDR",
	}

	scvalCmd := &cobra.Command{
		Use:   "scval <value>",
		Short: "Encode a typed argument as base64 ScVal XDR",
		Long: `Encode a value written in SoroProbe's argument syntax as base64 ScVal XDR.

The syntax is the one simulate takes: "u32:5", "i128:-10", "sym:transfer",
"addr:G...", "bytes:00ff", and vec: and map: with JSON-like literals of
further values. An untyped literal is inferred the way simulate infers it.

--json prints the value and its XDR together.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			encoded, err := xdrjson.EncodeScVal(scval.NewRegistry(), args[0])
			if err != nil {
				return err
			}
			if opts.jsonOut {
				return writeJSON(cmd.OutOrStdout(), map[string]string{"value": args[0], "xdr": encoded})
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), encoded)
			return err
		},
	}
	cmd.AddCommand(scvalCmd)
	return cmd
}

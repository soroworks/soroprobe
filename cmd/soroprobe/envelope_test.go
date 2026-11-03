package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/config"
	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func buildTestDecimalsEnvelope(t *testing.T) string {
	t.Helper()

	contract, err := stellar.ParseContractID(stellartest.SACContract)
	require.NoError(t, err)
	u32Val := xdr.Uint32(7)
	env, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: stellartest.SourceAccount,
		Contract:      contract,
		Function:      "decimals",
		Args:          []xdr.ScVal{{Type: xdr.ScValTypeScvU32, U32: &u32Val}},
	})
	require.NoError(t, err)
	return env
}

func TestSimulateEnvelopeFlagEndToEnd(t *testing.T) {
	fake := stellartest.NewFake(t)
	env := buildTestDecimalsEnvelope(t)

	out, err := runWithFake(t, fake, "simulate", "--envelope", env)
	require.NoError(t, err)
	assert.Contains(t, out, stellartest.SACContract)
	assert.Contains(t, out, "decimals")
	assert.Contains(t, out, "arguments")
	assert.Contains(t, out, "u32: 7")
	assert.Contains(t, out, "SUCCESS")
	require.Len(t, fake.SimulateRequests, 1)
	assert.Equal(t, env, fake.SimulateRequests[0].Transaction)

	jsonOut, err := runWithFake(t, fake, "--json", "simulate", "--envelope", env)
	require.NoError(t, err)
	var result probe.SimulateResult
	require.NoError(t, json.Unmarshal([]byte(jsonOut), &result))
	assert.Equal(t, stellartest.SACContract, result.ContractID)
	assert.Equal(t, "decimals", result.Function)
	require.Len(t, result.NamedArgs, 1)
	assert.Equal(t, "u32", result.NamedArgs[0].Type)
}

func TestSimulateEnvelopeFileAndStdinEndToEnd(t *testing.T) {
	fake := stellartest.NewFake(t)
	env := buildTestDecimalsEnvelope(t)
	path := writeTempFile(t, "tx.xdr", env+"\n")

	out, err := runWithFake(t, fake, "simulate", "--envelope-file", path)
	require.NoError(t, err)
	assert.Contains(t, out, "SUCCESS")
	require.Len(t, fake.SimulateRequests, 1)
	assert.Equal(t, env, fake.SimulateRequests[0].Transaction)

	t.Chdir(t.TempDir())
	cmd, opts := newRootCmd()
	opts.newProber = func(cfg config.Config) (*probe.Prober, func(), error) {
		p, newErr := probe.New(probe.Options{
			Client:        fake,
			SourceAccount: cfg.SourceAccount,
			Thresholds:    health.Thresholds{Warn: cfg.WarnLedgers, Critical: cfg.CriticalLedgers},
		})
		return p, func() {}, newErr
	}
	var stdout bytes.Buffer
	cmd.SetIn(strings.NewReader(env + "\n"))
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs([]string{"simulate", "--envelope-file", "-"})
	require.NoError(t, cmd.Execute())
	assert.Contains(t, stdout.String(), "SUCCESS")
}

func TestSimulateEnvelopeRejectsNonSorobanAndMultiOpEnvelopes(t *testing.T) {
	fake := stellartest.NewFake(t)
	sourceAcct := xdr.MustAddress(stellartest.SourceAccount)

	paymentOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypePayment,
			PaymentOp: &xdr.PaymentOp{
				Destination: sourceAcct.ToMuxedAccount(),
				Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
				Amount:      100,
			},
		},
	}

	buildEnv := func(ops ...xdr.Operation) string {
		txEnv := xdr.TransactionEnvelope{
			Type: xdr.EnvelopeTypeEnvelopeTypeTx,
			V1: &xdr.TransactionV1Envelope{
				Tx: xdr.Transaction{
					SourceAccount: sourceAcct.ToMuxedAccount(),
					Fee:           100,
					SeqNum:        1,
					Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
					Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
					Operations:    ops,
				},
			},
		}
		b64, err := xdr.MarshalBase64(txEnv)
		require.NoError(t, err)
		return b64
	}

	_, err := runWithFake(t, fake, "simulate", "--envelope", buildEnv(paymentOp))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "non-Soroban operation OperationTypePayment")

	_, err = runWithFake(t, fake, "simulate", "--envelope", buildEnv(paymentOp, paymentOp))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "multi-operation transactions are not supported (got 2 operations)")
}

func TestSimulateEnvelopeRejectsConflictingFlags(t *testing.T) {
	fake := stellartest.NewFake(t)
	env := buildTestDecimalsEnvelope(t)
	path := writeTempFile(t, "tx.xdr", env)

	tests := map[string][]string{
		"envelope and envelope-file": {"simulate", "--envelope", env, "--envelope-file", path},
		"envelope and positional":    {"simulate", "--envelope", env, stellartest.SACContract, "decimals"},
		"envelope and batch file":    {"simulate", "--envelope", env, "--file", path},
	}

	for name, args := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := runWithFake(t, fake, args...)
			require.Error(t, err)
		})
	}
}

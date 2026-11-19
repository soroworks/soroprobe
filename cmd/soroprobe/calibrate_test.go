package main

import (
	"encoding/json"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

const calibrateHash = "ffeeddccbbaa99887766554433221100ffeeddccbbaa99887766554433221100"

func calibrateFake(t *testing.T) *stellartest.Fake {
	t.Helper()
	fake := stellartest.NewFake(t)
	contract, err := stellar.ParseContractID(stellartest.WasmContract)
	require.NoError(t, err)
	b64, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: stellartest.SourceAccount, Contract: contract, Function: "decimals",
	})
	require.NoError(t, err)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &env))
	env.V1.Tx.Ext = xdr.TransactionExt{V: 1, SorobanData: &xdr.SorobanTransactionData{
		Resources: xdr.SorobanResources{
			Footprint:    xdr.LedgerFootprint{ReadOnly: []xdr.LedgerKey{stellar.InstanceKey(contract)}, ReadWrite: []xdr.LedgerKey{}},
			Instructions: 3_000_000,
		},
		ResourceFee: 70_000,
	}}
	envXDR, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	fake.Transactions = map[string]protocol.GetTransactionResponse{calibrateHash: {
		LatestLedger: fake.LatestLedger,
		TransactionDetails: protocol.TransactionDetails{
			Status: protocol.TransactionStatusSuccess, EnvelopeXDR: envXDR, Ledger: fake.LatestLedger,
		},
	}}
	return fake
}

func TestCalibrateCommandText(t *testing.T) {
	out, err := runWithFake(t, calibrateFake(t), "calibrate", calibrateHash)
	require.NoError(t, err)
	for _, want := range []string{"cpu instructions", "resource fee (stroops)", "declared", "delta %", "decimals"} {
		assert.Contains(t, out, want)
	}
}

func TestCalibrateCommandJSON(t *testing.T) {
	out, err := runWithFake(t, calibrateFake(t), "--json", "calibrate", calibrateHash)
	require.NoError(t, err)
	var res probe.CalibrateResult
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.Len(t, res.Resources, 4)
	assert.Equal(t, calibrateHash, res.Hash)
}

func TestCalibrateCommandUnknownHash(t *testing.T) {
	_, err := runWithFake(t, calibrateFake(t), "calibrate", "00")
	require.ErrorIs(t, err, probe.ErrTransactionNotFound)
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestWasmCommandOnADeployedContract(t *testing.T) {
	out, err := runWithFake(t, stellartest.NewFake(t), "wasm", "--privileged", "set_price", stellartest.WasmContract)
	require.NoError(t, err)
	for _, want := range []string{"protocol 22", "rssdkver", "exported functions", "set_price", "a.0 (require_auth)", "requires_auth", "heuristic"} {
		assert.Contains(t, out, want)
	}
}

func TestWasmCommandJSONAndFile(t *testing.T) {
	fake := stellartest.NewFake(t)
	out, err := runWithFake(t, fake, "--json", "wasm", stellartest.WasmContract)
	require.NoError(t, err)
	var report struct {
		Hash       string   `json:"hash"`
		Size       int      `json:"size_bytes"`
		Exported   []string `json:"exported_functions"`
		Privileged []any    `json:"privileged"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &report))
	assert.Len(t, report.Hash, 64)
	assert.Positive(t, report.Size)
	assert.Contains(t, report.Exported, "get_admin")

	path := filepath.Join(t.TempDir(), "bad.wasm")
	require.NoError(t, os.WriteFile(path, []byte("not wasm"), 0o600))
	_, err = runWithFake(t, fake, "wasm", path)
	require.ErrorContains(t, err, "malformed wasm")

	_, err = runWithFake(t, fake, "wasm", stellartest.SACContract)
	require.ErrorContains(t, err, "Stellar Asset Contract")
}

func TestTxCommand(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.Transactions = map[string]protocol.GetTransactionResponse{}
	var failed protocol.GetTransactionResponse
	stellartest.Load(t, "transaction_failed.json", &failed)
	fake.Transactions[failed.TransactionHash] = failed

	out, err := runWithFake(t, fake, "tx", failed.TransactionHash)
	require.NoError(t, err)
	for _, want := range []string{"FAILED", "Trapped", "outside of the footprint", "update_price_feeds", "fee charged"} {
		assert.Contains(t, out, want)
	}

	out, err = runWithFake(t, fake, "--json", "tx", failed.TransactionHash)
	require.NoError(t, err)
	var r probe.TxReport
	require.NoError(t, json.Unmarshal([]byte(out), &r))
	assert.Equal(t, "FAILED", r.Status)

	_, err = runWithFake(t, fake, "tx", "00000000000000000000000000000000000000000000000000000000000000ff")
	require.ErrorIs(t, err, probe.ErrTxNotFound)
}

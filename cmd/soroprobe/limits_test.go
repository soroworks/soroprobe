package main

import (
	"encoding/json"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func limitsFake(t *testing.T, txInstructions int64) *stellartest.Fake {
	t.Helper()
	fake := stellartest.NewFake(t)
	fake.StoreEntry(t, stellar.SettingKey(xdr.ConfigSettingIdConfigSettingContractComputeV0), xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeConfigSetting,
		ConfigSetting: &xdr.ConfigSettingEntry{
			ConfigSettingId: xdr.ConfigSettingIdConfigSettingContractComputeV0,
			ContractCompute: &xdr.ConfigSettingContractComputeV0{
				LedgerMaxInstructions: 500_000_000, TxMaxInstructions: xdr.Int64(txInstructions), TxMemoryLimit: 41_943_040,
			},
		},
	})
	return fake
}

func TestLimitsCommandText(t *testing.T) {
	out, err := runWithFake(t, limitsFake(t, 100_000_000), "limits")
	require.NoError(t, err)
	for _, want := range []string{"per transaction", "tx_max_instructions", "100000000", "instructions", "per ledger",
		"returned no ConfigSettingIdConfigSettingContractLedgerCostV0 setting"} {
		assert.Contains(t, out, want)
	}
}

func TestLimitsCommandJSON(t *testing.T) {
	out, err := runWithFake(t, limitsFake(t, 100_000_000), "--json", "limits")
	require.NoError(t, err)
	var got probe.NetworkLimits
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	v, ok := got.Get(probe.LimitTxInstructions)
	assert.True(t, ok)
	assert.Equal(t, int64(100_000_000), v)
}

func TestSimulateShowsLimitsFeesAndHeadroom(t *testing.T) {
	// decimals uses 127,569 instructions: 85% of a 150,000 limit.
	fake := limitsFake(t, 150_000)
	out, err := runWithFake(t, fake, "simulate", stellartest.SACContract, "decimals")
	require.NoError(t, err)
	for _, want := range []string{"share of network limits", "cpu instructions", "85.0%", "WARN", "recommended fees", "median", "XLM", "getFeeStats"} {
		assert.Contains(t, out, want)
	}

	out, err = runWithFake(t, limitsFake(t, 150_000), "simulate", "--headroom", "90", stellartest.SACContract, "decimals")
	require.NoError(t, err)
	assert.NotContains(t, out, "WARN")
}

func TestSimulateSourceFlag(t *testing.T) {
	kp := keypair.MustRandom()
	_, err := runWithFake(t, stellartest.NewFake(t), "simulate", "--source", kp.Address(), stellartest.SACContract, "decimals")
	require.ErrorIs(t, err, probe.ErrAccountNotFound)

	_, err = runWithFake(t, stellartest.NewFake(t), "simulate", "--source", kp.Seed(), stellartest.SACContract, "decimals")
	require.ErrorIs(t, err, probe.ErrSecretKey)
}

func TestCheckHeadroomFlag(t *testing.T) {
	out, err := runWithFake(t, limitsFake(t, 150_000), "check", "--fn", "decimals", "--headroom", "50", stellartest.SACContract)
	require.NoError(t, err)
	assert.Contains(t, out, "headroom")
}

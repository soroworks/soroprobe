package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestCompareCmd_MatchingWasm(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	out, err := runWithFake(t, fake, "compare", "--testnet", stellartest.WasmContract, "--mainnet", stellartest.WasmContract)
	require.NoError(t, err)
	assert.Contains(t, out, "WASM Code: MATCH")
	assert.Contains(t, out, "No differences detected")
}

func TestCompareCmd_MismatchingContracts(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	out, err := runWithFake(t, fake, "compare", "--testnet", stellartest.WasmContract, "--mainnet", stellartest.SACContract)
	require.Error(t, err)
	assert.Contains(t, out, "WASM Code: MISMATCH")
	assert.Contains(t, out, "Differences Detected")
}

func TestCompareCmd_MissingFlags(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	_, err := runWithFake(t, fake, "compare", "--testnet", stellartest.WasmContract)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "both --testnet <id> and --mainnet <id> are required")
}

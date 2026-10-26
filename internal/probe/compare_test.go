package probe_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestCompare_WasmMatch(t *testing.T) {
	t.Parallel()

	fakeA := stellartest.NewFake(t)
	fakeB := stellartest.NewFake(t)

	pA, err := probe.New(probe.Options{
		Client:        fakeA,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	pB, err := probe.New(probe.Options{
		Client:        fakeB,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := probe.Compare(context.Background(), probe.CompareRequest{
		NetworkA:  "testnet",
		ContractA: stellartest.WasmContract,
		ProberA:   pA,
		NetworkB:  "mainnet",
		ContractB: stellartest.WasmContract,
		ProberB:   pB,
	})
	require.NoError(t, err)

	assert.True(t, res.WasmMatch)
	assert.NotEmpty(t, res.WasmHashA)
	assert.Equal(t, res.WasmHashA, res.WasmHashB)
	assert.NotEmpty(t, res.TTLDiffs)
	assert.Empty(t, res.Differences)
}

func TestCompare_SimulationMatch(t *testing.T) {
	t.Parallel()

	fakeA := stellartest.NewFake(t)
	fakeB := stellartest.NewFake(t)

	pA, err := probe.New(probe.Options{
		Client:        fakeA,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	pB, err := probe.New(probe.Options{
		Client:        fakeB,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := probe.Compare(context.Background(), probe.CompareRequest{
		NetworkA:  "testnet",
		ContractA: stellartest.SACContract,
		ProberA:   pA,
		NetworkB:  "mainnet",
		ContractB: stellartest.SACContract,
		ProberB:   pB,
		Function:  "decimals",
	})
	require.NoError(t, err)

	require.NotNil(t, res.SimulateMatch)
	assert.True(t, *res.SimulateMatch)
	assert.Equal(t, res.SimulateA.Result, res.SimulateB.Result)
}

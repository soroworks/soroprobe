package probe_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

type mockVerifier struct {
	hash string
	err  error
}

func (m *mockVerifier) Interface(_ context.Context, _, _ string) (*abi.Interface, error) {
	return nil, nil
}

func (m *mockVerifier) RegisteredWasmHash(_ context.Context, _, _ string) (string, error) {
	return m.hash, m.err
}

func TestCheck_WasmMatch(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	// Fetch live hash from inspect
	baseProber, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	inspect, err := baseProber.Inspect(context.Background(), probe.InspectRequest{
		ContractID: stellartest.WasmContract,
	})
	require.NoError(t, err)
	liveHash := inspect.WasmHash
	require.NotEmpty(t, liveHash)

	t.Run("matches", func(t *testing.T) {
		p, err := probe.New(probe.Options{
			Client:        fake,
			SourceAccount: stellartest.SourceAccount,
			ABI:           &mockVerifier{hash: liveHash},
			Thresholds:    health.DefaultThresholds,
		})
		require.NoError(t, err)

		res, err := p.Check(context.Background(), probe.CheckRequest{
			ContractID: stellartest.WasmContract,
		})
		require.NoError(t, err)

		chk := findCheck(res.Checks, "wasm_match")
		require.NotNil(t, chk)
		assert.Equal(t, probe.OutcomePass, chk.Outcome)
		assert.Contains(t, chk.Detail, "live WASM matches SoroVault build")
		assert.False(t, p.TypedDecodingDisabled())
	})

	t.Run("mismatch warns and disables typed decoding", func(t *testing.T) {
		p, err := probe.New(probe.Options{
			Client:        fake,
			SourceAccount: stellartest.SourceAccount,
			ABI:           &mockVerifier{hash: "6f52ca8cf9e6cf61e967a544eb28ac6998fe283f58e2a392bcba1fa959a40000"},
			Thresholds:    health.DefaultThresholds,
		})
		require.NoError(t, err)

		res, err := p.Check(context.Background(), probe.CheckRequest{
			ContractID: stellartest.WasmContract,
		})
		require.NoError(t, err)

		chk := findCheck(res.Checks, "wasm_match")
		require.NotNil(t, chk)
		assert.Equal(t, probe.OutcomeWarn, chk.Outcome)
		assert.Contains(t, chk.Detail, "differs from SoroVault catalogued build")
		assert.True(t, p.TypedDecodingDisabled())
	})

	t.Run("unknown contract skips check cleanly", func(t *testing.T) {
		p, err := probe.New(probe.Options{
			Client:        fake,
			SourceAccount: stellartest.SourceAccount,
			ABI:           &mockVerifier{err: abi.ErrUnknownContract},
			Thresholds:    health.DefaultThresholds,
		})
		require.NoError(t, err)

		res, err := p.Check(context.Background(), probe.CheckRequest{
			ContractID: stellartest.WasmContract,
		})
		require.NoError(t, err)

		chk := findCheck(res.Checks, "wasm_match")
		assert.Nil(t, chk)
		assert.False(t, p.TypedDecodingDisabled())
	})

	t.Run("skipped when no verifier configured", func(t *testing.T) {
		p, err := probe.New(probe.Options{
			Client:        fake,
			SourceAccount: stellartest.SourceAccount,
			Thresholds:    health.DefaultThresholds,
		})
		require.NoError(t, err)

		res, err := p.Check(context.Background(), probe.CheckRequest{
			ContractID: stellartest.WasmContract,
		})
		require.NoError(t, err)

		chk := findCheck(res.Checks, "wasm_match")
		assert.Nil(t, chk)
	})
}

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

type mockUpgradeABI struct {
	iface *abi.Interface
}

func (m *mockUpgradeABI) Interface(_ context.Context, _, _ string) (*abi.Interface, error) {
	return m.iface, nil
}

func TestCheckUpgradeability_UpgradeableContract(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	upgradeIface := &abi.Interface{
		Functions: []abi.Function{
			{Name: "upgrade", Inputs: []abi.Param{{Name: "new_wasm", Type: abi.Type{Kind: "bytes", Display: "Bytes"}}}},
			{Name: "decimals"},
		},
	}

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
		ABI:           &mockUpgradeABI{iface: upgradeIface},
		Network:       "testnet",
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID:     stellartest.WasmContract,
		Upgradeability: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res.Upgradeability)

	assert.True(t, res.Upgradeability.Upgradeable)
	require.Len(t, res.Upgradeability.UpgradeFunctions, 1)
	assert.Equal(t, "upgrade", res.Upgradeability.UpgradeFunctions[0].Name)
	assert.NotEmpty(t, res.Upgradeability.Note)

	chk := findCheck(res.Checks, "upgradeability")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomeWarn, chk.Outcome)
	assert.Contains(t, chk.Detail, "contract is upgradeable")
	assert.Contains(t, chk.Detail, "heuristic: best-effort")
}

func TestCheckUpgradeability_ImmutableContract(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	immutableIface := &abi.Interface{
		Functions: []abi.Function{
			{Name: "decimals"},
			{Name: "balance"},
		},
	}

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
		ABI:           &mockUpgradeABI{iface: immutableIface},
		Network:       "testnet",
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID:     stellartest.WasmContract,
		Upgradeability: true,
	})
	require.NoError(t, err)
	require.NotNil(t, res.Upgradeability)

	assert.False(t, res.Upgradeability.Upgradeable)
	assert.Empty(t, res.Upgradeability.UpgradeFunctions)

	chk := findCheck(res.Checks, "upgradeability")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomePass, chk.Outcome)
	assert.Contains(t, chk.Detail, "no upgrade functions exposed")
}

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

type mockStandardABI struct {
	iface *abi.Interface
}

func (m *mockStandardABI) Interface(_ context.Context, _, _ string) (*abi.Interface, error) {
	return m.iface, nil
}

func TestCheckStandardCompliance_SEP41(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	// Valid SEP-41 Interface
	sep41Iface := &abi.Interface{
		Functions: []abi.Function{
			{Name: "allowance", Inputs: []abi.Param{{Name: "from", Type: abi.Type{Display: "Address"}}, {Name: "spender", Type: abi.Type{Display: "Address"}}}, Outputs: []abi.Type{{Display: "i128"}}},
			{Name: "approve", Inputs: []abi.Param{{Name: "from", Type: abi.Type{Display: "Address"}}, {Name: "spender", Type: abi.Type{Display: "Address"}}, {Name: "amount", Type: abi.Type{Display: "i128"}}, {Name: "expiration_ledger", Type: abi.Type{Display: "u32"}}}},
			{Name: "balance", Inputs: []abi.Param{{Name: "id", Type: abi.Type{Display: "Address"}}}, Outputs: []abi.Type{{Display: "i128"}}},
			{Name: "transfer", Inputs: []abi.Param{{Name: "from", Type: abi.Type{Display: "Address"}}, {Name: "to", Type: abi.Type{Display: "Address"}}, {Name: "amount", Type: abi.Type{Display: "i128"}}}},
			{Name: "transfer_from", Inputs: []abi.Param{{Name: "spender", Type: abi.Type{Display: "Address"}}, {Name: "from", Type: abi.Type{Display: "Address"}}, {Name: "to", Type: abi.Type{Display: "Address"}}, {Name: "amount", Type: abi.Type{Display: "i128"}}}},
			{Name: "decimals", Outputs: []abi.Type{{Display: "u32"}}},
			{Name: "name", Outputs: []abi.Type{{Display: "String"}}},
			{Name: "symbol", Outputs: []abi.Type{{Display: "String"}}},
		},
	}

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
		ABI:           &mockStandardABI{iface: sep41Iface},
		Network:       "testnet",
	})
	require.NoError(t, err)

	checks, err := p.CheckStandardCompliance(context.Background(), stellartest.SACContract, "sep41")
	require.NoError(t, err)
	require.NotEmpty(t, checks)

	for _, c := range checks {
		assert.Equal(t, probe.OutcomePass, c.Outcome, "expected check %s to pass, got: %s", c.Name, c.Detail)
	}
}

func TestCheckStandardCompliance_MissingFunction(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	// Missing allowance function
	partialIface := &abi.Interface{
		Functions: []abi.Function{
			{Name: "balance", Inputs: []abi.Param{{Name: "id", Type: abi.Type{Display: "Address"}}}, Outputs: []abi.Type{{Display: "i128"}}},
			{Name: "decimals", Outputs: []abi.Type{{Display: "u32"}}},
		},
	}

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
		ABI:           &mockStandardABI{iface: partialIface},
		Network:       "testnet",
	})
	require.NoError(t, err)

	checks, err := p.CheckStandardCompliance(context.Background(), stellartest.SACContract, "sep41")
	require.NoError(t, err)

	var foundMissing bool
	for _, c := range checks {
		if c.Name == "standard:sep41:allowance" {
			foundMissing = true
			assert.Equal(t, probe.OutcomeFail, c.Outcome)
			assert.Contains(t, c.Detail, "missing required function")
		}
	}
	assert.True(t, foundMissing)
}

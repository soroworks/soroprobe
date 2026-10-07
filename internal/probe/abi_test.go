package probe_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

// fakeABI serves fixed interfaces and records what it was asked for.
type fakeABI struct {
	ifaces   map[string]*abi.Interface
	err      error
	networks []string
}

func (f *fakeABI) Interface(_ context.Context, network, contractID string) (*abi.Interface, error) {
	f.networks = append(f.networks, network)
	if f.err != nil {
		return nil, f.err
	}
	iface, ok := f.ifaces[contractID]
	if !ok {
		return nil, fmt.Errorf("%w: %s", abi.ErrUnknownContract, contractID)
	}
	return iface, nil
}

// sacInterface declares decimals with one u32 argument, which the real SAC
// does not, purely so the test can watch a bare argument get typed.
var sacInterface = &abi.Interface{Functions: []abi.Function{
	{
		Name:    "decimals",
		Inputs:  []abi.Param{{Name: "scale", Type: abi.Type{Kind: "u32", Display: "u32"}}},
		Outputs: []abi.Type{{Kind: "u32", Display: "u32"}},
	},
	{Name: "name", Outputs: []abi.Type{{Kind: "string", Display: "String"}}},
}}

func newABIProber(t *testing.T, fake *stellartest.Fake, src abi.Source) *probe.Prober {
	t.Helper()
	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
		ABI:           src,
		Network:       "testnet",
	})
	require.NoError(t, err)
	return p
}

func TestSimulateTypesArgumentsFromABI(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	src := &fakeABI{ifaces: map[string]*abi.Interface{stellartest.SACContract: sacInterface}}
	p := newABIProber(t, fake, src)

	result, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimals",
		Args:       []string{"5"},
	})
	require.NoError(t, err)

	assert.Equal(t, []string{"testnet"}, src.networks)
	assert.Equal(t, "fn decimals(scale: u32) -> u32", result.Signature)
	assert.Equal(t, []string{"5"}, result.Args, "the request is reported as given")
	assert.Equal(t, []string{"u32:5"}, result.EncodedArgs)
	assert.Empty(t, result.ABINote)

	// Without the interface "5" would have been encoded as an i128.
	args, err := stellartest.ArgsOf(fake.SimulateRequests[0].Transaction)
	require.NoError(t, err)
	require.Len(t, args, 1)
	require.NotNil(t, args[0].U32)
	assert.EqualValues(t, 5, *args[0].U32)
}

func TestSimulateExplicitPrefixBeatsABI(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p := newABIProber(t, fake, &fakeABI{ifaces: map[string]*abi.Interface{stellartest.SACContract: sacInterface}})

	result, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimals",
		Args:       []string{"i64:5"},
	})
	require.NoError(t, err)
	assert.Nil(t, result.EncodedArgs, "nothing was rewritten")

	args, err := stellartest.ArgsOf(fake.SimulateRequests[0].Transaction)
	require.NoError(t, err)
	assert.NotNil(t, args[0].I64)
}

func TestSimulateRejectsUnknownFunctionBeforeSimulating(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p := newABIProber(t, fake, &fakeABI{ifaces: map[string]*abi.Interface{stellartest.SACContract: sacInterface}})

	_, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimal",
	})
	require.ErrorIs(t, err, abi.ErrNoSuchFunction)
	assert.Contains(t, err.Error(), "available: decimals, name")
	assert.Empty(t, fake.SimulateRequests, "a call that cannot succeed must not reach RPC")
}

func TestSimulateRejectsWrongArgumentCount(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p := newABIProber(t, fake, &fakeABI{ifaces: map[string]*abi.Interface{stellartest.SACContract: sacInterface}})

	_, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimals",
	})
	require.ErrorIs(t, err, abi.ErrArgumentCount)
	assert.Empty(t, fake.SimulateRequests)
}

func TestSimulateFallsBackToInference(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  *fakeABI
		note string
	}{
		{"unregistered contract", &fakeABI{}, "not found in the interface registry"},
		{"registry down", &fakeABI{err: errors.New("connection refused")}, "connection refused"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := stellartest.NewFake(t)
			p := newABIProber(t, fake, tt.src)

			result, err := p.Simulate(context.Background(), probe.SimulateRequest{
				ContractID: stellartest.SACContract,
				Function:   "decimals",
			})
			require.NoError(t, err)
			assert.True(t, result.Success)
			assert.Contains(t, result.ABINote, tt.note)
			assert.Empty(t, result.Signature)
		})
	}
}

func TestCheckUsesABIForItsSimulation(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p := newABIProber(t, fake, &fakeABI{ifaces: map[string]*abi.Interface{stellartest.SACContract: sacInterface}})

	result, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimals",
		Args:       []string{"7"},
	})
	require.NoError(t, err)
	require.NotNil(t, result.Simulate)
	assert.Equal(t, []string{"u32:7"}, result.Simulate.EncodedArgs)
}

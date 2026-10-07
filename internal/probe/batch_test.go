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

func named(name, contract, fn string) probe.NamedCheck {
	return probe.NamedCheck{Name: name, CheckRequest: probe.CheckRequest{ContractID: contract, Function: fn}}
}

func TestCheckBatchAllPass(t *testing.T) {
	t.Parallel()
	p := newProber(t, stellartest.NewFake(t), health.DefaultThresholds)

	result, err := p.CheckBatch(context.Background(), probe.BatchRequest{Checks: []probe.NamedCheck{
		named("native", stellartest.SACContract, "decimals"),
		named("", stellartest.WasmContract, ""),
	}})
	require.NoError(t, err)

	assert.True(t, result.OK)
	assert.False(t, result.Errored)
	require.Len(t, result.Items, 2)
	assert.Equal(t, "native", result.Items[0].Name)
	assert.Equal(t, stellartest.WasmContract, result.Items[1].Name, "the name defaults to the contract ID")
	assert.True(t, result.Items[0].Result.OK)
}

func TestCheckBatchUnhealthyIsNotAnError(t *testing.T) {
	t.Parallel()
	p := newProber(t, stellartest.NewFake(t), health.DefaultThresholds)

	result, err := p.CheckBatch(context.Background(), probe.BatchRequest{Checks: []probe.NamedCheck{
		named("ok", stellartest.SACContract, "decimals"),
		named("gone", stellartest.UndeployedContract, ""),
	}})
	require.NoError(t, err)

	assert.False(t, result.OK)
	assert.False(t, result.Errored, "an unhealthy contract is an answer, not a tool failure")
	assert.False(t, result.Items[1].Result.OK)
	assert.Empty(t, result.Items[1].Error)
}

func TestCheckBatchContinuesPastAnErroredCheck(t *testing.T) {
	t.Parallel()
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.DefaultThresholds)

	result, err := p.CheckBatch(context.Background(), probe.BatchRequest{Checks: []probe.NamedCheck{
		named("bad", "not-a-contract", ""),
		named("good", stellartest.SACContract, "decimals"),
	}})
	require.NoError(t, err)

	assert.False(t, result.OK)
	assert.True(t, result.Errored)
	assert.NotEmpty(t, result.Items[0].Error)
	assert.Nil(t, result.Items[0].Result)
	require.NotNil(t, result.Items[1].Result, "the next check still runs")
	assert.True(t, result.Items[1].Result.OK)
}

func TestCheckBatchRejectsMalformedBatches(t *testing.T) {
	t.Parallel()
	p := newProber(t, stellartest.NewFake(t), health.DefaultThresholds)

	tooMany := make([]probe.NamedCheck, probe.MaxBatchChecks+1)
	for i := range tooMany {
		tooMany[i] = named("", stellartest.SACContract, "")
		tooMany[i].Name = string(rune('a'+i%26)) + string(rune('a'+i/26))
	}

	durability := named("x", stellartest.SACContract, "")
	durability.DataDurability = "temp"

	tests := map[string]probe.BatchRequest{
		"empty":            {},
		"too many":         {Checks: tooMany},
		"no contract":      {Checks: []probe.NamedCheck{named("x", "", "")}},
		"duplicate names":  {Checks: []probe.NamedCheck{named("x", stellartest.SACContract, ""), named("x", stellartest.WasmContract, "")}},
		"default dup name": {Checks: []probe.NamedCheck{named("", stellartest.SACContract, ""), named("", stellartest.SACContract, "decimals")}},
		"bad durability":   {Checks: []probe.NamedCheck{durability}},
	}
	for name, req := range tests {
		_, err := p.CheckBatch(context.Background(), req)
		assert.ErrorIs(t, err, probe.ErrInvalidBatch, name)
	}
}

func TestCheckBatchStopsOnCancel(t *testing.T) {
	t.Parallel()
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.DefaultThresholds)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.CheckBatch(ctx, probe.BatchRequest{Checks: []probe.NamedCheck{named("a", stellartest.SACContract, "")}})
	assert.ErrorIs(t, err, context.Canceled)
	assert.Empty(t, fake.EntryRequests, "a cancelled batch sends nothing")
}

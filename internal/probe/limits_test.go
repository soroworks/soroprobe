package probe_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func storeSetting(t *testing.T, fake *stellartest.Fake, id xdr.ConfigSettingId, entry xdr.ConfigSettingEntry) {
	t.Helper()
	entry.ConfigSettingId = id
	fake.StoreEntry(t, stellar.SettingKey(id), xdr.LedgerEntryData{
		Type:          xdr.LedgerEntryTypeConfigSetting,
		ConfigSetting: &entry,
	})
}

// storeLimits stores a full set of limit settings with the given
// per-transaction instruction limit.
func storeLimits(t *testing.T, fake *stellartest.Fake, txInstructions int64) {
	t.Helper()
	maxSize, keySize, entrySize := xdr.Uint32(131072), xdr.Uint32(250), xdr.Uint32(131072)
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes, xdr.ConfigSettingEntry{ContractMaxSizeBytes: &maxSize})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractComputeV0, xdr.ConfigSettingEntry{
		ContractCompute: &xdr.ConfigSettingContractComputeV0{
			LedgerMaxInstructions: 500_000_000, TxMaxInstructions: xdr.Int64(txInstructions), TxMemoryLimit: 41_943_040,
		},
	})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractLedgerCostV0, xdr.ConfigSettingEntry{
		ContractLedgerCost: &xdr.ConfigSettingContractLedgerCostV0{
			TxMaxDiskReadEntries: 100, TxMaxDiskReadBytes: 200_000, TxMaxWriteLedgerEntries: 50, TxMaxWriteBytes: 132_096,
			LedgerMaxDiskReadEntries: 1000, LedgerMaxDiskReadBytes: 3_500_000, LedgerMaxWriteLedgerEntries: 500, LedgerMaxWriteBytes: 143_360,
		},
	})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractLedgerCostExtV0, xdr.ConfigSettingEntry{
		ContractLedgerCostExt: &xdr.ConfigSettingContractLedgerCostExtV0{TxMaxFootprintEntries: 100},
	})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractEventsV0, xdr.ConfigSettingEntry{
		ContractEvents: &xdr.ConfigSettingContractEventsV0{TxMaxContractEventsSizeBytes: 16_384},
	})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractBandwidthV0, xdr.ConfigSettingEntry{
		ContractBandwidth: &xdr.ConfigSettingContractBandwidthV0{TxMaxSizeBytes: 132_096, LedgerMaxTxsSizeBytes: 133_120},
	})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractDataKeySizeBytes, xdr.ConfigSettingEntry{ContractDataKeySizeBytes: &keySize})
	storeSetting(t, fake, xdr.ConfigSettingIdConfigSettingContractDataEntrySizeBytes, xdr.ConfigSettingEntry{ContractDataEntrySizeBytes: &entrySize})
}

func TestLimitsReadsTheConfigSettings(t *testing.T) {
	fake := stellartest.NewFake(t)
	storeLimits(t, fake, 100_000_000)
	p := newProber(t, fake, health.Thresholds{})

	limits, err := p.Limits(context.Background())
	require.NoError(t, err)
	assert.Empty(t, limits.Missing)
	assert.Empty(t, limits.Unrecognized)
	assert.Equal(t, fake.LatestLedger, limits.LatestLedger)

	for name, want := range map[string]int64{
		probe.LimitTxInstructions:     100_000_000,
		probe.LimitTxDiskReadBytes:    200_000,
		probe.LimitTxWriteBytes:       132_096,
		probe.LimitTxFootprintEntries: 100,
		"tx_memory_limit":             41_943_040,
		"tx_max_size":                 132_096,
		"tx_max_events_size":          16_384,
		"ledger_max_instructions":     500_000_000,
		"contract_max_size":           131_072,
		"contract_data_key_size":      250,
	} {
		got, ok := limits.Get(name)
		assert.True(t, ok, name)
		assert.Equal(t, want, got, name)
	}

	// Transaction limits come first, then entry sizes, then ledger totals.
	seen := map[string]bool{}
	last := ""
	for _, l := range limits.Limits {
		if l.Scope != last {
			assert.False(t, seen[l.Scope], "scope %s is split", l.Scope)
			seen[l.Scope], last = true, l.Scope
		}
		assert.NotEmpty(t, l.Unit, l.Name)
	}
	assert.Equal(t, "transaction", limits.Limits[0].Scope)

	// Limits are cached: a second read makes no RPC call.
	before := len(fake.EntryRequests)
	_, err = p.Limits(context.Background())
	require.NoError(t, err)
	assert.Len(t, fake.EntryRequests, before)
}

func TestLimitsReportsMissingAndUnknownSettings(t *testing.T) {
	fake := stellartest.NewFake(t)
	// A compute setting whose value is the wrong arm stands in for a type a
	// newer protocol introduced.
	size := xdr.Uint32(1)
	fake.StoreEntry(t, stellar.SettingKey(xdr.ConfigSettingIdConfigSettingContractComputeV0), xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeConfigSetting,
		ConfigSetting: &xdr.ConfigSettingEntry{
			ConfigSettingId:      xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes,
			ContractMaxSizeBytes: &size,
		},
	})
	p := newProber(t, fake, health.Thresholds{})

	limits, err := p.Limits(context.Background())
	require.NoError(t, err)
	assert.Empty(t, limits.Limits[1:])
	assert.Contains(t, limits.Missing, "ConfigSettingIdConfigSettingContractLedgerCostV0")
	assert.NotContains(t, limits.Missing, "ConfigSettingIdConfigSettingContractComputeV0")
	require.Len(t, limits.Limits, 1)
	assert.Equal(t, "contract_max_size", limits.Limits[0].Name)
	assert.Empty(t, limits.Unrecognized)
}

func TestLimitsSurfacesRPCErrors(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.EntriesErr = errors.New("rpc down")
	_, err := newProber(t, fake, health.Thresholds{}).Limits(context.Background())
	require.Error(t, err)
}

func TestCompareWithLimits(t *testing.T) {
	limits := &probe.NetworkLimits{Limits: []probe.Limit{
		{Name: probe.LimitTxInstructions, Value: 1000},
		{Name: probe.LimitTxDiskReadBytes, Value: 100},
		{Name: probe.LimitTxFootprintEntries, Value: 10},
	}}
	cost := probe.Cost{Instructions: 900, DiskReadBytes: 10, WriteBytes: 5}
	fp := probe.Footprint{ReadOnly: []string{"a", "b"}}

	report := probe.CompareWithLimits(cost, fp, limits, 0)
	require.NotNil(t, report)
	assert.Equal(t, probe.DefaultHeadroomPercent, report.HeadroomPercent)
	require.Len(t, report.Resources, 3, "write bytes has no reported limit and is skipped")
	assert.InDelta(t, 90.0, report.Resources[0].Percent, 1e-9)
	assert.True(t, report.Resources[0].OverHeadroom)
	assert.InDelta(t, 20.0, report.Resources[2].Percent, 1e-9)
	assert.Contains(t, report.Warning, "cpu instructions at 90.0%")

	relaxed := probe.CompareWithLimits(cost, fp, limits, 95)
	assert.Empty(t, relaxed.Warning)
	assert.Nil(t, probe.CompareWithLimits(cost, fp, nil, 80))
	assert.Nil(t, probe.CompareWithLimits(cost, fp, &probe.NetworkLimits{}, 80))
}

func TestSimulateReportsUsageAndCheckWarnsNearLimits(t *testing.T) {
	ctx := context.Background()

	// The decimals fixture uses 127,569 instructions.
	fake := stellartest.NewFake(t)
	storeLimits(t, fake, 140_000)
	p := newProber(t, fake, health.Thresholds{})
	res, err := p.Simulate(ctx, probe.SimulateRequest{ContractID: stellartest.SACContract, Function: "decimals"})
	require.NoError(t, err)
	require.NotNil(t, res.Usage)
	assert.Contains(t, res.Usage.Warning, "cpu instructions")

	check, err := p.Check(ctx, probe.CheckRequest{ContractID: stellartest.SACContract, Function: "decimals"})
	require.NoError(t, err)
	headroom := headroomCheck(t, check, "headroom")
	assert.Equal(t, probe.OutcomeWarn, headroom.Outcome)
	assert.True(t, check.OK, "a warning does not fail the check")

	roomy := stellartest.NewFake(t)
	storeLimits(t, roomy, 100_000_000)
	check, err = newProber(t, roomy, health.Thresholds{}).Check(ctx, probe.CheckRequest{
		ContractID: stellartest.SACContract, Function: "decimals", HeadroomPercent: 50,
	})
	require.NoError(t, err)
	headroom = headroomCheck(t, check, "headroom")
	assert.Equal(t, probe.OutcomePass, headroom.Outcome)
	assert.Contains(t, headroom.Detail, "within 50%")
}

func TestCheckHasNoHeadroomWithoutLimits(t *testing.T) {
	fake := stellartest.NewFake(t)
	check, err := newProber(t, fake, health.Thresholds{}).Check(context.Background(),
		probe.CheckRequest{ContractID: stellartest.SACContract, Function: "decimals"})
	require.NoError(t, err)
	for _, c := range check.Checks {
		assert.NotEqual(t, "headroom", c.Name)
	}
	assert.Contains(t, check.Simulate.Notes, "the network reported no resource limits to compare with")
}

func headroomCheck(t *testing.T, r *probe.CheckResult, name string) probe.Check {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %s check in %+v", name, r.Checks)
	return probe.Check{}
}

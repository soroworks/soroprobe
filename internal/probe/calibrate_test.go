package probe_test

import (
	"context"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

const txHash = "a1b2c3d4e5f60718293a4b5c6d7e8f90a1b2c3d4e5f60718293a4b5c6d7e8f90"

// submittedTx builds a getTransaction response for a call to fn on the Wasm
// fixture contract, declaring the given resources and touching its instance.
func submittedTx(t *testing.T, fn string, ledger uint32, declared xdr.SorobanResources, resourceFee int64) protocol.GetTransactionResponse {
	t.Helper()
	contract, err := stellar.ParseContractID(stellartest.WasmContract)
	require.NoError(t, err)
	b64, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: stellartest.SourceAccount,
		Contract:      contract,
		Function:      fn,
	})
	require.NoError(t, err)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &env))
	declared.Footprint = xdr.LedgerFootprint{ReadOnly: []xdr.LedgerKey{stellar.InstanceKey(contract)}, ReadWrite: []xdr.LedgerKey{}}
	env.V1.Tx.Ext = xdr.TransactionExt{V: 1, SorobanData: &xdr.SorobanTransactionData{
		Resources:   declared,
		ResourceFee: xdr.Int64(resourceFee),
	}}
	envXDR, err := xdr.MarshalBase64(env)
	require.NoError(t, err)

	return protocol.GetTransactionResponse{
		LatestLedger: ledger + 100,
		OldestLedger: ledger - 1000,
		TransactionDetails: protocol.TransactionDetails{
			Status:          protocol.TransactionStatusSuccess,
			TransactionHash: txHash,
			EnvelopeXDR:     envXDR,
			Ledger:          ledger,
		},
	}
}

func metaWithCharge(t *testing.T, nonRefundable, refundable int64) string {
	t.Helper()
	meta := xdr.TransactionMeta{V: 4, V4: &xdr.TransactionMetaV4{
		SorobanMeta: &xdr.SorobanTransactionMetaV2{Ext: xdr.SorobanTransactionMetaExt{V: 1, V1: &xdr.SorobanTransactionMetaExtV1{
			TotalNonRefundableResourceFeeCharged: xdr.Int64(nonRefundable),
			TotalRefundableResourceFeeCharged:    xdr.Int64(refundable),
		}}},
	}}
	b64, err := xdr.MarshalBase64(meta)
	require.NoError(t, err)
	return b64
}

func coreMetric(t *testing.T, name string, value uint64) string {
	t.Helper()
	topic, metric := xdr.ScSymbol("core_metrics"), xdr.ScSymbol(name)
	v := xdr.Uint64(value)
	ev := xdr.DiagnosticEvent{Event: xdr.ContractEvent{
		Type: xdr.ContractEventTypeDiagnostic,
		Body: xdr.ContractEventBody{V: 0, V0: &xdr.ContractEventV0{
			Topics: []xdr.ScVal{{Type: xdr.ScValTypeScvSymbol, Sym: &topic}, {Type: xdr.ScValTypeScvSymbol, Sym: &metric}},
			Data:   xdr.ScVal{Type: xdr.ScValTypeScvU64, U64: &v},
		}},
	}}
	b64, err := xdr.MarshalBase64(ev)
	require.NoError(t, err)
	return b64
}

func resource(t *testing.T, res *probe.CalibrateResult, name string) probe.ResourceDelta {
	t.Helper()
	for _, r := range res.Resources {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("no %q resource in %+v", name, res.Resources)
	return probe.ResourceDelta{}
}

func TestCalibrateComparesWithConsumedResources(t *testing.T) {
	fake := stellartest.NewFake(t)
	sim := fake.Simulations["decimals"]
	tx := submittedTx(t, "decimals", fake.LatestLedger, xdr.SorobanResources{Instructions: 9_000_000, DiskReadBytes: 5000, WriteBytes: 0}, 90_000)
	tx.ResultMetaXDR = metaWithCharge(t, 40_000, 2_000)
	tx.DiagnosticEventsXDR = []string{coreMetric(t, "cpu_insn", 1_000_000), coreMetric(t, "ledger_read_byte", 4000)}
	fake.Transactions = map[string]protocol.GetTransactionResponse{txHash: tx}
	p := newProber(t, fake, health.Thresholds{})

	res, err := p.Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	assert.Equal(t, stellartest.WasmContract, res.ContractID)
	assert.Equal(t, "decimals", res.Function)
	assert.True(t, res.SimulationSuccess)

	var data xdr.SorobanTransactionData
	require.NoError(t, xdr.SafeUnmarshalBase64(sim.TransactionDataXDR, &data))

	cpu := resource(t, res, "cpu instructions")
	assert.Equal(t, probe.SourceConsumed, cpu.Source)
	assert.Equal(t, int64(1_000_000), cpu.Actual)
	assert.Equal(t, int64(data.Resources.Instructions), cpu.Estimated)
	assert.Equal(t, cpu.Estimated-cpu.Actual, cpu.Delta)
	require.NotNil(t, cpu.Percent)
	assert.InDelta(t, float64(cpu.Delta)/1_000_000*100, *cpu.Percent, 1e-9)

	assert.Equal(t, probe.SourceConsumed, resource(t, res, "disk read bytes").Source)
	// No core metric for writes: the declared figure stands in.
	write := resource(t, res, "write bytes")
	assert.Equal(t, probe.SourceDeclared, write.Source)
	assert.Nil(t, write.Percent, "a zero actual has no percentage")

	fee := resource(t, res, "resource fee (stroops)")
	assert.Equal(t, probe.SourceConsumed, fee.Source)
	assert.Equal(t, int64(42_000), fee.Actual)
}

func TestCalibrateFallsBackToDeclaredResources(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.Transactions = map[string]protocol.GetTransactionResponse{
		txHash: submittedTx(t, "decimals", fake.LatestLedger, xdr.SorobanResources{Instructions: 2_000_000, DiskReadBytes: 100, WriteBytes: 10}, 50_000),
	}
	res, err := newProber(t, fake, health.Thresholds{}).Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	for _, r := range res.Resources {
		assert.Equal(t, probe.SourceDeclared, r.Source, r.Name)
	}
	assert.Equal(t, int64(2_000_000), resource(t, res, "cpu instructions").Actual)
	assert.Equal(t, int64(50_000), resource(t, res, "resource fee (stroops)").Actual)
	assert.Contains(t, res.Notes[0], "no core_metrics")
}

func TestCalibrateNotesStateChanges(t *testing.T) {
	fake := stellartest.NewFake(t)
	// The transaction ran long before the instance entry was last modified.
	old := submittedTx(t, "decimals", 1000, xdr.SorobanResources{Instructions: 1}, 1)
	fake.Transactions = map[string]protocol.GetTransactionResponse{txHash: old}
	key := stellartest.InstanceKeyOf(t, stellartest.WasmContract)
	entry := fake.Entries[key]
	entry.LastModifiedLedger = 5000
	fake.Entries[key] = entry

	res, err := newProber(t, fake, health.Thresholds{}).Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	assert.True(t, res.StateChanged)
	require.Len(t, res.ChangedEntries, 1)
	assert.Contains(t, res.ChangedEntries[0], "(modified)")
	assert.Contains(t, res.Notes[len(res.Notes)-1], "comparison is approximate")

	// Run in the same ledger as the last modification: nothing changed since.
	same := submittedTx(t, "decimals", 5000, xdr.SorobanResources{Instructions: 1}, 1)
	fake.Transactions[txHash] = same
	res, err = newProber(t, fake, health.Thresholds{}).Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	assert.False(t, res.StateChanged)

	// An entry that no longer exists counts as changed too.
	delete(fake.Entries, key)
	res, err = newProber(t, fake, health.Thresholds{}).Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	assert.True(t, res.StateChanged)
	assert.Contains(t, res.ChangedEntries[0], "no longer live")
}

func TestCalibrateExplainsAFailingResimulation(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.Transactions = map[string]protocol.GetTransactionResponse{
		txHash: submittedTx(t, "no_such_fn", fake.LatestLedger, xdr.SorobanResources{Instructions: 1}, 1),
	}
	res, err := newProber(t, fake, health.Thresholds{}).Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	assert.False(t, res.SimulationSuccess)
	require.NotNil(t, res.SimulationErrorExplanation)
	assert.Equal(t, probe.ErrorKindHost, res.SimulationErrorExplanation.Kind)
	assert.Contains(t, res.Notes, "the invocation fails when simulated now, so its estimates are not comparable")
}

func TestCalibrateResimulatesTheSameInvocation(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.Transactions = map[string]protocol.GetTransactionResponse{
		txHash: submittedTx(t, "decimals", fake.LatestLedger, xdr.SorobanResources{Instructions: 1}, 1),
	}
	_, err := newProber(t, fake, health.Thresholds{}).Calibrate(context.Background(), txHash)
	require.NoError(t, err)
	require.Len(t, fake.SimulateRequests, 1)
	fn, err := stellartest.SubjectOf(fake.SimulateRequests[0].Transaction)
	require.NoError(t, err)
	assert.Equal(t, "decimals", fn)
}

func TestCalibrateErrors(t *testing.T) {
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.Thresholds{})
	ctx := context.Background()

	_, err := p.Calibrate(ctx, "  ")
	require.Error(t, err)

	_, err = p.Calibrate(ctx, txHash)
	require.ErrorIs(t, err, probe.ErrTransactionNotFound)
	assert.Contains(t, err.Error(), "the RPC keeps ledgers")

	// A transaction with no Soroban resources cannot be calibrated.
	tx := submittedTx(t, "decimals", fake.LatestLedger, xdr.SorobanResources{}, 0)
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(tx.EnvelopeXDR, &env))
	env.V1.Tx.Ext = xdr.TransactionExt{}
	tx.EnvelopeXDR, err = xdr.MarshalBase64(env)
	require.NoError(t, err)
	fake.Transactions = map[string]protocol.GetTransactionResponse{txHash: tx}
	_, err = p.Calibrate(ctx, txHash)
	require.ErrorContains(t, err, "no Soroban resources")
}

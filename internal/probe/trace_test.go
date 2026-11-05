package probe_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func encodeDiagEvent(t *testing.T, contractID string, success bool, topics []xdr.ScVal, data xdr.ScVal) string {
	t.Helper()
	de := xdr.DiagnosticEvent{
		InSuccessfulContractCall: success,
		Event: xdr.ContractEvent{
			Type: xdr.ContractEventTypeDiagnostic,
			Body: xdr.ContractEventBody{
				V: 0,
				V0: &xdr.ContractEventV0{
					Topics: topics,
					Data:   data,
				},
			},
		},
	}
	if contractID != "" {
		raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
		require.NoError(t, err)
		var cid xdr.ContractId
		copy(cid[:], raw)
		de.Event.ContractId = &cid
	}
	b64, err := xdr.MarshalBase64(de)
	require.NoError(t, err)
	return b64
}

func symVal(s string) xdr.ScVal {
	sym := xdr.ScSymbol(s)
	return xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
}

func contractBytesVal(t *testing.T, contractID string) xdr.ScVal {
	t.Helper()
	raw, err := strkey.Decode(strkey.VersionByteContract, contractID)
	require.NoError(t, err)
	b := xdr.ScBytes(raw)
	return xdr.ScVal{Type: xdr.ScValTypeScvBytes, Bytes: &b}
}

func u32Val(v uint32) xdr.ScVal {
	u := xdr.Uint32(v)
	return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &u}
}

func vecVal(items ...xdr.ScVal) xdr.ScVal {
	vec := xdr.ScVec(items)
	ptr := &vec
	return xdr.ScVal{Type: xdr.ScValTypeScvVec, Vec: &ptr}
}

func TestSimulateTraceRecordedFixtures(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.DefaultThresholds)

	t.Run("successful call builds root frame with return value", func(t *testing.T) {
		res, err := p.Simulate(context.Background(), probe.SimulateRequest{
			ContractID: stellartest.SACContract,
			Function:   "decimals",
			Trace:      true,
		})
		require.NoError(t, err)
		require.Len(t, res.CallTree, 1)

		root := res.CallTree[0]
		assert.Equal(t, stellartest.SACContract, root.ContractID)
		assert.Equal(t, "decimals", root.Function)
		assert.True(t, root.Success)
		assert.Equal(t, uint32(7), root.ReturnValue)
	})

	t.Run("failed call attaches diagnostic error to the failing frame", func(t *testing.T) {
		res, err := p.Simulate(context.Background(), probe.SimulateRequest{
			ContractID: stellartest.SACContract,
			Function:   "no_such_fn",
			Trace:      true,
		})
		require.NoError(t, err)
		require.Len(t, res.CallTree, 1)

		root := res.CallTree[0]
		assert.Equal(t, stellartest.SACContract, root.ContractID)
		assert.Equal(t, "no_such_fn", root.Function)
		assert.False(t, root.Success)
		assert.Contains(t, root.Error, "symbol not found")
	})
}

func TestSimulateTraceNestedCrossContractCalls(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	sim := fake.Simulations["decimals"]
	sim.EventsXDR = []string{
		encodeDiagEvent(t, "", true,
			[]xdr.ScVal{symVal("fn_call"), contractBytesVal(t, stellartest.SACContract), symVal("decimals")},
			vecVal(u32Val(100))),
		encodeDiagEvent(t, stellartest.SACContract, true,
			[]xdr.ScVal{symVal("fn_call"), contractBytesVal(t, stellartest.WasmContract), symVal("quote")},
			vecVal(u32Val(50))),
		encodeDiagEvent(t, stellartest.WasmContract, true,
			[]xdr.ScVal{symVal("fn_return"), symVal("quote")},
			u32Val(250)),
		encodeDiagEvent(t, stellartest.SACContract, true,
			[]xdr.ScVal{symVal("fn_return"), symVal("decimals")},
			u32Val(7)),
	}
	fake.Simulations["decimals"] = sim

	p := newProber(t, fake, health.DefaultThresholds)
	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimals",
		Trace:      true,
	})
	require.NoError(t, err)
	require.Len(t, res.CallTree, 1)

	root := res.CallTree[0]
	assert.Equal(t, stellartest.SACContract, root.ContractID)
	assert.Equal(t, "decimals", root.Function)
	assert.Equal(t, []any{uint32(100)}, root.Args)
	assert.Equal(t, uint32(7), root.ReturnValue)
	require.Len(t, root.Calls, 1)

	child := root.Calls[0]
	assert.Equal(t, stellartest.WasmContract, child.ContractID)
	assert.Equal(t, "quote", child.Function)
	assert.Equal(t, []any{uint32(50)}, child.Args)
	assert.Equal(t, uint32(250), child.ReturnValue)
	assert.True(t, child.Success)

	// Verify nested JSON emission.
	rawJSON, err := json.Marshal(res)
	require.NoError(t, err)
	assert.Contains(t, string(rawJSON), `"call_tree":[`)
	assert.Contains(t, string(rawJSON), `"calls":[`)
}

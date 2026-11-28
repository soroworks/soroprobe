package probe_test

import (
	"context"
	"testing"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

type staticABISource struct {
	ifaces map[string]*abi.Interface
}

func (s *staticABISource) Interface(_ context.Context, _, contractID string) (*abi.Interface, error) {
	if iface, ok := s.ifaces[contractID]; ok {
		return iface, nil
	}
	return nil, abi.ErrUnknownContract
}

func TestSimulatePrebuiltEnvelope(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	const contractID = "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC"
	addr, err := stellar.ParseContractID(contractID)
	require.NoError(t, err)

	u := xdr.Uint32(7)
	envelope, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: stellartest.SourceAccount,
		Contract:      addr,
		Function:      "decimals",
		Args:          []xdr.ScVal{{Type: xdr.ScValTypeScvU32, U32: &u}},
	})
	require.NoError(t, err)

	abiSrc := &staticABISource{
		ifaces: map[string]*abi.Interface{
			contractID: {
				Functions: []abi.Function{{
					Name:    "decimals",
					Inputs:  []abi.Param{{Name: "index", Type: abi.Type{Kind: "u32", Display: "u32"}}},
					Outputs: []abi.Type{{Kind: "u32", Display: "u32"}},
				}},
			},
		},
	}

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		ABI:           abiSrc,
	})
	require.NoError(t, err)

	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		Envelope: envelope,
	})
	require.NoError(t, err)
	assert.True(t, res.Success)
	assert.Equal(t, contractID, res.ContractID)
	assert.Equal(t, "decimals", res.Function)
	assert.Equal(t, "fn decimals(index: u32) -> u32", res.Signature)
	require.Len(t, res.DecodedArgs, 1)
	assert.Equal(t, uint32(7), res.DecodedArgs[0])
	assert.Equal(t, []string{"index (u32) = 7"}, res.Args)
}

func TestSimulateRejectsNonSorobanAndMultiOpEnvelopes(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
	})
	require.NoError(t, err)

	account := txnbuild.NewSimpleAccount(stellartest.SourceAccount, 0)
	bumpOp := &txnbuild.BumpSequence{BumpTo: 100}

	t.Run("non-Soroban envelope is rejected", func(t *testing.T) {
		tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &account,
			IncrementSequenceNum: true,
			Operations:           []txnbuild.Operation{bumpOp},
			BaseFee:              txnbuild.MinBaseFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
		})
		require.NoError(t, err)
		b64, err := tx.Base64()
		require.NoError(t, err)

		_, err = p.Simulate(context.Background(), probe.SimulateRequest{Envelope: b64})
		require.ErrorIs(t, err, stellar.ErrInvalidEnvelope)
		assert.Contains(t, err.Error(), "non-Soroban operation")
	})

	t.Run("multi-operation envelope is rejected", func(t *testing.T) {
		tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
			SourceAccount:        &account,
			IncrementSequenceNum: true,
			Operations:           []txnbuild.Operation{bumpOp, bumpOp},
			BaseFee:              txnbuild.MinBaseFee,
			Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
		})
		require.NoError(t, err)
		b64, err := tx.Base64()
		require.NoError(t, err)

		_, err = p.Simulate(context.Background(), probe.SimulateRequest{Envelope: b64})
		require.ErrorIs(t, err, stellar.ErrInvalidEnvelope)
		assert.Contains(t, err.Error(), "2 operations")
	})

	t.Run("malformed base64 envelope is rejected", func(t *testing.T) {
		_, err = p.Simulate(context.Background(), probe.SimulateRequest{Envelope: "not-valid-xdr!!!"})
		require.ErrorIs(t, err, stellar.ErrInvalidEnvelope)
	})
}

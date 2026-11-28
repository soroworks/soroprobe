package probe_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/keypair"
	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestRecommendFeesFromPercentiles(t *testing.T) {
	stats := protocol.GetFeeStatsResponse{
		SorobanInclusionFee: protocol.FeeDistribution{P10: 100, P50: 150, P90: 2500, TransactionCount: 184, LedgerCount: 50},
		LatestLedger:        77,
	}
	rec := probe.RecommendFees(12_279, 100, stats)

	require.Len(t, rec.Options, 3)
	want := []struct {
		level      string
		percentile int
		fee        int64
	}{{"low", 10, 100}, {"median", 50, 150}, {"high", 90, 2500}}
	for i, w := range want {
		o := rec.Options[i]
		assert.Equal(t, w.level, o.Level)
		assert.Equal(t, w.percentile, o.Percentile)
		assert.Equal(t, w.fee, o.InclusionFee)
		assert.Equal(t, 12_279+w.fee, o.TotalFee)
	}
	assert.Equal(t, "0.0014779", rec.Options[2].TotalXLM)
	assert.Equal(t, uint32(184), rec.TransactionCount)
	assert.Equal(t, uint32(50), rec.LedgerCount)
	assert.Equal(t, uint32(77), rec.LatestLedger)
	assert.Contains(t, rec.Source, "getFeeStats")
	assert.Empty(t, rec.Note)
}

func TestRecommendFeesNeverGoesBelowTheMinimum(t *testing.T) {
	rec := probe.RecommendFees(500, 0, protocol.GetFeeStatsResponse{})
	for _, o := range rec.Options {
		assert.Equal(t, probe.DefaultBaseFee, o.InclusionFee, o.Level)
		assert.Equal(t, 500+probe.DefaultBaseFee, o.TotalFee)
	}
	assert.Contains(t, rec.Note, "no Soroban transactions")
}

func TestFormatXLM(t *testing.T) {
	assert.Equal(t, "0.0000001", probe.FormatXLM(1))
	assert.Equal(t, "1.0000000", probe.FormatXLM(10_000_000))
	assert.Equal(t, "123.4567890", probe.FormatXLM(1_234_567_890))
	assert.Equal(t, "-0.5000000", probe.FormatXLM(-5_000_000))
}

func TestSimulateRecommendsFeesFromTheFixture(t *testing.T) {
	fake := stellartest.NewFake(t)
	res, err := newProber(t, fake, health.Thresholds{}).Simulate(context.Background(),
		probe.SimulateRequest{ContractID: stellartest.SACContract, Function: "decimals"})
	require.NoError(t, err)
	require.NotNil(t, res.Fees)
	assert.Equal(t, res.Cost.ResourceFee, res.Fees.ResourceFee)
	assert.Equal(t, int64(2500), res.Fees.Options[2].InclusionFee, "p90 of fee_stats.json")
	assert.Equal(t, fake.FeeStats.LatestLedger, res.Fees.LatestLedger)
}

func TestSimulateNotesUnavailableFeeStats(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.FeeStatsErr = errors.New("method not found")
	res, err := newProber(t, fake, health.Thresholds{}).Simulate(context.Background(),
		probe.SimulateRequest{ContractID: stellartest.SACContract, Function: "decimals"})
	require.NoError(t, err)
	assert.Nil(t, res.Fees)
	assert.Contains(t, res.Notes, "fee stats unavailable: method not found")
}

func TestSimulateAsAGivenSource(t *testing.T) {
	kp := keypair.MustRandom()
	fake := stellartest.NewFake(t)
	var id xdr.AccountId
	require.NoError(t, id.SetAddress(kp.Address()))
	fake.StoreAccount(t, xdr.AccountEntry{AccountId: id, Balance: 1e9, SeqNum: 4242, Thresholds: xdr.Thresholds{1, 0, 0, 0}})
	p := newProber(t, fake, health.Thresholds{})

	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract, Function: "decimals", Source: kp.Address(),
	})
	require.NoError(t, err)
	assert.Equal(t, kp.Address(), res.Source)

	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(fake.SimulateRequests[0].Transaction, &env))
	assert.Equal(t, kp.Address(), env.SourceAccount().ToAccountId().Address())
	assert.Equal(t, int64(4243), env.SeqNum(), "the next sequence number of the real account")
}

func TestSimulateSourceErrors(t *testing.T) {
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.Thresholds{})
	ctx := context.Background()
	req := probe.SimulateRequest{ContractID: stellartest.SACContract, Function: "decimals"}

	req.Source = keypair.MustRandom().Address()
	_, err := p.Simulate(ctx, req)
	require.ErrorIs(t, err, probe.ErrAccountNotFound)

	req.Source = keypair.MustRandom().Seed()
	_, err = p.Simulate(ctx, req)
	require.ErrorIs(t, err, probe.ErrSecretKey)
	assert.NotContains(t, err.Error(), req.Source, "the secret is never echoed")

	req.Source = "not-an-account"
	_, err = p.Simulate(ctx, req)
	require.Error(t, err)

	assert.Empty(t, fake.SimulateRequests, "nothing is simulated with a bad source")
}

func TestAuthEntriesAreJudgedAgainstTheRequestedSource(t *testing.T) {
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.Thresholds{})
	ctx := context.Background()

	base, err := p.Simulate(ctx, probe.SimulateRequest{ContractID: stellartest.SACContract, Function: "transfer"})
	require.NoError(t, err)
	require.NotEmpty(t, base.Auth)
	signer := base.Auth[0].Address
	require.NotEmpty(t, signer)

	// Simulated as the account that must authorize, the entry is covered by
	// the transaction's own signature.
	var id xdr.AccountId
	if err := id.SetAddress(signer); err != nil {
		t.Skipf("fixture signer %s is not an account", signer)
	}
	fake.StoreAccount(t, xdr.AccountEntry{AccountId: id, Balance: 1e9, SeqNum: 1, Thresholds: xdr.Thresholds{1, 0, 0, 0}})
	res, err := p.Simulate(ctx, probe.SimulateRequest{ContractID: stellartest.SACContract, Function: "transfer", Source: signer})
	require.NoError(t, err)
	assert.True(t, res.Auth[0].IsSourceAccount)
	assert.Equal(t, signer != stellartest.SourceAccount, !base.Auth[0].IsSourceAccount)
}

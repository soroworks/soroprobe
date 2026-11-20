package probe_test

import (
	"context"
	"github.com/stellar/go-stellar-sdk/xdr"
	"math/big"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func i128Val(n int64) xdr.ScVal {
	p := xdr.Int128Parts{Hi: 0, Lo: xdr.Uint64(uint64(n))} //nolint:gosec // G115: test values are non-negative
	return xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &p}
}

func TestBalanceFormatsWithDecimalsAndSymbol(t *testing.T) {
	fake := stellartest.NewFake(t)
	base := fake.Simulations["decimals"]
	fake.Simulations["decimals"] = simResult(t, base, u32Val(7))
	fake.Simulations["symbol"] = simResult(t, base, strVal("USDC"))
	fake.Simulations["balance"] = simResult(t, base, i128Val(125_000_000))

	p := standardProber(t, fake, nil)
	res, err := p.Balance(context.Background(), probe.BalanceRequest{
		Token:   stellartest.WasmContract,
		Holders: []string{stellartest.WasmContract, "not-an-address"},
	})
	require.NoError(t, err)
	assert.Equal(t, "USDC", res.Symbol)
	assert.EqualValues(t, 7, res.Decimals)
	require.Len(t, res.Balances, 2)
	assert.Equal(t, "12.5", res.Balances[0].Balance)
	assert.Equal(t, "125000000", res.Balances[0].Raw)
	assert.Contains(t, res.Balances[1].Error, "invalid address")
}

func TestBalanceNeedsAHolder(t *testing.T) {
	p := standardProber(t, stellartest.NewFake(t), nil)
	_, err := p.Balance(context.Background(), probe.BalanceRequest{Token: stellartest.WasmContract})
	require.Error(t, err)
}

func TestFormatUnits(t *testing.T) {
	cases := []struct {
		n        int64
		decimals uint32
		want     string
	}{
		{0, 7, "0"}, {1, 7, "0.0000001"}, {10_000_000, 7, "1"}, {-125, 2, "-1.25"}, {42, 0, "42"}, {5, 3, "0.005"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, probe.FormatUnits(big.NewInt(c.n), c.decimals))
	}
}

// BenchmarkFormatUnits tracks the cost of formatting a raw balance for a
// 7-decimal token, which is the common case for Stellar assets.
func BenchmarkFormatUnits(b *testing.B) {
	n := big.NewInt(125_000_000)

	b.ReportAllocs()
	var got string
	for b.Loop() {
		got = probe.FormatUnits(n, 7)
	}
	runtime.KeepAlive(got)
}

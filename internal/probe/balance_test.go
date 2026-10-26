package probe_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestFormatTokenAmount(t *testing.T) {
	assert.Equal(t, "1.5000000 USDC", probe.FormatTokenAmount("15000000", 7, "USDC"))
	assert.Equal(t, "0.0000001 XLM", probe.FormatTokenAmount("1", 7, "XLM"))
	assert.Equal(t, "0 USDC", probe.FormatTokenAmount("0", 7, "USDC"))
	assert.Equal(t, "100.0000000 TOKEN", probe.FormatTokenAmount("1000000000", 7, "TOKEN"))
}

func TestReadHoldersFromFile(t *testing.T) {
	input := `
# Comment line
GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF
GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK

# Another comment
CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM
`
	holders, err := probe.ReadHoldersFromFile(strings.NewReader(input))
	require.NoError(t, err)
	require.Len(t, holders, 3)
	assert.Equal(t, "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF", holders[0])
	assert.Equal(t, "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK", holders[1])
	assert.Equal(t, "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAD2KM", holders[2])
}

func TestGetTokenBalances(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	report, err := p.GetTokenBalances(context.Background(), stellartest.SACContract, []string{
		"GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK",
	})
	require.NoError(t, err)
	assert.Equal(t, uint32(7), report.Token.Decimals)
	require.Len(t, report.Holders, 1)
	assert.Equal(t, "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK", report.Holders[0].Address)
}

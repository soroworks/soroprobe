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

func TestInspectSAC_NativeAsset(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := p.Inspect(context.Background(), probe.InspectRequest{
		ContractID: stellartest.SACContract,
	})
	require.NoError(t, err)
	assert.Equal(t, "stellar_asset", res.Executable)
	require.NotNil(t, res.SACDetails)
	assert.True(t, res.SACDetails.IsNative)
	assert.Contains(t, res.SACDetails.AssetCode, "native")
	assert.NotEmpty(t, res.SACDetails.CodeTTLExplanation)
	assert.Nil(t, res.Code)
}

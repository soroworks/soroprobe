package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestCheckCmd_UpgradeabilityFlag(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	out, err := runWithFake(t, fake, "check", stellartest.WasmContract, "--upgradeability")
	require.NoError(t, err)
	assert.Contains(t, out, "Upgradeability Report")
	assert.Contains(t, out, "Code Modified")
}

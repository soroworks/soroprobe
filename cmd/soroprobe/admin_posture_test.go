package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestCheckCmd_AdminFnFlag(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	out, err := runWithFake(t, fake, "check", stellartest.SACContract, "--admin-fn", "decimals")
	require.NoError(t, err)
	assert.Contains(t, out, "admin_posture")
}

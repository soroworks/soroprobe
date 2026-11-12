package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestInspectCmd_SACDetails(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	out, err := runWithFake(t, fake, "inspect", stellartest.SACContract)
	require.NoError(t, err)
	assert.Contains(t, out, "stellar asset details")
	assert.Contains(t, out, "native (XLM)")
	assert.Contains(t, out, "AUTH_REQUIRED=")
}

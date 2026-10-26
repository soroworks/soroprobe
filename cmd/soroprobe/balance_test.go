package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestBalanceCmd_ArgHolders(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	out, err := runWithFake(t, fake, "balance", stellartest.SACContract, "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK")
	require.NoError(t, err)
	assert.Contains(t, out, "Token:")
	assert.Contains(t, out, "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK")
}

func TestBalanceCmd_FileHolders(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "holders.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK
"), 0600))

	out, err := runWithFake(t, fake, "balance", stellartest.SACContract, "--file", filePath)
	require.NoError(t, err)
	assert.Contains(t, out, "Token:")
	assert.Contains(t, out, "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK")
}

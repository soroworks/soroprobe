package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi/abitest"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func writeConstructorWasm(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contract.wasm")
	wasm := abitest.Wasm(t, abitest.Fn("__constructor", nil, abitest.Input("cap", abitest.U32)))
	require.NoError(t, os.WriteFile(path, wasm, 0o600))
	return path
}

func deployCmdFake(t *testing.T) *stellartest.Fake {
	t.Helper()
	fake := stellartest.NewFake(t)
	fake.Simulations[stellartest.UploadContractWasm] = fake.Simulations["decimals"]
	fake.Simulations[stellartest.CreateContract] = fake.Simulations["decimals"]
	return fake
}

func TestEstimateDeployCommandJSON(t *testing.T) {
	path := writeConstructorWasm(t)
	out, err := runWithFake(t, deployCmdFake(t), "--json", "estimate-deploy", path, "5")
	require.NoError(t, err)

	var res probe.DeployEstimate
	require.NoError(t, json.Unmarshal([]byte(out), &res))
	assert.True(t, res.Upload.Success)
	assert.True(t, res.Instantiate.Success)
	assert.Equal(t, []string{"u32:5"}, res.ConstructorArgs)
	assert.NotEmpty(t, res.ContractID)
}

func TestEstimateDeployCommandText(t *testing.T) {
	path := writeConstructorWasm(t)
	out, err := runWithFake(t, deployCmdFake(t), "estimate-deploy", "--source", stellartest.SourceAccount, path, "5")
	require.NoError(t, err)
	for _, want := range []string{"upload", "instantiate", "total estimated fee", "a real deploy gets a different one", "nothing was submitted", "fn __constructor(cap: u32)"} {
		assert.Contains(t, out, want)
	}
}

func TestEstimateDeployCommandMissingFile(t *testing.T) {
	_, err := runWithFake(t, deployCmdFake(t), "estimate-deploy", filepath.Join(t.TempDir(), "nope.wasm"))
	require.ErrorContains(t, err, "read wasm")
}

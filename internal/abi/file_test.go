package abi_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
)

// zkvoteInterfaceJSON is an example of SoroVault's ABI JSON for zkvote.
const zkvoteInterfaceJSON = `{
  "contract_id": "CCLV77FYLRJMBGTILTKVIM76SI6JY56H7KTT47HE26UF4F265UYRDSR4",
  "interface": {
    "functions": [
      {
        "name": "vote",
        "inputs": [
          {"name": "proposal_id", "type": {"kind": "u32", "display": "u32"}},
          {"name": "voter", "type": {"kind": "address", "display": "Address"}},
          {"name": "proof", "type": {"kind": "bytes", "display": "Bytes"}}
        ],
        "outputs": [{"kind": "bool", "display": "bool"}]
      },
      {
        "name": "get_proposal",
        "inputs": [
          {"name": "proposal_id", "type": {"kind": "u32", "display": "u32"}}
        ],
        "outputs": [{"kind": "string", "display": "String"}]
      }
    ]
  }
}`

func TestFileSourceZKVoteInterface(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "zkvote_abi.json")
	require.NoError(t, os.WriteFile(path, []byte(zkvoteInterfaceJSON), 0600))

	source, err := abi.NewFileSource(path)
	require.NoError(t, err)

	// Serves for any contract ID
	iface, err := source.Interface(context.Background(), "testnet", "ANY_CONTRACT_ID")
	require.NoError(t, err)
	require.NotNil(t, iface)

	fn, err := iface.Lookup("vote")
	require.NoError(t, err)
	assert.Equal(t, "vote", fn.Name)
	assert.Equal(t, "fn vote(proposal_id: u32, voter: Address, proof: Bytes) -> bool", fn.Signature())

	// Lookup nonexistent function
	_, err = iface.Lookup("nonexistent")
	require.Error(t, err)
	assert.ErrorIs(t, err, abi.ErrNoSuchFunction)
}

func TestFileSourceDirectFunctionsFormat(t *testing.T) {
	t.Parallel()

	raw := `{
    "functions": [
      {
        "name": "ping",
        "inputs": [],
        "outputs": []
      }
    ]
  }`
	dir := t.TempDir()
	path := filepath.Join(dir, "direct.json")
	require.NoError(t, os.WriteFile(path, []byte(raw), 0600))

	source, err := abi.NewFileSource(path)
	require.NoError(t, err)

	iface, err := source.Interface(context.Background(), "public", "ANY")
	require.NoError(t, err)
	fn, err := iface.Lookup("ping")
	require.NoError(t, err)
	assert.Equal(t, "ping", fn.Name)
}

func TestFileSourceMissingFile(t *testing.T) {
	t.Parallel()

	_, err := abi.NewFileSource("does_not_exist.json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read abi file")
}

func TestFileSourceMalformedFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "malformed.json")
	require.NoError(t, os.WriteFile(path, []byte("{not json"), 0600))

	_, err := abi.NewFileSource(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse abi file")

	emptyPath := filepath.Join(dir, "empty_iface.json")
	require.NoError(t, os.WriteFile(emptyPath, []byte(`{"interface": {"functions": []}}`), 0600))
	_, err = abi.NewFileSource(emptyPath)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "is malformed")
}

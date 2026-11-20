package abi_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/abi/abitest"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestChainSourceDecodesRealWasmFixtureAndCachesByHash(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	source := abi.NewChainSource(fake)
	iface, err := source.Interface(context.Background(), "testnet", stellartest.WasmContract)
	require.NoError(t, err)
	fn, err := iface.Lookup("decimals")
	require.NoError(t, err)
	assert.Equal(t, "fn decimals() -> u32", fn.Signature())
	assert.Len(t, fake.EntryRequests, 2, "the contract instance and code are read once")

	_, err = source.Interface(context.Background(), "testnet", stellartest.WasmContract)
	require.NoError(t, err)
	assert.Len(t, fake.EntryRequests, 3, "the decoded spec is cached by Wasm hash")
}

func TestChainSourceReportsNoWasmForSAC(t *testing.T) {
	t.Parallel()

	source := abi.NewChainSource(stellartest.NewFake(t))
	_, err := source.Interface(context.Background(), "testnet", stellartest.SACContract)
	assert.ErrorIs(t, err, abi.ErrNoWasm)
}

func TestChainSourceCodeReturnsTheLiveWasmAndItsHash(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	wasm := abitest.Wasm(t, abitest.Fn("get", nil))
	hash := fake.DeployWasm(t, stellartest.UndeployedContract, wasm)

	got, gotHash, err := abi.NewChainSource(fake).Code(context.Background(), stellartest.UndeployedContract)
	require.NoError(t, err)
	assert.Equal(t, wasm, got)
	assert.Equal(t, hash, gotHash)
	assert.Equal(t, fmt.Sprintf("%x", sha256.Sum256(wasm)), gotHash)
}

func TestChainSourceCodeErrors(t *testing.T) {
	t.Parallel()

	source := abi.NewChainSource(stellartest.NewFake(t))

	_, _, err := source.Code(context.Background(), stellartest.SACContract)
	assert.ErrorIs(t, err, abi.ErrNoWasm)

	_, _, err = source.Code(context.Background(), stellartest.UndeployedContract)
	assert.ErrorIs(t, err, abi.ErrUnknownContract)

	_, _, err = source.Code(context.Background(), "not-a-contract")
	require.Error(t, err)
}

func TestParseMeta(t *testing.T) {
	t.Parallel()

	wasm := abitest.WasmWithMeta(t, nil, []abitest.Meta{
		{Key: "rsver", Val: "1.91.0"},
		{Key: "source_repo", Val: "https://github.com/example/token"},
		{Key: "commit_hash", Val: "abc123"},
		{Key: "bldimg", Val: "example/build@sha256:00"},
		{Key: "commit_hash", Val: "def456"},
	})
	meta, err := abi.ParseMeta(wasm)
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/example/token", meta["source_repo"])
	assert.Equal(t, "example/build@sha256:00", meta["bldimg"])
	assert.Equal(t, "def456", meta["commit_hash"], "a repeated key keeps its last value")
}

func TestParseMetaWithoutASectionIsEmptyNotAnError(t *testing.T) {
	t.Parallel()

	meta, err := abi.ParseMeta(abitest.Wasm(t, abitest.Fn("get", nil)))
	require.NoError(t, err)
	assert.Empty(t, meta)

	_, err = abi.ParseMeta([]byte("not wasm"))
	require.Error(t, err)
}

func TestParseMetaEdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		wasm    []byte
		want    map[string]string
		wantErr bool
	}{
		{name: "nil input", wasm: nil, wantErr: true},
		{name: "empty input", wasm: []byte{}, wantErr: true},
		{
			name: "empty key and value are kept",
			wasm: abitest.WasmWithMeta(t, nil, []abitest.Meta{{Key: "", Val: ""}}),
			want: map[string]string{"": ""},
		},
		{
			name: "empty value does not drop the key",
			wasm: abitest.WasmWithMeta(t, nil, []abitest.Meta{{Key: "rsver", Val: ""}, {Key: "bldimg", Val: "img"}}),
			want: map[string]string{"rsver": "", "bldimg": "img"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			meta, err := abi.ParseMeta(tt.wasm)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, meta)
		})
	}
}

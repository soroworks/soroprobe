package abi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
)

func TestSoroVault_RegisteredWasmHash(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/contracts/CAAAA":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"contract_id":"CAAAA","wasm_hash":"abc123hash","interface":{"functions":[{"name":"hello"}]}}`))
		case "/api/contracts/CBBBB":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"error":"not found"}`))
		case "/api/contracts/CCCC":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"contract_id":"CCCC","interface":{"functions":[]}}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer ts.Close()

	client, err := abi.NewSoroVault(ts.URL, ts.Client(), 2*time.Second)
	require.NoError(t, err)

	ctx := context.Background()

	// Successful lookup
	hash, err := client.RegisteredWasmHash(ctx, "testnet", "CAAAA")
	require.NoError(t, err)
	assert.Equal(t, "abc123hash", hash)

	// Full metadata lookup
	meta, err := client.Metadata(ctx, "testnet", "CAAAA")
	require.NoError(t, err)
	assert.Equal(t, "CAAAA", meta.ContractID)
	assert.Equal(t, "abc123hash", meta.WasmHash)
	assert.NotNil(t, meta.Interface)

	// Unknown contract
	_, err = client.RegisteredWasmHash(ctx, "testnet", "CBBBB")
	require.Error(t, err)
	assert.ErrorIs(t, err, abi.ErrUnknownContract)

	// Missing wasm_hash in response
	_, err = client.RegisteredWasmHash(ctx, "testnet", "CCCC")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no wasm_hash")
}

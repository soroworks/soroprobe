package soroforge_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/integration/soroforge"
)

func TestHTTPClient_ListContracts_Pagination(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer my-secret-token", r.Header.Get("Authorization"))
		assert.Equal(t, "testnet", r.URL.Query().Get("network"))

		switch r.URL.Query().Get("page") {
		case "1":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"contracts": [
					{"id": "CAAAA", "name": "token", "wasm_hash": "hash1"},
					{"id": "CBBBB", "name": "vault", "wasm_hash": "hash2"}
				],
				"has_more": true,
				"next_page": 2
			}`))
		case "2":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"contracts": [
					{"id": "CCCC", "name": "oracle", "wasm_hash": "hash3"}
				],
				"has_more": false,
				"next_page": 0
			}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer ts.Close()

	client, err := soroforge.New(ts.URL, "my-secret-token", ts.Client())
	require.NoError(t, err)

	contracts, err := client.ListContracts(context.Background(), "testnet")
	require.NoError(t, err)
	require.Len(t, contracts, 3)

	assert.Equal(t, "CAAAA", contracts[0].ContractID)
	assert.Equal(t, "token", contracts[0].Name)
	assert.Equal(t, "hash1", contracts[0].WasmHash)

	assert.Equal(t, "CBBBB", contracts[1].ContractID)
	assert.Equal(t, "hash2", contracts[1].WasmHash)

	assert.Equal(t, "CCCC", contracts[2].ContractID)
	assert.Equal(t, "hash3", contracts[2].WasmHash)
}

func TestHTTPClient_ListContracts_AuthError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "invalid or expired token"}`))
	}))
	defer ts.Close()

	client, err := soroforge.New(ts.URL, "bad-token", ts.Client())
	require.NoError(t, err)

	_, err = client.ListContracts(context.Background(), "testnet")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication failed")
	assert.Contains(t, err.Error(), "invalid or expired token")
}

func TestHTTPClient_ListContracts_DirectArray(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[
			{"contract_id": "CAAAA", "name": "token", "wasm_hash": "hash1"}
		]`))
	}))
	defer ts.Close()

	client, err := soroforge.New(ts.URL, "", ts.Client())
	require.NoError(t, err)

	contracts, err := client.ListContracts(context.Background(), "")
	require.NoError(t, err)
	require.Len(t, contracts, 1)
	assert.Equal(t, "CAAAA", contracts[0].ContractID)
}

func TestHTTPClient_New_ValidatesURL(t *testing.T) {
	_, err := soroforge.New("ftp://invalid.com", "", nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be an absolute http:// or https:// URL")
}

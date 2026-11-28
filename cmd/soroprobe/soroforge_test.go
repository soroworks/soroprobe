package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestCheckFromSoroForge_SuccessAndDrift(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer valid-token", r.Header.Get("Authorization"))
		assert.Equal(t, "testnet", r.URL.Query().Get("network"))

		w.Header().Set("Content-Type", "application/json")
		// CCLV77FYLRJMBGTILTKVIM76SI6JY56H7KTT47HE26UF4F265UYRDSR4 has WasmCode in fake fixture
		// Return it with a drifting hash to verify drift detection warning
		fmt.Fprintf(w, `{
			"contracts": [
				{"contract_id": %q, "name": "sample_contract", "wasm_hash": "differentrecordedhash123"}
			],
			"has_more": false
		}`, stellartest.WasmContract)
	}))
	defer ts.Close()

	out, err := runWithFake(t, fake, "check", "--from-soroforge", ts.URL, "--network", "testnet", "--soroforge-token", "valid-token")
	require.NoError(t, err)
	assert.Contains(t, out, "sample_contract")
	assert.Contains(t, out, "WASM drift detected")
}

func TestCheckFromSoroForge_AuthError(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": "unauthorized read-only access required"}`))
	}))
	defer ts.Close()

	_, err := runWithFake(t, fake, "check", "--from-soroforge", ts.URL, "--network", "testnet", "--soroforge-token", "bad-token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication failed")
	assert.Contains(t, err.Error(), "unauthorized read-only access required")
}

func TestCheckFromSoroForge_ConflictFlags(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	// Conflict with positional contract ID
	_, err := runWithFake(t, fake, "check", "CAAAA", "--from-soroforge", "http://localhost:8080")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "give a contract ID or --from-soroforge, not both")

	// Conflict with --file
	_, err = runWithFake(t, fake, "check", "--file", "checks.json", "--from-soroforge", "http://localhost:8080")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "give --file or --from-soroforge, not both")
}

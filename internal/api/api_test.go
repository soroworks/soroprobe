package api_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/api"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func newServer(t *testing.T, fake *stellartest.Fake) http.Handler {
	t.Helper()

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
	})
	require.NoError(t, err)

	return api.New(api.Options{Prober: p}).Handler()
}

// do issues a request and decodes the JSON body into v when v is non-nil.
func do(t *testing.T, h http.Handler, method, target, body string, v any) *httptest.ResponseRecorder {
	t.Helper()

	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if v != nil {
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), v), "body was: %s", rec.Body.String())
	}
	return rec
}

func TestLiveness(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var body map[string]string
	rec := do(t, h, http.MethodGet, "/healthz", "", &body)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "ok", body["status"])
}

func TestLivenessDoesNotTouchTheNetwork(t *testing.T) {
	t.Parallel()

	// A liveness probe must not fail just because an upstream RPC endpoint is
	// briefly unavailable.
	fake := stellartest.NewFake(t)
	fake.EntriesErr = errors.New("rpc down")
	fake.SimulateErr = errors.New("rpc down")
	h := newServer(t, fake)

	rec := do(t, h, http.MethodGet, "/healthz", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, fake.EntryRequests)
	assert.Empty(t, fake.SimulateRequests)
}

func TestSimulateEndpoint(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var result probe.SimulateResult
	rec := do(t, h, http.MethodPost, "/v1/simulate",
		`{"contract_id":"`+stellartest.SACContract+`","function":"decimals"}`, &result)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, result.Success)
	assert.EqualValues(t, 7, result.ReturnValue)
	assert.Positive(t, result.Cost.Instructions)
}

func TestSimulateEndpointWithArgs(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	h := newServer(t, fake)

	rec := do(t, h, http.MethodPost, "/v1/simulate",
		`{"contract_id":"`+stellartest.SACContract+`","function":"decimals","args":["u32:1"]}`, nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, fake.SimulateRequests, 1)
	args, err := stellartest.ArgsOf(fake.SimulateRequests[0].Transaction)
	require.NoError(t, err)
	require.Len(t, args, 1)
	assert.EqualValues(t, 1, *args[0].U32)
}

func TestSimulateEndpointReportsContractFailureAs200(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var result probe.SimulateResult
	rec := do(t, h, http.MethodPost, "/v1/simulate",
		`{"contract_id":"`+stellartest.SACContract+`","function":"no_such_fn"}`, &result)

	// The request itself succeeded; the contract rejected the call. Those are
	// different things and the status code must not conflate them.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, result.Success)
	assert.NotEmpty(t, result.Error)
}

func TestSimulateEndpointBadRequests(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	tests := []struct {
		name string
		body string
	}{
		{"malformed json", `{`},
		{"missing contract id", `{"function":"decimals"}`},
		{"missing function", `{"contract_id":"` + stellartest.SACContract + `"}`},
		{"invalid contract id", `{"contract_id":"nope","function":"decimals"}`},
		{"bad argument", `{"contract_id":"` + stellartest.SACContract + `","function":"decimals","args":["u32:xyz"]}`},
		{"unknown field", `{"contract_id":"x","function":"y","bogus":1}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, h, http.MethodPost, "/v1/simulate", tt.body, nil)
			assert.Equal(t, http.StatusBadRequest, rec.Code, "body was: %s", rec.Body.String())
			assert.Contains(t, rec.Body.String(), "error")
		})
	}
}

func TestSimulateEndpointUpstreamFailureIsBadGateway(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	fake.SimulateErr = errors.New("rpc unreachable")
	h := newServer(t, fake)

	rec := do(t, h, http.MethodPost, "/v1/simulate",
		`{"contract_id":"`+stellartest.SACContract+`","function":"decimals"}`, nil)
	assert.Equal(t, http.StatusBadGateway, rec.Code)
}

func TestInspectEndpoint(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var result probe.InspectResult
	rec := do(t, h, http.MethodGet, "/v1/inspect/"+stellartest.WasmContract, "", &result)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, result.Deployed)
	assert.Equal(t, "wasm", result.Executable)
	require.NotNil(t, result.Code)
}

func TestInspectEndpointWithDataKeys(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var result probe.InspectResult
	rec := do(t, h, http.MethodGet,
		"/v1/inspect/"+stellartest.SACContract+"?key=sym:Admin&key=sym:Balance", "", &result)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.Len(t, result.Data, 2)
	assert.Equal(t, "sym:Admin", result.Data[0].Key)
}

func TestInspectEndpointRejectsUnknownDurability(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	rec := do(t, h, http.MethodGet,
		"/v1/inspect/"+stellartest.SACContract+"?durability=forever", "", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestInspectEndpointInvalidContract(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	rec := do(t, h, http.MethodGet, "/v1/inspect/nope", "", nil)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCheckEndpoint(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var result probe.CheckResult
	rec := do(t, h, http.MethodGet, "/v1/check/"+stellartest.SACContract+"?fn=decimals", "", &result)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, result.OK)
	require.NotNil(t, result.Simulate)
}

func TestCheckEndpointFailingCheckIsStill200(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	var result probe.CheckResult
	rec := do(t, h, http.MethodGet, "/v1/check/"+stellartest.UndeployedContract, "", &result)

	// An unhealthy contract is a successful request reporting bad news.
	// Callers read "ok" rather than the status code.
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.False(t, result.OK)
}

func TestCheckEndpointPassesArgs(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	h := newServer(t, fake)

	rec := do(t, h, http.MethodGet,
		"/v1/check/"+stellartest.SACContract+"?fn=decimals&arg=u32:1&arg=sym:x", "", nil)
	assert.Equal(t, http.StatusOK, rec.Code)

	require.Len(t, fake.SimulateRequests, 1)
	args, err := stellartest.ArgsOf(fake.SimulateRequests[0].Transaction)
	require.NoError(t, err)
	assert.Len(t, args, 2)
}

func TestNoWriteRoutesExist(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	// SoroProbe is read-only by design. These routes must not exist.
	for _, target := range []string{"/v1/send", "/v1/submit", "/v1/deploy", "/v1/restore"} {
		rec := do(t, h, http.MethodPost, target, `{}`, nil)
		assert.Equal(t, http.StatusNotFound, rec.Code, "%s should not exist", target)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	t.Parallel()

	h := newServer(t, stellartest.NewFake(t))

	rec := do(t, h, http.MethodGet, "/v1/simulate", "", nil)
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}

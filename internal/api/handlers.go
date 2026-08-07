package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
)

// maxBodyBytes caps request bodies; these payloads are tiny.
const maxBodyBytes = 64 << 10

// handleLiveness reports that the process is up. It deliberately does not call
// out to the network, so that a liveness probe does not fail because an
// upstream RPC endpoint is briefly unavailable.
func (s *Server) handleLiveness(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// simulateBody is the POST /v1/simulate payload.
type simulateBody struct {
	ContractID string   `json:"contract_id"`
	Function   string   `json:"function"`
	Args       []string `json:"args"`
}

func (s *Server) handleSimulate(w http.ResponseWriter, r *http.Request) {
	var body simulateBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if body.ContractID == "" || body.Function == "" {
		writeError(w, http.StatusBadRequest, errors.New("contract_id and function are required"))
		return
	}

	result, err := s.prober.Simulate(r.Context(), probe.SimulateRequest{
		ContractID: body.ContractID,
		Function:   body.Function,
		Args:       body.Args,
	})
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}

	// A simulation that the contract rejects is a valid, fully-formed answer,
	// so it is still 200. Callers read the "success" field.
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	durability, err := durabilityParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	result, err := s.prober.Inspect(r.Context(), probe.InspectRequest{
		ContractID:     chi.URLParam(r, "contract"),
		DataKeys:       r.URL.Query()["key"],
		DataDurability: durability,
	})
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	durability, err := durabilityParam(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}

	query := r.URL.Query()
	result, err := s.prober.Check(r.Context(), probe.CheckRequest{
		ContractID:     chi.URLParam(r, "contract"),
		Function:       query.Get("fn"),
		Args:           query["arg"],
		DataKeys:       query["key"],
		DataDurability: durability,
	})
	if err != nil {
		writeError(w, statusForError(err), err)
		return
	}

	// A failing check is a successful request that reports bad news, so the
	// body is returned with 200 and callers read "ok". Returning 5xx here would
	// conflate an unhealthy contract with a broken service.
	writeJSON(w, http.StatusOK, result)
}

func durabilityParam(r *http.Request) (health.Durability, error) {
	switch strings.ToLower(r.URL.Query().Get("durability")) {
	case "", "persistent":
		return health.DurabilityPersistent, nil
	case "temporary", "temp":
		return health.DurabilityTemporary, nil
	default:
		return "", fmt.Errorf("unknown durability %q (want persistent or temporary)", r.URL.Query().Get("durability"))
	}
}

// statusForError maps a probe error onto an HTTP status. Bad input from the
// caller is a 400; anything else is treated as an upstream failure.
func statusForError(err error) int {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "invalid contract id"),
		strings.Contains(msg, "argument "),
		strings.Contains(msg, "arg "),
		strings.Contains(msg, "function name is required"):
		return http.StatusBadRequest
	default:
		return http.StatusBadGateway
	}
}

package probe

import (
	"context"
	"fmt"
	"strings"
)

// TTLComparison details the TTL differences of a corresponding ledger entry across networks.
type TTLComparison struct {
	Kind        string `json:"kind"`
	Key         string `json:"key,omitempty"`
	LedgersA    uint32 `json:"ledgers_a"`
	LedgersB    uint32 `json:"ledgers_b"`
	DiffLedgers int64  `json:"diff_ledgers"`
	StatusA     string `json:"status_a"`
	StatusB     string `json:"status_b"`
}

// CompareResult reports a side-by-side comparison of a contract across two networks.
type CompareResult struct {
	NetworkA  string          `json:"network_a"`
	ContractA string          `json:"contract_a"`
	InspectA  *InspectResult  `json:"inspect_a,omitempty"`
	SimulateA *SimulateResult `json:"simulate_a,omitempty"`

	NetworkB  string          `json:"network_b"`
	ContractB string          `json:"contract_b"`
	InspectB  *InspectResult  `json:"inspect_b,omitempty"`
	SimulateB *SimulateResult `json:"simulate_b,omitempty"`

	WasmMatch bool   `json:"wasm_match"`
	WasmHashA string `json:"wasm_hash_a,omitempty"`
	WasmHashB string `json:"wasm_hash_b,omitempty"`

	TTLDiffs      []TTLComparison `json:"ttl_diffs,omitempty"`
	SimulateMatch *bool           `json:"simulate_match,omitempty"`
	Differences   []string        `json:"differences,omitempty"`
}

// CompareRequest configures cross-network comparison.
type CompareRequest struct {
	NetworkA  string
	ContractA string
	ProberA   *Prober

	NetworkB  string
	ContractB string
	ProberB   *Prober

	Function string
	Args     []string
}

// Compare inspects and optionally simulates the contract on both networks and compares results.
func Compare(ctx context.Context, req CompareRequest) (*CompareResult, error) {
	inspA, err := req.ProberA.Inspect(ctx, InspectRequest{ContractID: req.ContractA})
	if err != nil {
		return nil, fmt.Errorf("%s inspect failed: %w", req.NetworkA, err)
	}

	inspB, err := req.ProberB.Inspect(ctx, InspectRequest{ContractID: req.ContractB})
	if err != nil {
		return nil, fmt.Errorf("%s inspect failed: %w", req.NetworkB, err)
	}

	res := &CompareResult{
		NetworkA:  req.NetworkA,
		ContractA: req.ContractA,
		InspectA:  inspA,
		WasmHashA: inspA.WasmHash,

		NetworkB:  req.NetworkB,
		ContractB: req.ContractB,
		InspectB:  inspB,
		WasmHashB: inspB.WasmHash,
	}

	// Compare WASM hash
	if inspA.WasmHash != "" && inspB.WasmHash != "" {
		if strings.EqualFold(inspA.WasmHash, inspB.WasmHash) {
			res.WasmMatch = true
		} else {
			res.WasmMatch = false
			res.Differences = append(res.Differences, fmt.Sprintf("WASM hash mismatch: %s=%s vs %s=%s", req.NetworkA, inspA.WasmHash, req.NetworkB, inspB.WasmHash))
		}
	} else if inspA.WasmHash != inspB.WasmHash {
		res.WasmMatch = false
		res.Differences = append(res.Differences, fmt.Sprintf("WASM presence mismatch: %s has %q, %s has %q", req.NetworkA, inspA.WasmHash, req.NetworkB, inspB.WasmHash))
	} else {
		// Both empty (e.g. host contracts or undeployed)
		res.WasmMatch = inspA.Deployed && inspB.Deployed
	}

	// Compare instance TTL
	if inspA.Instance.Present && inspB.Instance.Present {
		diff := int64(inspA.Instance.LedgersRemaining) - int64(inspB.Instance.LedgersRemaining)
		res.TTLDiffs = append(res.TTLDiffs, TTLComparison{
			Kind:        "instance",
			LedgersA:    inspA.Instance.LedgersRemaining,
			LedgersB:    inspB.Instance.LedgersRemaining,
			DiffLedgers: diff,
			StatusA:     string(inspA.Instance.Status),
			StatusB:     string(inspB.Instance.Status),
		})
	}

	// Compare code TTL
	if inspA.Code != nil && inspB.Code != nil && inspA.Code.Present && inspB.Code.Present {
		diff := int64(inspA.Code.LedgersRemaining) - int64(inspB.Code.LedgersRemaining)
		res.TTLDiffs = append(res.TTLDiffs, TTLComparison{
			Kind:        "code",
			LedgersA:    inspA.Code.LedgersRemaining,
			LedgersB:    inspB.Code.LedgersRemaining,
			DiffLedgers: diff,
			StatusA:     string(inspA.Code.Status),
			StatusB:     string(inspB.Code.Status),
		})
	}

	// Optional simulation comparison
	if req.Function != "" {
		simA, err := req.ProberA.Simulate(ctx, SimulateRequest{
			ContractID: req.ContractA,
			Function:   req.Function,
			Args:       req.Args,
		})
		if err != nil {
			return nil, fmt.Errorf("%s simulate failed: %w", req.NetworkA, err)
		}
		res.SimulateA = simA

		simB, err := req.ProberB.Simulate(ctx, SimulateRequest{
			ContractID: req.ContractB,
			Function:   req.Function,
			Args:       req.Args,
		})
		if err != nil {
			return nil, fmt.Errorf("%s simulate failed: %w", req.NetworkB, err)
		}
		res.SimulateB = simB

		match := simA.Success == simB.Success && simA.Result == simB.Result
		res.SimulateMatch = &match
		if !match {
			res.Differences = append(res.Differences, fmt.Sprintf("Simulation mismatch for %s: %s (success=%v, res=%s) vs %s (success=%v, res=%s)",
				req.Function, req.NetworkA, simA.Success, simA.Result, req.NetworkB, simB.Success, simB.Result))
		}
	}

	return res, nil
}

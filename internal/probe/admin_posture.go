package probe

import (
	"context"
	"fmt"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// AdminPosture reports the trust model and security posture of a contract's controlling admin.
type AdminPosture struct {
	AdminAddress  string `json:"admin_address"`
	Kind          string `json:"kind"` // "account" or "contract"
	IsMultisig    bool   `json:"is_multisig"`
	SignerCount   int    `json:"signer_count,omitempty"`
	MasterWeight  uint32 `json:"master_weight,omitempty"`
	ThresholdLow  uint32 `json:"threshold_low,omitempty"`
	ThresholdMed  uint32 `json:"threshold_med,omitempty"`
	ThresholdHigh uint32 `json:"threshold_high,omitempty"`
	AdminWasmHash string `json:"admin_wasm_hash,omitempty"`
	IsMainnet     bool   `json:"is_mainnet"`
	SingleKeyRisk bool   `json:"single_key_risk"`
	Summary       string `json:"summary"`
}

// EvaluateAdminPosture resolves the admin address and checks signers/thresholds or contract WASM.
func (p *Prober) EvaluateAdminPosture(ctx context.Context, contractID, adminFn string) (*AdminPosture, error) {
	if adminFn == "" {
		adminFn = "admin"
	}

	sim, err := p.Simulate(ctx, SimulateRequest{
		ContractID: contractID,
		Function:   adminFn,
	})
	if err != nil {
		return nil, fmt.Errorf("calling %s failed: %w", adminFn, err)
	}
	if !sim.Success || sim.Result == "" {
		return nil, fmt.Errorf("resolving admin via %s failed: %s", adminFn, sim.Error)
	}

	rawAddr := strings.TrimSpace(sim.Result)
	// Strip scval formatting if present (e.g. addr:G... or Address(G...))
	rawAddr = strings.TrimPrefix(rawAddr, "addr:")
	rawAddr = strings.TrimPrefix(rawAddr, "address:")
	rawAddr = strings.Trim(rawAddr, `"'`)

	isMainnet := strings.EqualFold(p.network, "public") || strings.EqualFold(p.network, "mainnet")

	posture := &AdminPosture{
		AdminAddress: rawAddr,
		IsMainnet:    isMainnet,
	}

	if strings.HasPrefix(rawAddr, "G") {
		posture.Kind = "account"
		accID, err := stellar.ParseAccountID(rawAddr)
		if err != nil {
			return nil, fmt.Errorf("invalid admin account address %q: %w", rawAddr, err)
		}

		key := stellar.AccountKey(accID)
		b64Key, err := stellar.EncodeKey(key)
		if err != nil {
			return nil, err
		}

		resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{b64Key}})
		if err != nil {
			return nil, err
		}
		if len(resp.Entries) == 0 {
			posture.Summary = fmt.Sprintf("admin account %s does not exist on-chain", rawAddr)
			posture.SingleKeyRisk = true
			return posture, nil
		}

		data, err := stellar.DecodeEntryData(resp.Entries[0].DataXDR)
		if err != nil {
			return nil, err
		}
		acc := data.MustAccount()

		posture.MasterWeight = uint32(acc.Thresholds[0])
		posture.ThresholdLow = uint32(acc.Thresholds[1])
		posture.ThresholdMed = uint32(acc.Thresholds[2])
		posture.ThresholdHigh = uint32(acc.Thresholds[3])

		signers := 0
		if posture.MasterWeight > 0 {
			signers++
		}
		signers += len(acc.Signers)
		posture.SignerCount = signers
		posture.IsMultisig = signers > 1

		if !posture.IsMultisig {
			posture.SingleKeyRisk = true
			if isMainnet {
				posture.Summary = fmt.Sprintf("HIGH RISK: mainnet contract controlled by single-key admin (%s) with master weight %d (thresholds: low=%d, med=%d, high=%d)",
					rawAddr, posture.MasterWeight, posture.ThresholdLow, posture.ThresholdMed, posture.ThresholdHigh)
			} else {
				posture.Summary = fmt.Sprintf("WARN: controlled by single-key admin (%s) with master weight %d (thresholds: low=%d, med=%d, high=%d)",
					rawAddr, posture.MasterWeight, posture.ThresholdLow, posture.ThresholdMed, posture.ThresholdHigh)
			}
		} else {
			posture.Summary = fmt.Sprintf("multisig admin (%s) with %d signers (thresholds: low=%d, med=%d, high=%d)",
				rawAddr, posture.SignerCount, posture.ThresholdLow, posture.ThresholdMed, posture.ThresholdHigh)
		}
		return posture, nil
	}

	if strings.HasPrefix(rawAddr, "C") {
		posture.Kind = "contract"
		insp, err := p.Inspect(ctx, InspectRequest{ContractID: rawAddr})
		if err == nil && insp != nil {
			posture.AdminWasmHash = insp.WasmHash
		}
		posture.Summary = fmt.Sprintf("controlled by governance smart contract (%s) [WASM: %s]", rawAddr, posture.AdminWasmHash)
		return posture, nil
	}

	posture.Kind = "unknown"
	posture.Summary = fmt.Sprintf("admin resolved to unrecognized address format: %s", rawAddr)
	return posture, nil
}

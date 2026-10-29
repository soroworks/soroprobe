package probe

import (
	"context"
	"fmt"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar"
)

// UpgradeFunction describes a discovered function that can alter contract bytecode or implementation.
type UpgradeFunction struct {
	Name      string   `json:"name"`
	Inputs    []string `json:"inputs,omitempty"`
	Heuristic string   `json:"heuristic"`
}

// UpgradeabilityReport details whether and how a contract can be upgraded.
type UpgradeabilityReport struct {
	// Upgradeable reports whether any upgrade-like entrypoint was found.
	Upgradeable bool `json:"upgradeable"`
	// UpgradeFunctions lists the functions that appear to allow upgrading the contract.
	UpgradeFunctions []UpgradeFunction `json:"upgrade_functions,omitempty"`
	// AdminRequired describes the presumed authorization required to invoke the upgrade.
	AdminRequired string `json:"admin_required,omitempty"`
	// LastModifiedLedger is the ledger sequence when the contract's code entry was last modified.
	LastModifiedLedger uint32 `json:"last_modified_ledger,omitempty"`
	// LedgersSinceUpgrade is how many ledgers have passed since the code entry was last modified.
	LedgersSinceUpgrade uint32 `json:"ledgers_since_upgrade,omitempty"`
	// Note warns that this analysis is heuristic and best-effort.
	Note string `json:"note"`
}

// CheckUpgradeability inspects the contract's ABI and on-ledger code entry to evaluate upgradeability.
func (p *Prober) CheckUpgradeability(ctx context.Context, contractID string, inspect *InspectResult) (*UpgradeabilityReport, error) {
	rep := &UpgradeabilityReport{
		Note: "Upgradeability assessment is heuristic and best-effort; custom proxy patterns or internal upgrades may differ.",
	}

	// 1. Fetch code entry last modified ledger
	if inspect != nil && inspect.WasmHash != "" {
		codeHash, err := hexToHash(inspect.WasmHash)
		if err == nil {
			key, err := stellar.EncodeKey(stellar.CodeKey(codeHash))
			if err == nil {
				resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{key}})
				if err == nil && len(resp.Entries) > 0 {
					rep.LastModifiedLedger = resp.Entries[0].LastModifiedLedgerSeq
					if resp.LatestLedger >= rep.LastModifiedLedger {
						rep.LedgersSinceUpgrade = resp.LatestLedger - rep.LastModifiedLedger
					}
				}
			}
		}
	}

	// 2. Inspect ABI interface for upgrade-like entrypoints
	if p.abi != nil {
		iface, err := p.abi.Interface(ctx, p.network, contractID)
		if err == nil && iface != nil {
			for _, fn := range iface.Functions {
				lower := strings.ToLower(fn.Name)
				isUpgrade := strings.Contains(lower, "upgrade") ||
					strings.HasPrefix(lower, "set_wasm") ||
					strings.HasPrefix(lower, "update_wasm") ||
					strings.HasPrefix(lower, "update_contract") ||
					strings.HasPrefix(lower, "migrate")

				if isUpgrade {
					var inputs []string
					for _, in := range fn.Inputs {
						inputs = append(inputs, fmt.Sprintf("%s: %s", in.Name, in.Type.Display))
					}
					rep.UpgradeFunctions = append(rep.UpgradeFunctions, UpgradeFunction{
						Name:      fn.Name,
						Inputs:    inputs,
						Heuristic: "function name matches upgrade/code update convention",
					})
				}
			}
		}
	}

	rep.Upgradeable = len(rep.UpgradeFunctions) > 0

	// 3. Check for admin authority
	adminAddress := p.detectAdmin(ctx, contractID)
	if adminAddress != "" {
		rep.AdminRequired = fmt.Sprintf("governed by admin (%s)", adminAddress)
	} else if rep.Upgradeable {
		rep.AdminRequired = "requires contract authority (unresolved admin address)"
	} else {
		rep.AdminRequired = "none (immutable contract interface)"
	}

	return rep, nil
}

func (p *Prober) detectAdmin(ctx context.Context, contractID string) string {
	for _, fnName := range []string{"admin", "get_admin", "owner", "get_owner"} {
		sim, err := p.Simulate(ctx, SimulateRequest{
			ContractID: contractID,
			Function:   fnName,
		})
		if err == nil && sim.Success && sim.Result != "" && !strings.Contains(sim.Result, "error") {
			return sim.Result
		}
	}
	return ""
}

package probe

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/soroworks/soroprobe/internal/abi"
)

// StandardFunction specifies a required interface function under a standard.
type StandardFunction struct {
	Name     string
	Inputs   []string // expected input type displays (e.g. "Address", "i128", "u32")
	Output   string   // expected return type display
	ReadOnly bool
}

// StandardSpec defines an on-chain smart contract interface standard.
type StandardSpec struct {
	Name        string
	Description string
	Functions   []StandardFunction
}

// SEP41Standard defines the SEP-0041 Token Interface Specification.
var SEP41Standard = StandardSpec{
	Name:        "sep41",
	Description: "SEP-0041 Token Interface Specification",
	Functions: []StandardFunction{
		{Name: "allowance", Inputs: []string{"Address", "Address"}, Output: "i128", ReadOnly: false},
		{Name: "approve", Inputs: []string{"Address", "Address", "i128", "u32"}, Output: "", ReadOnly: false},
		{Name: "balance", Inputs: []string{"Address"}, Output: "i128", ReadOnly: false},
		{Name: "transfer", Inputs: []string{"Address", "Address", "i128"}, Output: "", ReadOnly: false},
		{Name: "transfer_from", Inputs: []string{"Address", "Address", "Address", "i128"}, Output: "", ReadOnly: false},
		{Name: "decimals", Inputs: nil, Output: "u32", ReadOnly: true},
		{Name: "name", Inputs: nil, Output: "String", ReadOnly: true},
		{Name: "symbol", Inputs: nil, Output: "String", ReadOnly: true},
	},
}

var standardsRegistry = map[string]StandardSpec{
	"sep41": SEP41Standard,
}

// CheckStandardCompliance verifies that a contract implements the named standard.
func (p *Prober) CheckStandardCompliance(ctx context.Context, contractID, standardName string) ([]Check, error) {
	spec, ok := standardsRegistry[strings.ToLower(strings.TrimSpace(standardName))]
	if !ok {
		return nil, fmt.Errorf("unrecognized standard %q (available: sep41)", standardName)
	}

	if p.abi == nil {
		return []Check{{
			Name:    "standard:" + spec.Name,
			Outcome: OutcomeWarn,
			Detail:  "cannot verify interface standard: no ABI/SoroVault source configured",
		}}, nil
	}

	iface, err := p.abi.Interface(ctx, p.network, contractID)
	if err != nil {
		return []Check{{
			Name:    "standard:" + spec.Name,
			Outcome: OutcomeWarn,
			Detail:  fmt.Sprintf("cannot verify interface standard: ABI lookup failed: %v", err),
		}}, nil
	}

	fnMap := make(map[string]abi.Function)
	for _, fn := range iface.Functions {
		fnMap[fn.Name] = fn
	}

	var checks []Check
	prefix := "standard:" + spec.Name + ":"

	for _, expected := range spec.Functions {
		actual, found := fnMap[expected.Name]
		if !found {
			checks = append(checks, Check{
				Name:    prefix + expected.Name,
				Outcome: OutcomeFail,
				Detail:  fmt.Sprintf("missing required function %s", expected.Name),
			})
			continue
		}

		// Verify input count
		if len(actual.Inputs) != len(expected.Inputs) {
			checks = append(checks, Check{
				Name:    prefix + expected.Name,
				Outcome: OutcomeFail,
				Detail:  fmt.Sprintf("parameter count mismatch: expected %d, got %d", len(expected.Inputs), len(actual.Inputs)),
			})
			continue
		}

		// Verify input types
		typesMatch := true
		for i, in := range actual.Inputs {
			expType := expected.Inputs[i]
			if !strings.EqualFold(in.Type.Display, expType) && !strings.EqualFold(in.Type.Kind, expType) {
				typesMatch = false
				checks = append(checks, Check{
					Name:    prefix + expected.Name,
					Outcome: OutcomeFail,
					Detail:  fmt.Sprintf("parameter %d (%s) type mismatch: expected %s, got %s", i+1, in.Name, expType, in.Type.Display),
				})
				break
			}
		}
		if !typesMatch {
			continue
		}

		// Verify return type
		if expected.Output != "" {
			if len(actual.Outputs) == 0 {
				checks = append(checks, Check{
					Name:    prefix + expected.Name,
					Outcome: OutcomeFail,
					Detail:  fmt.Sprintf("return type mismatch: expected %s, got void", expected.Output),
				})
				continue
			}
			outDisplay := actual.Outputs[0].Display
			outKind := actual.Outputs[0].Kind
			if !strings.EqualFold(outDisplay, expected.Output) && !strings.EqualFold(outKind, expected.Output) {
				checks = append(checks, Check{
					Name:    prefix + expected.Name,
					Outcome: OutcomeFail,
					Detail:  fmt.Sprintf("return type mismatch: expected %s, got %s", expected.Output, outDisplay),
				})
				continue
			}
		}

		checks = append(checks, Check{
			Name:    prefix + expected.Name,
			Outcome: OutcomePass,
			Detail:  "interface matches specification",
		})

		// For read-only functions, simulate and verify sanity
		if expected.ReadOnly {
			sim, err := p.Simulate(ctx, SimulateRequest{
				ContractID: contractID,
				Function:   expected.Name,
			})
			simCheckName := prefix + expected.Name + ":sanity"
			if err != nil || !sim.Success {
				errMsg := "simulation failed"
				if sim != nil && sim.Error != "" {
					errMsg = sim.Error
				} else if err != nil {
					errMsg = err.Error()
				}
				checks = append(checks, Check{
					Name:    simCheckName,
					Outcome: OutcomeFail,
					Detail:  fmt.Sprintf("read-only simulation failed: %s", errMsg),
				})
			} else {
				// Sanity assertions
				val := sim.Result
				valid := true
				reason := ""
				switch expected.Name {
				case "decimals":
					clean := strings.TrimPrefix(val, "u32:")
					clean = strings.TrimPrefix(clean, "i32:")
					d, err := strconv.ParseUint(clean, 10, 32)
					if err != nil || d > 18 {
						valid = false
						reason = fmt.Sprintf("unreasonable decimals value (%s)", val)
					}
				case "name", "symbol":
					clean := strings.TrimPrefix(val, "sym:")
					clean = strings.TrimPrefix(clean, "string:")
					clean = strings.Trim(clean, `"'`)
					if clean == "" {
						valid = false
						reason = "empty string returned"
					}
				}

				if valid {
					checks = append(checks, Check{
						Name:    simCheckName,
						Outcome: OutcomePass,
						Detail:  fmt.Sprintf("simulation returned sane value (%s)", val),
					})
				} else {
					checks = append(checks, Check{
						Name:    simCheckName,
						Outcome: OutcomeFail,
						Detail:  reason,
					})
				}
			}
		}
	}

	return checks, nil
}

package probe

import (
	"context"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// CallFrame is one node in a simulated cross-contract invocation tree,
// reconstructed from the host's fn_call, fn_return, and error diagnostic
// events.
type CallFrame struct {
	ContractID  string       `json:"contract_id"`
	Function    string       `json:"function"`
	Signature   string       `json:"signature,omitempty"`
	Args        []any        `json:"args,omitempty"`
	ReturnValue any          `json:"return_value,omitempty"`
	HasReturn   bool         `json:"-"`
	Success     bool         `json:"success"`
	Error       string       `json:"error,omitempty"`
	Calls       []*CallFrame `json:"calls,omitempty"`
}

// buildCallTree reconstructs the nested cross-contract call tree from the raw
// base64 XDR diagnostic events emitted during simulation.
func (p *Prober) buildCallTree(ctx context.Context, rawEvents []string, simError string) ([]*CallFrame, error) {
	if len(rawEvents) == 0 {
		return nil, nil
	}

	var (
		roots []*CallFrame
		stack []*CallFrame
	)

	for i, b64 := range rawEvents {
		var de xdr.DiagnosticEvent
		if err := xdr.SafeUnmarshalBase64(b64, &de); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}

		topics := de.Event.Body.V0.Topics
		if len(topics) == 0 {
			continue
		}

		head, ok := scValSymbolOrString(topics[0])
		if !ok {
			continue
		}

		switch head {
		case "fn_call":
			if len(topics) < 3 {
				continue
			}
			callee := extractCalleeContract(topics[1], de.Event.ContractId)
			fnName, _ := scValSymbolOrString(topics[2])
			args, err := p.decodeCallArgs(de.Event.Body.V0.Data)
			if err != nil {
				return nil, fmt.Errorf("event %d fn_call args: %w", i, err)
			}

			frame := &CallFrame{
				ContractID: callee,
				Function:   fnName,
				Args:       args,
				Success:    de.InSuccessfulContractCall,
			}
			p.enrichFrameABI(ctx, frame)

			if len(stack) == 0 {
				roots = append(roots, frame)
			} else {
				parent := stack[len(stack)-1]
				parent.Calls = append(parent.Calls, frame)
			}
			stack = append(stack, frame)

		case "fn_return":
			if len(stack) == 0 {
				continue
			}
			var fnName string
			if len(topics) >= 2 {
				fnName, _ = scValSymbolOrString(topics[1])
			}
			idx := len(stack) - 1
			if fnName != "" {
				for j := len(stack) - 1; j >= 0; j-- {
					if stack[j].Function == fnName {
						idx = j
						break
					}
				}
			}
			frame := stack[idx]
			stack = stack[:idx]

			retVal, err := p.codec.Decode(de.Event.Body.V0.Data)
			if err != nil {
				return nil, fmt.Errorf("event %d fn_return: %w", i, err)
			}
			frame.ReturnValue = retVal
			frame.HasReturn = true
			if frame.Error == "" && de.InSuccessfulContractCall {
				frame.Success = true
			}

		case "error":
			errText := p.formatDiagnosticError(topics[1:], de.Event.Body.V0.Data)
			if len(stack) > 0 {
				top := stack[len(stack)-1]
				top.Success = false
				if top.Error == "" {
					top.Error = errText
				}
			} else if len(roots) > 0 {
				last := roots[len(roots)-1]
				last.Success = false
				if last.Error == "" {
					last.Error = errText
				}
			}
		}
	}

	// Any frame still on the stack when the event stream ended did not return
	// normally; mark it failed and attach the simulation error if no specific
	// diagnostic error event was emitted for it.
	for i := len(stack) - 1; i >= 0; i-- {
		frame := stack[i]
		frame.Success = false
		if frame.Error == "" && simError != "" && i == len(stack)-1 {
			frame.Error = firstLine(simError)
		}
	}

	return roots, nil
}

func (p *Prober) enrichFrameABI(ctx context.Context, frame *CallFrame) {
	if p.abi == nil || frame.ContractID == "" || frame.Function == "" {
		return
	}
	iface, err := p.abi.Interface(ctx, p.network, frame.ContractID)
	if err != nil {
		return
	}
	fn, err := iface.Lookup(frame.Function)
	if err != nil {
		return
	}
	frame.Signature = fn.Signature()
}

func (p *Prober) decodeCallArgs(data xdr.ScVal) ([]any, error) {
	switch data.Type {
	case xdr.ScValTypeScvVoid:
		return nil, nil
	case xdr.ScValTypeScvVec:
		if data.Vec == nil || *data.Vec == nil {
			return nil, nil
		}
		vec := **data.Vec
		if len(vec) == 0 {
			return nil, nil
		}
		out := make([]any, 0, len(vec))
		for _, item := range vec {
			decoded, err := p.codec.Decode(item)
			if err != nil {
				return nil, err
			}
			out = append(out, decoded)
		}
		return out, nil
	default:
		decoded, err := p.codec.Decode(data)
		if err != nil {
			return nil, err
		}
		return []any{decoded}, nil
	}
}

func (p *Prober) formatDiagnosticError(topics []xdr.ScVal, data xdr.ScVal) string {
	var parts []string
	for _, t := range topics {
		if decoded, err := p.codec.Decode(t); err == nil && decoded != nil {
			parts = append(parts, fmt.Sprint(decoded))
		}
	}
	if data.Type != xdr.ScValTypeScvVoid {
		if decoded, err := p.codec.Decode(data); err == nil && decoded != nil {
			parts = append(parts, fmt.Sprint(decoded))
		}
	}
	return strings.Join(parts, ": ")
}

func extractCalleeContract(topic xdr.ScVal, fallback *xdr.ContractId) string {
	switch topic.Type {
	case xdr.ScValTypeScvBytes:
		if topic.Bytes != nil && len(*topic.Bytes) == 32 {
			if encoded, err := strkey.Encode(strkey.VersionByteContract, *topic.Bytes); err == nil {
				return encoded
			}
		}
	case xdr.ScValTypeScvAddress:
		if topic.Address != nil {
			if s, err := topic.Address.String(); err == nil {
				return s
			}
		}
	case xdr.ScValTypeScvString:
		if topic.Str != nil {
			return string(*topic.Str)
		}
	case xdr.ScValTypeScvSymbol:
		if topic.Sym != nil {
			return string(*topic.Sym)
		}
	}
	if fallback != nil {
		if encoded, err := strkey.Encode(strkey.VersionByteContract, fallback[:]); err == nil {
			return encoded
		}
	}
	return ""
}

func scValSymbolOrString(v xdr.ScVal) (string, bool) {
	switch v.Type {
	case xdr.ScValTypeScvSymbol:
		if v.Sym != nil {
			return string(*v.Sym), true
		}
	case xdr.ScValTypeScvString:
		if v.Str != nil {
			return string(*v.Str), true
		}
	}
	return "", false
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}

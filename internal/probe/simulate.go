package probe

import (
	"context"
	"fmt"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// SimulateRequest describes an invocation to simulate.
type SimulateRequest struct {
	// ContractID is the strkey contract address ("C...").
	ContractID string `json:"contract_id"`
	// Function is the contract function to call.
	Function string `json:"function"`
	// Args are argument specifications in the scval package's "type:value" form.
	Args []string `json:"args,omitempty"`
}

// SimulateResult is the outcome of a simulated invocation.
type SimulateResult struct {
	ContractID string   `json:"contract_id"`
	Function   string   `json:"function"`
	Args       []string `json:"args,omitempty"`

	// Success is true when the call would succeed.
	Success bool `json:"success"`
	// Error carries the simulation failure message when Success is false.
	Error string `json:"error,omitempty"`

	// ReturnValue is the decoded return value, JSON-marshalable.
	ReturnValue any `json:"return_value"`
	// ReturnValueXDR is the raw base64 ScVal, for exact round-tripping.
	ReturnValueXDR string `json:"return_value_xdr,omitempty"`

	Cost      Cost      `json:"cost"`
	Footprint Footprint `json:"footprint"`

	// RestoreRequired is set when archived entries block the call.
	RestoreRequired *RestoreInfo `json:"restore_required,omitempty"`

	// Events are decoded diagnostic events from the simulation.
	Events []Event `json:"events,omitempty"`

	// LatestLedger is the ledger the simulation ran against.
	LatestLedger uint32 `json:"latest_ledger"`
}

// Simulate builds an invocation, simulates it, and reports the decoded result
// alongside its resource cost.
//
// A simulation that the network rejects is not a Go error: it is a result with
// Success false and Error set. Go errors are reserved for problems reaching the
// network or encoding the request.
func (p *Prober) Simulate(ctx context.Context, req SimulateRequest) (*SimulateResult, error) {
	contract, err := stellar.ParseContractID(req.ContractID)
	if err != nil {
		return nil, err
	}
	if req.Function == "" {
		return nil, fmt.Errorf("a function name is required")
	}

	args, err := p.codec.EncodeAll(req.Args)
	if err != nil {
		return nil, err
	}

	envelope, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: p.source,
		Contract:      contract,
		Function:      req.Function,
		Args:          args,
	})
	if err != nil {
		return nil, err
	}

	resp, err := p.client.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{
		Transaction: envelope,
	})
	if err != nil {
		return nil, err
	}

	return p.buildSimulateResult(req, resp)
}

func (p *Prober) buildSimulateResult(req SimulateRequest, resp protocol.SimulateTransactionResponse) (*SimulateResult, error) {
	result := &SimulateResult{
		ContractID:   req.ContractID,
		Function:     req.Function,
		Args:         req.Args,
		Success:      resp.Error == "",
		Error:        resp.Error,
		LatestLedger: resp.LatestLedger,
		Cost:         Cost{MinResourceFee: resp.MinResourceFee},
	}

	if resp.RestorePreamble != nil {
		result.RestoreRequired = &RestoreInfo{
			MinResourceFee:     resp.RestorePreamble.MinResourceFee,
			TransactionDataXDR: resp.RestorePreamble.TransactionDataXDR,
		}
	}

	if resp.TransactionDataXDR != "" {
		var data xdr.SorobanTransactionData
		if err := xdr.SafeUnmarshalBase64(resp.TransactionDataXDR, &data); err != nil {
			return nil, fmt.Errorf("decode transaction data: %w", err)
		}
		result.Cost.Instructions = uint32(data.Resources.Instructions)
		result.Cost.DiskReadBytes = uint32(data.Resources.DiskReadBytes)
		result.Cost.WriteBytes = uint32(data.Resources.WriteBytes)
		result.Cost.ResourceFee = int64(data.ResourceFee)
		result.Footprint = describeFootprint(data.Resources.Footprint)
	}

	if len(resp.Results) > 0 && resp.Results[0].ReturnValueXDR != nil {
		raw := *resp.Results[0].ReturnValueXDR
		result.ReturnValueXDR = raw

		var val xdr.ScVal
		if err := xdr.SafeUnmarshalBase64(raw, &val); err != nil {
			return nil, fmt.Errorf("decode return value: %w", err)
		}
		decoded, err := p.codec.Decode(val)
		if err != nil {
			return nil, fmt.Errorf("decode return value: %w", err)
		}
		result.ReturnValue = decoded
	}

	events, err := p.decodeEvents(resp.EventsXDR)
	if err != nil {
		// Diagnostic events are advisory. Losing them must not mask an
		// otherwise usable simulation result.
		p.log.Debug("could not decode diagnostic events", "err", err)
	} else {
		result.Events = events
	}

	return result, nil
}

func (p *Prober) decodeEvents(raw []string) ([]Event, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	events := make([]Event, 0, len(raw))

	for i, b64 := range raw {
		var de xdr.DiagnosticEvent
		if err := xdr.SafeUnmarshalBase64(b64, &de); err != nil {
			return nil, fmt.Errorf("event %d: %w", i, err)
		}

		event := Event{
			Type:                     de.Event.Type.String(),
			InSuccessfulContractCall: de.InSuccessfulContractCall,
		}
		if de.Event.ContractId != nil {
			addr := xdr.ScAddress{
				Type:       xdr.ScAddressTypeScAddressTypeContract,
				ContractId: de.Event.ContractId,
			}
			if s, err := addr.String(); err == nil {
				event.ContractID = s
			}
		}

		for _, topic := range de.Event.Body.V0.Topics {
			decoded, err := p.codec.Decode(topic)
			if err != nil {
				return nil, fmt.Errorf("event %d topic: %w", i, err)
			}
			event.Topics = append(event.Topics, decoded)
		}
		data, err := p.codec.Decode(de.Event.Body.V0.Data)
		if err != nil {
			return nil, fmt.Errorf("event %d data: %w", i, err)
		}
		event.Data = data

		events = append(events, event)
	}
	return events, nil
}

func describeFootprint(fp xdr.LedgerFootprint) Footprint {
	out := Footprint{ReadOnly: []string{}, ReadWrite: []string{}}
	for _, key := range fp.ReadOnly {
		out.ReadOnly = append(out.ReadOnly, describeLedgerKey(key))
	}
	for _, key := range fp.ReadWrite {
		out.ReadWrite = append(out.ReadWrite, describeLedgerKey(key))
	}
	return out
}

// describeLedgerKey renders a ledger key as a short human-readable label.
func describeLedgerKey(key xdr.LedgerKey) string {
	switch key.Type {
	case xdr.LedgerEntryTypeContractData:
		cd := key.ContractData
		addr, err := cd.Contract.String()
		if err != nil {
			addr = "<unknown contract>"
		}
		kind := "data"
		if cd.Key.Type == xdr.ScValTypeScvLedgerKeyContractInstance {
			kind = "instance"
		}
		return fmt.Sprintf("contract %s %s (%s)", kind, addr, durabilityName(cd.Durability))

	case xdr.LedgerEntryTypeContractCode:
		return fmt.Sprintf("contract code %x", key.ContractCode.Hash[:8])

	case xdr.LedgerEntryTypeAccount:
		return fmt.Sprintf("account %s", key.Account.AccountId.Address())

	case xdr.LedgerEntryTypeTrustline:
		return "trustline"

	case xdr.LedgerEntryTypeTtl:
		return "ttl entry"

	default:
		return key.Type.String()
	}
}

func durabilityName(d xdr.ContractDataDurability) string {
	if d == xdr.ContractDataDurabilityTemporary {
		return "temporary"
	}
	return "persistent"
}

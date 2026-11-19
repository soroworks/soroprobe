package probe

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/scval"
)

// ErrTxNotFound reports a hash the RPC has no record of: a transaction that
// never landed, or one older than the RPC's retention window.
var ErrTxNotFound = errors.New("transaction not found")

// TxResources are the resources a transaction declared and the fees it was
// charged.
type TxResources struct {
	// Instructions, DiskReadBytes and WriteBytes are the limits the
	// transaction declared, which bound what it used.
	Instructions  uint32 `json:"instructions"`
	DiskReadBytes uint32 `json:"disk_read_bytes"`
	WriteBytes    uint32 `json:"write_bytes"`
	// ReadOnlyEntries and ReadWriteEntries count its footprint.
	ReadOnlyEntries  int `json:"read_only_entries"`
	ReadWriteEntries int `json:"read_write_entries"`
	// ResourceFee is the resource fee it offered, in stroops.
	ResourceFee int64 `json:"resource_fee_stroops"`
	// ResourceFeeCharged is the resource fee it was charged, when the meta
	// reports it.
	ResourceFeeCharged *int64 `json:"resource_fee_charged_stroops,omitempty"`
	// FeeCharged is the total fee charged, in stroops.
	FeeCharged int64 `json:"fee_charged_stroops"`
}

// TxFailure explains why a transaction failed.
type TxFailure struct {
	// Result is the transaction result code, such as "txFailed".
	Result string `json:"result"`
	// Operation is the invokeHostFunction result code, such as "trapped".
	Operation string `json:"operation,omitempty"`
	// Error is the host or contract error the diagnostic events report,
	// such as "Error(Contract, #3)", with the host's message.
	Error string `json:"error,omitempty"`
}

// TxReport explains a submitted transaction.
type TxReport struct {
	Hash   string `json:"hash"`
	Status string `json:"status"`
	// Ledger and ClosedAt say when it was applied.
	Ledger   uint32    `json:"ledger"`
	ClosedAt time.Time `json:"closed_at"`
	Source   string    `json:"source"`
	FeeBump  bool      `json:"fee_bump"`

	// ContractID, Function and Args describe a contract invocation;
	// HostFunction names any other host function (an upload or a create).
	ContractID   string `json:"contract_id,omitempty"`
	Function     string `json:"function,omitempty"`
	Args         []any  `json:"args,omitempty"`
	Signature    string `json:"signature,omitempty"`
	HostFunction string `json:"host_function"`

	// ReturnValue is the decoded return value of a successful call.
	ReturnValue any `json:"return_value,omitempty"`
	// Events are the contract events the transaction emitted.
	Events    []Event      `json:"events,omitempty"`
	Resources *TxResources `json:"resources,omitempty"`
	Failure   *TxFailure   `json:"failure,omitempty"`
}

// Tx fetches a submitted transaction through getTransaction and decodes
// its invocation, result, resource usage and contract events.
func (p *Prober) Tx(ctx context.Context, hash string) (*TxReport, error) {
	hash = strings.ToLower(strings.TrimSpace(hash))
	if len(hash) != 64 {
		return nil, fmt.Errorf("%q is not a transaction hash: want 64 hex characters", hash)
	}
	tx, err := p.client.GetTransaction(ctx, protocol.GetTransactionRequest{Hash: hash})
	if err != nil {
		return nil, err
	}
	if tx.Status == protocol.TransactionStatusNotFound || tx.EnvelopeXDR == "" {
		return nil, fmt.Errorf("%w: %s; the RPC keeps ledgers %d to %d, so a transaction older than that must be looked up in an archive such as Hubble",
			ErrTxNotFound, hash, tx.OldestLedger, tx.LatestLedger)
	}

	r := &TxReport{Hash: hash, Status: tx.Status, Ledger: tx.Ledger, FeeBump: tx.FeeBump}
	if tx.LedgerCloseTime > 0 {
		r.ClosedAt = time.Unix(tx.LedgerCloseTime, 0).UTC()
	}

	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(tx.EnvelopeXDR, &env); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	r.Source = env.SourceAccount().ToAccountId().Address()
	p.describeInvocation(ctx, env, r)
	if data, ok := txSorobanData(env); ok {
		r.Resources = &TxResources{
			Instructions:     uint32(data.Resources.Instructions),
			DiskReadBytes:    uint32(data.Resources.DiskReadBytes),
			WriteBytes:       uint32(data.Resources.WriteBytes),
			ReadOnlyEntries:  len(data.Resources.Footprint.ReadOnly),
			ReadWriteEntries: len(data.Resources.Footprint.ReadWrite),
			ResourceFee:      int64(data.ResourceFee),
		}
	}

	var result xdr.TransactionResult
	if err := xdr.SafeUnmarshalBase64(tx.ResultXDR, &result); err != nil {
		return nil, fmt.Errorf("decode result: %w", err)
	}
	if r.Resources != nil {
		r.Resources.FeeCharged = int64(result.FeeCharged)
	}
	if tx.Status != protocol.TransactionStatusSuccess {
		r.Failure = txFailure(result, tx.DiagnosticEventsXDR)
	}

	if tx.ResultMetaXDR != "" {
		var meta xdr.TransactionMeta
		if err := xdr.SafeUnmarshalBase64(tx.ResultMetaXDR, &meta); err != nil {
			return nil, fmt.Errorf("decode meta: %w", err)
		}
		p.readMeta(meta, r)
	}
	return r, nil
}

func (p *Prober) describeInvocation(ctx context.Context, env xdr.TransactionEnvelope, r *TxReport) {
	ops := env.Operations()
	if len(ops) != 1 {
		r.HostFunction = fmt.Sprintf("%d operations; not a Soroban transaction", len(ops))
		return
	}
	invoke, ok := ops[0].Body.GetInvokeHostFunctionOp()
	if !ok {
		r.HostFunction = ops[0].Body.Type.String()
		return
	}
	switch invoke.HostFunction.Type {
	case xdr.HostFunctionTypeHostFunctionTypeInvokeContract:
		r.HostFunction = "invoke_contract"
		args := invoke.HostFunction.MustInvokeContract()
		if id, err := args.ContractAddress.String(); err == nil {
			r.ContractID = id
		}
		r.Function = string(args.FunctionName)
		r.Args = make([]any, 0, len(args.Args))
		for _, a := range args.Args {
			r.Args = append(r.Args, txDecode(a))
		}
		if r.ContractID != "" {
			if iface, err := p.chain.Interface(ctx, p.network, r.ContractID); err == nil {
				if fn, err := iface.Lookup(r.Function); err == nil {
					r.Signature = fn.Signature()
				}
			}
		}
	case xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm:
		r.HostFunction = "upload_contract_wasm"
	default:
		r.HostFunction = "create_contract"
	}
}

// txSorobanData returns the resources a transaction declared, unwrapping a
// fee bump.
func txSorobanData(env xdr.TransactionEnvelope) (xdr.SorobanTransactionData, bool) {
	var ext xdr.TransactionExt
	switch {
	case env.V1 != nil:
		ext = env.V1.Tx.Ext
	case env.FeeBump != nil && env.FeeBump.Tx.InnerTx.V1 != nil:
		ext = env.FeeBump.Tx.InnerTx.V1.Tx.Ext
	default:
		return xdr.SorobanTransactionData{}, false
	}
	if ext.SorobanData == nil {
		return xdr.SorobanTransactionData{}, false
	}
	return *ext.SorobanData, true
}

// readMeta takes the return value, contract events and charged resource
// fee from the transaction meta, in either the v3 or the v4 layout.
func (p *Prober) readMeta(meta xdr.TransactionMeta, r *TxReport) {
	var events []xdr.ContractEvent
	var ext *xdr.SorobanTransactionMetaExt
	switch {
	case meta.V4 != nil:
		for _, op := range meta.V4.Operations {
			events = append(events, op.Events...)
		}
		if sm := meta.V4.SorobanMeta; sm != nil {
			ext = &sm.Ext
			if sm.ReturnValue != nil && r.Status == protocol.TransactionStatusSuccess {
				r.ReturnValue = txDecode(*sm.ReturnValue)
			}
		}
	case meta.V3 != nil && meta.V3.SorobanMeta != nil:
		sm := meta.V3.SorobanMeta
		events = sm.Events
		ext = &sm.Ext
		if r.Status == protocol.TransactionStatusSuccess {
			r.ReturnValue = txDecode(sm.ReturnValue)
		}
	}
	for _, ev := range events {
		r.Events = append(r.Events, txEvent(ev))
	}
	if ext != nil && ext.V1 != nil && r.Resources != nil {
		charged := int64(ext.V1.TotalNonRefundableResourceFeeCharged + ext.V1.TotalRefundableResourceFeeCharged)
		r.Resources.ResourceFeeCharged = &charged
	}
}

func txEvent(ev xdr.ContractEvent) Event {
	out := Event{Type: strings.TrimPrefix(strings.ToLower(ev.Type.String()), "contracteventtype"), InSuccessfulContractCall: true}
	if ev.ContractId != nil {
		if id, err := (xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: ev.ContractId}).String(); err == nil {
			out.ContractID = id
		}
	}
	if body, ok := ev.Body.GetV0(); ok {
		for _, t := range body.Topics {
			out.Topics = append(out.Topics, txDecode(t))
		}
		out.Data = txDecode(body.Data)
	}
	return out
}

func txDecode(v xdr.ScVal) any {
	decoded, err := scval.Decode(v)
	if err != nil {
		return v.Type.String()
	}
	return decoded
}

// txFailure reads the failure codes from the result and the error the
// diagnostic events report.
func txFailure(result xdr.TransactionResult, diagnostics []string) *TxFailure {
	f := &TxFailure{}
	res := result.Result
	if inner, ok := res.GetInnerResultPair(); ok {
		f.Result = strings.TrimPrefix(inner.Result.Result.Code.String(), "TransactionResultCode")
		if ops, ok := inner.Result.Result.GetResults(); ok && len(ops) == 1 {
			f.Operation = txOperationCode(ops[0])
		}
	} else {
		f.Result = strings.TrimPrefix(res.Code.String(), "TransactionResultCode")
		if ops, ok := res.GetResults(); ok && len(ops) == 1 {
			f.Operation = txOperationCode(ops[0])
		}
	}
	for _, b64 := range diagnostics {
		var ev xdr.DiagnosticEvent
		if err := xdr.SafeUnmarshalBase64(b64, &ev); err != nil {
			continue
		}
		body, ok := ev.Event.Body.GetV0()
		if !ok || len(body.Topics) < 2 {
			continue
		}
		if s, ok := body.Topics[0].GetSym(); !ok || s != "error" {
			continue
		}
		if e, ok := body.Topics[1].GetError(); ok {
			f.Error = txScError(e)
			if msg, ok := txDecode(body.Data).(string); ok && msg != "" {
				f.Error += ": " + msg
			} else if items, ok := txDecode(body.Data).([]any); ok && len(items) > 0 {
				if msg, ok := items[0].(string); ok {
					f.Error += ": " + msg
				}
			}
			break
		}
	}
	return f
}

func txOperationCode(op xdr.OperationResult) string {
	tr, ok := op.GetTr()
	if !ok {
		return op.Code.String()
	}
	if ih, ok := tr.GetInvokeHostFunctionResult(); ok {
		return strings.TrimPrefix(ih.Code.String(), "InvokeHostFunctionResultCodeInvokeHostFunction")
	}
	return tr.Type.String()
}

// txScError writes an ScError the way the host prints it.
func txScError(e xdr.ScError) string {
	typ := strings.TrimPrefix(e.Type.String(), "ScErrorTypeSce")
	if e.Type == xdr.ScErrorTypeSceContract && e.ContractCode != nil {
		return fmt.Sprintf("Error(%s, #%d)", typ, *e.ContractCode)
	}
	code := ""
	if e.Code != nil {
		code = strings.TrimPrefix(e.Code.String(), "ScErrorCodeScec")
	}
	return fmt.Sprintf("Error(%s, %s)", typ, code)
}

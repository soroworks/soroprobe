package probe

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// ErrTransactionNotFound reports a hash the RPC has no record of: a
// transaction that never landed, or one older than the RPC's retention window.
var ErrTransactionNotFound = errors.New("transaction not found")

// Where a calibration's "actual" figure comes from.
const (
	// SourceConsumed is a figure the network measured while running the
	// transaction, read from its core_metrics diagnostic events or its fee
	// accounting.
	SourceConsumed = "consumed"
	// SourceDeclared is the limit the transaction declared, which bounds
	// what it consumed. The network only reports what was actually used
	// when diagnostic events are enabled.
	SourceDeclared = "declared"
)

// ResourceDelta compares one resource between a fresh simulation and the
// transaction that really ran.
type ResourceDelta struct {
	// Name is the resource, such as "cpu instructions".
	Name string `json:"name"`
	// Estimated is what simulating the invocation now reports.
	Estimated int64 `json:"estimated"`
	// Actual is what the transaction used, or declared; see Source.
	Actual int64 `json:"actual"`
	// Source is SourceConsumed or SourceDeclared.
	Source string `json:"source"`
	// Delta is Estimated minus Actual: positive means the simulation
	// over-estimates.
	Delta int64 `json:"delta"`
	// Percent is Delta as a percentage of Actual. It is nil when Actual is
	// zero, where a percentage has no meaning.
	Percent *float64 `json:"percent,omitempty"`
}

// CalibrateResult compares a submitted transaction's resource usage with a
// fresh simulation of the same invocation.
type CalibrateResult struct {
	Hash string `json:"hash"`
	// Ledger is the ledger the transaction was included in.
	Ledger uint32 `json:"ledger"`
	// Status is the transaction's status: SUCCESS or FAILED.
	Status string `json:"status"`
	// ContractID and Function name the invocation, when it called a
	// contract function.
	ContractID string `json:"contract_id,omitempty"`
	Function   string `json:"function,omitempty"`

	// SimulationSuccess is false when the invocation would fail if run
	// now, in which case the estimates are not comparable.
	SimulationSuccess bool   `json:"simulation_success"`
	SimulationError   string `json:"simulation_error,omitempty"`
	// SimulationErrorExplanation names the error in SimulationError.
	SimulationErrorExplanation *ErrorExplanation `json:"simulation_error_explanation,omitempty"`

	// Resources compares each resource, in a stable order.
	Resources []ResourceDelta `json:"resources"`

	// StateChanged is true when a ledger entry the transaction touched has
	// been modified, created or deleted since it ran, so the simulation
	// ran against different state and the comparison is approximate.
	StateChanged bool `json:"state_changed"`
	// ChangedEntries lists the touched entries that changed.
	ChangedEntries []string `json:"changed_entries,omitempty"`
	// Notes explain anything that makes the comparison approximate.
	Notes []string `json:"notes,omitempty"`

	// LatestLedger is the ledger the simulation ran against.
	LatestLedger uint32 `json:"latest_ledger"`
}

// Calibrate re-simulates the invocation in a submitted transaction and
// compares the estimate with what the transaction actually consumed, so a
// fee strategy can be given the margin it really needs. Nothing is
// submitted.
func (p *Prober) Calibrate(ctx context.Context, hash string) (*CalibrateResult, error) {
	hash = strings.TrimSpace(hash)
	if hash == "" {
		return nil, errors.New("a transaction hash is required")
	}
	tx, err := p.client.GetTransaction(ctx, protocol.GetTransactionRequest{Hash: hash})
	if err != nil {
		return nil, err
	}
	if tx.Status == protocol.TransactionStatusNotFound || tx.EnvelopeXDR == "" {
		return nil, fmt.Errorf("%w: %s; the RPC keeps ledgers %d to %d, so an older transaction cannot be fetched from it",
			ErrTransactionNotFound, hash, tx.OldestLedger, tx.LatestLedger)
	}

	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(tx.EnvelopeXDR, &env); err != nil {
		return nil, fmt.Errorf("decode transaction envelope: %w", err)
	}
	ops := env.Operations()
	if len(ops) != 1 {
		return nil, fmt.Errorf("transaction %s has %d operations; only a single Soroban invocation can be calibrated", hash, len(ops))
	}
	invoke, ok := ops[0].Body.GetInvokeHostFunctionOp()
	if !ok {
		return nil, fmt.Errorf("transaction %s is not a Soroban invocation", hash)
	}
	declared, ok := sorobanData(env)
	if !ok {
		return nil, fmt.Errorf("transaction %s carries no Soroban resources to compare", hash)
	}

	out := &CalibrateResult{Hash: hash, Ledger: tx.Ledger, Status: tx.Status}
	if args, ok := invoke.HostFunction.GetInvokeContract(); ok {
		out.Function = string(args.FunctionName)
		if id, err := args.ContractAddress.String(); err == nil {
			out.ContractID = id
		}
	}

	source := env.SourceAccount().ToAccountId()
	if ops[0].SourceAccount != nil {
		source = ops[0].SourceAccount.ToAccountId()
	}
	envelope, err := stellar.BuildHostFunctionTx(source.Address(), invoke.HostFunction)
	if err != nil {
		return nil, err
	}
	sim, err := p.client.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{Transaction: envelope})
	if err != nil {
		return nil, err
	}
	estimate, err := p.buildSimulateResult(SimulateRequest{ContractID: out.ContractID, Function: out.Function}, sim)
	if err != nil {
		return nil, err
	}
	out.LatestLedger = estimate.LatestLedger
	out.SimulationSuccess = estimate.Success
	out.SimulationError = estimate.Error
	if !estimate.Success {
		if out.ContractID != "" {
			out.SimulationErrorExplanation = p.explainFailure(ctx, out.ContractID, estimate.Error)
		} else {
			out.SimulationErrorExplanation = ParseError(estimate.Error)
		}
		out.Notes = append(out.Notes, "the invocation fails when simulated now, so its estimates are not comparable")
	}

	metrics, charged, chargedOK := p.consumption(tx)
	out.Resources = compareResources(estimate.Cost, declared, metrics, charged, chargedOK)
	if len(metrics) == 0 {
		out.Notes = append(out.Notes, "the transaction carries no core_metrics diagnostic events, so cpu instructions "+
			"and bytes are compared with what it declared, which bounds what it consumed")
	}
	if tx.Status != protocol.TransactionStatusSuccess {
		out.Notes = append(out.Notes, "the transaction failed, so it may have stopped before using all it would have")
	}

	if err := p.checkStateChanged(ctx, declared.Resources.Footprint, tx.Ledger, out); err != nil {
		return nil, err
	}
	return out, nil
}

// sorobanData returns the resources a transaction declared, unwrapping a
// fee bump.
func sorobanData(env xdr.TransactionEnvelope) (xdr.SorobanTransactionData, bool) {
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

// consumption reads what the transaction really used: the core_metrics
// diagnostic events the network emits when diagnostics are on, and the
// resource fee it was charged.
func (p *Prober) consumption(tx protocol.GetTransactionResponse) (map[string]int64, int64, bool) {
	var events []xdr.DiagnosticEvent
	for _, raw := range tx.DiagnosticEventsXDR {
		var ev xdr.DiagnosticEvent
		if err := xdr.SafeUnmarshalBase64(raw, &ev); err == nil {
			events = append(events, ev)
		}
	}

	var charged int64
	var chargedOK bool
	var meta xdr.TransactionMeta
	if tx.ResultMetaXDR != "" {
		if err := xdr.SafeUnmarshalBase64(tx.ResultMetaXDR, &meta); err != nil {
			p.log.Debug("could not decode transaction meta", "err", err)
		} else {
			var ext *xdr.SorobanTransactionMetaExt
			switch {
			case meta.V3 != nil && meta.V3.SorobanMeta != nil:
				ext = &meta.V3.SorobanMeta.Ext
				if len(events) == 0 {
					events = meta.V3.SorobanMeta.DiagnosticEvents
				}
			case meta.V4 != nil:
				if meta.V4.SorobanMeta != nil {
					ext = &meta.V4.SorobanMeta.Ext
				}
				if len(events) == 0 {
					events = meta.V4.DiagnosticEvents
				}
			}
			if ext != nil && ext.V1 != nil {
				charged = int64(ext.V1.TotalNonRefundableResourceFeeCharged + ext.V1.TotalRefundableResourceFeeCharged)
				chargedOK = true
			}
		}
	}
	return coreMetrics(events), charged, chargedOK
}

// coreMetrics collects the network's own resource measurements: diagnostic
// events with topics ["core_metrics", <name>] and a u64 value.
func coreMetrics(events []xdr.DiagnosticEvent) map[string]int64 {
	out := map[string]int64{}
	for _, ev := range events {
		body, ok := ev.Event.Body.GetV0()
		if !ok || len(body.Topics) != 2 {
			continue
		}
		first, ok := body.Topics[0].GetSym()
		if !ok || first != "core_metrics" {
			continue
		}
		name, ok := body.Topics[1].GetSym()
		if !ok {
			continue
		}
		if v, ok := body.Data.GetU64(); ok && uint64(v) <= math.MaxInt64 {
			out[string(name)] = int64(v) //nolint:gosec // checked against MaxInt64 above
		}
	}
	return out
}

// compareResources lines the estimate up against the actual figures.
func compareResources(estimate Cost, declared xdr.SorobanTransactionData, metrics map[string]int64, charged int64, chargedOK bool) []ResourceDelta {
	actual := func(metric string, fallback int64) (int64, string) {
		if v, ok := metrics[metric]; ok {
			return v, SourceConsumed
		}
		return fallback, SourceDeclared
	}
	res := declared.Resources
	cpu, cpuSrc := actual("cpu_insn", int64(res.Instructions))
	read, readSrc := actual("ledger_read_byte", int64(res.DiskReadBytes))
	write, writeSrc := actual("ledger_write_byte", int64(res.WriteBytes))
	fee, feeSrc := int64(declared.ResourceFee), SourceDeclared
	if chargedOK {
		fee, feeSrc = charged, SourceConsumed
	}

	return []ResourceDelta{
		delta("cpu instructions", int64(estimate.Instructions), cpu, cpuSrc),
		delta("disk read bytes", int64(estimate.DiskReadBytes), read, readSrc),
		delta("write bytes", int64(estimate.WriteBytes), write, writeSrc),
		delta("resource fee (stroops)", estimate.ResourceFee, fee, feeSrc),
	}
}

func delta(name string, estimated, actual int64, source string) ResourceDelta {
	d := ResourceDelta{Name: name, Estimated: estimated, Actual: actual, Source: source, Delta: estimated - actual}
	if actual != 0 {
		pct := float64(d.Delta) / float64(actual) * 100
		d.Percent = &pct
	}
	return d
}

// checkStateChanged reads the entries the transaction touched and reports
// any modified after its ledger, or no longer there, since the simulation
// then ran against different state.
func (p *Prober) checkStateChanged(ctx context.Context, fp xdr.LedgerFootprint, ledger uint32, out *CalibrateResult) error {
	keys := append(append([]xdr.LedgerKey{}, fp.ReadOnly...), fp.ReadWrite...)
	if len(keys) == 0 {
		return nil
	}
	encoded := make([]string, 0, len(keys))
	labels := make(map[string]string, len(keys))
	for _, key := range keys {
		b64, err := stellar.EncodeKey(key)
		if err != nil {
			return err
		}
		encoded = append(encoded, b64)
		labels[b64] = describeLedgerKey(key)
	}
	resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: encoded})
	if err != nil {
		return err
	}
	found := make(map[string]bool, len(resp.Entries))
	for _, entry := range resp.Entries {
		found[entry.KeyXDR] = true
		// The transaction's own writes land in its ledger, so only a later
		// modification is a change since it ran.
		if entry.LastModifiedLedger > ledger {
			out.ChangedEntries = append(out.ChangedEntries, labels[entry.KeyXDR]+" (modified)")
		}
	}
	for _, b64 := range encoded {
		if !found[b64] {
			out.ChangedEntries = append(out.ChangedEntries, labels[b64]+" (no longer live)")
		}
	}
	if len(out.ChangedEntries) > 0 {
		out.StateChanged = true
		out.Notes = append(out.Notes, fmt.Sprintf("%d ledger entr%s the transaction touched changed since it ran, so the comparison is approximate",
			len(out.ChangedEntries), plural(len(out.ChangedEntries), "y", "ies")))
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

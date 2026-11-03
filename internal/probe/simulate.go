package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar"
)

// SimulateRequest describes an invocation to simulate.
type SimulateRequest struct {
	// ContractID is the strkey contract address ("C...").
	ContractID string `json:"contract_id,omitempty"`
	// Function is the contract function to call.
	Function string `json:"function,omitempty"`
	// Args are argument specifications in the scval package's "type:value" form.
	Args []string `json:"args,omitempty"`
	// Envelope, when set, is a prebuilt base64-encoded Stellar
	// TransactionEnvelope XDR to simulate as given instead of building a
	// fresh invocation transaction from ContractID, Function and Args.
	Envelope string `json:"envelope,omitempty"`
	// Baseline, when set, is a saved simulation report whose cost this
	// simulation is compared against. It must name the same contract and
	// function; see ErrBaselineMismatch.
	Baseline *Baseline `json:"baseline,omitempty"`
	// MaxIncreasePercent, when set, is the largest percentage increase in
	// any cost figure that is tolerated. It only has an effect alongside a
	// Baseline.
	MaxIncreasePercent *float64 `json:"max_increase_percent,omitempty"`
	// Repeat, when greater than one, simulates the call that many times and
	// reports the spread between the runs. It is how a noisy estimate is
	// told apart from a stable one.
	Repeat int `json:"repeat,omitempty"`
	// MaxSpreadPercent, when set alongside Repeat, is the largest spread in
	// any cost figure, as a percentage of its minimum, that is tolerated
	// before the call is reported as noisy. A nil value reports the spread
	// without judging it.
	MaxSpreadPercent *float64 `json:"max_spread_percent,omitempty"`
}

// ErrInvalidEnvelope is returned when a prebuilt transaction envelope is
// malformed, contains multiple operations, or does not invoke a Soroban
// contract function.
var ErrInvalidEnvelope = errors.New("invalid transaction envelope")

// ErrBaselineMismatch is returned when a baseline names a different contract
// or function than the call being simulated. Its costs say nothing about a
// different call, so comparing them would report a change that never
// happened.
var ErrBaselineMismatch = errors.New("the baseline does not match the call")

// Baseline is a saved simulation report used as a cost reference.
//
// It has the shape a `simulate --json` report has, so a report saved from an
// earlier run can be fed back in unchanged.
type Baseline struct {
	// ContractID is the contract the baseline was taken against.
	ContractID string `json:"contract_id"`
	// Function is the function the baseline was taken against.
	Function string `json:"function"`
	// Cost is the cost the baseline recorded.
	Cost Cost `json:"cost"`
}

// CostChange is the change in one cost figure between a baseline and a
// simulation.
type CostChange struct {
	// Name is the figure's label, such as "cpu instructions".
	Name string `json:"name"`
	// Baseline is the figure's value in the saved report.
	Baseline int64 `json:"baseline"`
	// Current is the figure's value in this simulation.
	Current int64 `json:"current"`
	// Delta is Current minus Baseline.
	Delta int64 `json:"delta"`
	// Percent is the percentage change. It is nil when the baseline was
	// zero, because a percentage change from zero has no meaning.
	Percent *float64 `json:"percent,omitempty"`
}

// CostComparison reports how a simulation's cost differs from a baseline.
type CostComparison struct {
	// Figures are the per-figure changes, in a stable order.
	Figures []CostChange `json:"figures"`
	// MaxIncreasePercent is the tolerated percentage increase, when a
	// threshold was given.
	MaxIncreasePercent *float64 `json:"max_increase_percent,omitempty"`
	// Exceeded is true when a figure grew by more than the tolerance.
	Exceeded bool `json:"exceeded"`
}

// costFigures lists the figures a comparison looks at, in a stable order.
//
// The minimum resource fee is deliberately left out: it is the network's
// recommendation rather than a measurement, and it moves with the resource
// fee anyway.
func costFigures(c Cost) []CostFigure {
	return []CostFigure{
		{Name: "cpu instructions", Value: int64(c.Instructions)},
		{Name: "disk read bytes", Value: int64(c.DiskReadBytes)},
		{Name: "write bytes", Value: int64(c.WriteBytes)},
		{Name: "resource fee (stroops)", Value: c.ResourceFee},
	}
}

// CostFigure is one measurable figure of a simulation's cost.
type CostFigure struct {
	// Name is the figure's label.
	Name string
	// Value is the figure's value.
	Value int64
}

// CompareCost compares a simulation's cost against a saved baseline.
//
// The comparison is a pure function of the two costs and the threshold, so it
// can be exercised without a network or a prober.
//
// A figure that grew is compared in percentage terms. A baseline of zero has
// no percentage to compare against, so any growth from it counts as an
// increase without limit: from nothing to something is not a small change.
func CompareCost(baseline, current Cost, maxIncreasePercent *float64) CostComparison {
	base := costFigures(baseline)
	cur := costFigures(current)

	out := CostComparison{
		Figures:            make([]CostChange, 0, len(base)),
		MaxIncreasePercent: maxIncreasePercent,
	}

	for i := range base {
		change := CostChange{
			Name:     base[i].Name,
			Baseline: base[i].Value,
			Current:  cur[i].Value,
		}
		change.Delta = change.Current - change.Baseline
		if change.Baseline != 0 {
			percent := float64(change.Delta) / float64(change.Baseline) * 100
			change.Percent = &percent
		}
		out.Figures = append(out.Figures, change)
	}

	if maxIncreasePercent == nil {
		return out
	}

	threshold := *maxIncreasePercent
	for _, f := range out.Figures {
		if f.Delta <= 0 {
			continue
		}
		if f.Percent == nil {
			out.Exceeded = true
			continue
		}
		if *f.Percent > threshold {
			out.Exceeded = true
		}
	}
	return out
}

// NamedSimulation is one named invocation in a simulation batch.
type NamedSimulation struct {
	Name string `json:"name,omitempty"`
	SimulateRequest
}

// SimulateBatchRequest is a list of invocations to simulate sequentially.
type SimulateBatchRequest struct {
	Calls []NamedSimulation `json:"calls"`
}

// SimulateBatchItem is the outcome of one simulation in a batch.
type SimulateBatchItem struct {
	Name   string          `json:"name"`
	Result *SimulateResult `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

// SimulateBatchResult aggregates a batch of simulations.
type SimulateBatchResult struct {
	Items []SimulateBatchItem `json:"items"`
}

// SimulateBatch runs each invocation sequentially and records individual
// errors so a bad call does not prevent the remaining calls from running.
func (p *Prober) SimulateBatch(ctx context.Context, req SimulateBatchRequest) (*SimulateBatchResult, error) {
	if len(req.Calls) == 0 {
		return nil, fmt.Errorf("a simulation batch must contain at least one call")
	}

	result := &SimulateBatchResult{Items: make([]SimulateBatchItem, 0, len(req.Calls))}
	for _, call := range req.Calls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		name := call.Name
		if name == "" {
			name = call.ContractID
		}
		item := SimulateBatchItem{Name: name}
		simulated, err := p.Simulate(ctx, call.SimulateRequest)
		if item.Name == "" && simulated != nil {
			item.Name = simulated.ContractID
		}
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			item.Error = err.Error()
		} else {
			item.Result = simulated
		}
		result.Items = append(result.Items, item)
	}
	return result, nil
}

// NamedArg is a decoded invocation argument paired with its parameter name and
// declared type when the target contract's interface is available.
type NamedArg struct {
	Name  string `json:"name,omitempty"`
	Type  string `json:"type,omitempty"`
	Value any    `json:"value"`
}

// SimulateResult is the outcome of a simulated invocation.
type SimulateResult struct {
	ContractID string   `json:"contract_id"`
	Function   string   `json:"function"`
	Args       []string `json:"args,omitempty"`

	// Signature is the function's declared signature, present when the
	// arguments were typed or decoded from the contract's interface.
	Signature string `json:"signature,omitempty"`
	// EncodedArgs are the arguments as actually encoded, after typing from
	// the interface. Present only when typing changed at least one.
	EncodedArgs []string `json:"encoded_args,omitempty"`
	// NamedArgs are the decoded arguments of a prebuilt transaction
	// envelope, paired with parameter names and declared types when the
	// target contract's interface is available.
	NamedArgs []NamedArg `json:"named_args,omitempty"`
	// ABINote explains why an interface source was configured but not
	// used, so a fallback to inference is visible rather than silent.
	ABINote string `json:"abi_note,omitempty"`

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

	// Comparison is the cost compared against a baseline, when a baseline
	// was supplied.
	Comparison *CostComparison `json:"comparison,omitempty"`

	// Spread describes how the cost varied across repeated runs, when
	// --repeat asked for more than one.
	Spread *SpreadReport `json:"spread,omitempty"`

	// RestoreRequired is set when archived entries block the call.
	RestoreRequired *RestoreInfo `json:"restore_required,omitempty"`

	// Events are decoded diagnostic events from the simulation.
	Events []Event `json:"events,omitempty"`

	// Auth lists the authorization entries the call requires.
	Auth []AuthEntry `json:"auth,omitempty"`

	// LatestLedger is the ledger the simulation ran against.
	LatestLedger uint32 `json:"latest_ledger"`
}

// AuthorizedInvocation is a node in an authorized invocation tree.
type AuthorizedInvocation struct {
	ContractID     string                 `json:"contract_id,omitempty"`
	Function       string                 `json:"function,omitempty"`
	Signature      string                 `json:"signature,omitempty"`
	Args           []any                  `json:"args,omitempty"`
	NamedArgs      []NamedArg             `json:"named_args,omitempty"`
	SubInvocations []AuthorizedInvocation `json:"sub_invocations,omitempty"`
}

// AuthEntry represents an authorization required by an invocation.
type AuthEntry struct {
	Address         string                `json:"address,omitempty"`
	IsSourceAccount bool                  `json:"is_source_account"`
	RootInvocation  *AuthorizedInvocation `json:"root_invocation,omitempty"`
	RawXDR          string                `json:"raw_xdr,omitempty"`
}

// DefaultBaseFee is the network minimum base fee for a single transaction operation (100 stroops).
const DefaultBaseFee int64 = 100

// EstimateTotalFee calculates the estimated total fee for a simulated call:
// resource fee plus the base inclusion fee (base fee × 1 operation).
func EstimateTotalFee(resourceFee, baseFee int64) int64 {
	if baseFee <= 0 {
		baseFee = DefaultBaseFee
	}
	return resourceFee + baseFee
}

// Simulate builds an invocation (or uses a prebuilt transaction envelope),
// simulates it, and reports the decoded result alongside its resource cost.
//
// A simulation that the network rejects is not a Go error: it is a result with
// Success false and Error set. Go errors are reserved for problems reaching the
// network or encoding/validating the request.
func (p *Prober) Simulate(ctx context.Context, req SimulateRequest) (*SimulateResult, error) {
	var (
		envelope     string
		typed        typedArgs
		namedArgs    []NamedArg
		envelopeAuth []xdr.SorobanAuthorizationEntry
		sourceAddr   = p.source
	)

	if strings.TrimSpace(req.Envelope) != "" {
		if req.ContractID != "" || req.Function != "" || len(req.Args) > 0 {
			return nil, fmt.Errorf("%w: give either a contract call or a prebuilt envelope, not both", ErrInvalidEnvelope)
		}
		parsed, err := p.parseEnvelope(ctx, req.Envelope)
		if err != nil {
			return nil, err
		}
		req.ContractID = parsed.contractID
		req.Function = parsed.function
		req.Args = parsed.argStrings
		envelope = parsed.envelope
		typed.signature = parsed.signature
		typed.note = parsed.abiNote
		namedArgs = parsed.namedArgs
		envelopeAuth = parsed.auth
		if parsed.sourceAccount != "" {
			sourceAddr = parsed.sourceAccount
		}
		if err := checkBaseline(req); err != nil {
			return nil, err
		}
	} else {
		contract, err := stellar.ParseContractID(req.ContractID)
		if err != nil {
			return nil, err
		}
		if req.Function == "" {
			return nil, fmt.Errorf("a function name is required")
		}
		// A baseline for a different call is rejected before anything is sent:
		// its costs would describe a call that was never made.
		if err := checkBaseline(req); err != nil {
			return nil, err
		}

		typed, err = p.typeArgs(ctx, req)
		if err != nil {
			return nil, err
		}

		args, err := p.codec.EncodeAll(typed.args)
		if err != nil {
			return nil, err
		}

		envelope, err = stellar.BuildInvocationTx(stellar.InvocationParams{
			SourceAccount: p.source,
			Contract:      contract,
			Function:      req.Function,
			Args:          args,
		})
		if err != nil {
			return nil, err
		}
	}

	if req.Repeat < 1 {
		req.Repeat = 1
	}

	costs := make([]Cost, 0, req.Repeat)
	footprints := make([]Footprint, 0, req.Repeat)
	var result *SimulateResult
	for i := 0; i < req.Repeat; i++ {
		resp, err := p.client.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{
			Transaction: envelope,
		})
		if err != nil {
			return nil, err
		}

		built, err := p.buildSimulateResultWithAuth(ctx, req, resp, sourceAddr, envelopeAuth)
		if err != nil {
			return nil, err
		}
		// The last run is the one reported in full; the earlier runs only
		// contribute to the spread. The call is deterministic in the
		// success case, so they agree on everything the report shows.
		result = built
		costs = append(costs, built.Cost)
		footprints = append(footprints, built.Footprint)
	}

	result.Signature = typed.signature
	result.ABINote = typed.note
	if typed.changed {
		result.EncodedArgs = typed.args
	}
	if len(namedArgs) > 0 {
		result.NamedArgs = namedArgs
	}

	// A repeated run is judged by its worst case: the maximum of every
	// figure cannot make a flaky cost check pass by luck.
	if req.Repeat > 1 {
		spread := SpreadCost(costs, footprints, req.MaxSpreadPercent)
		result.Spread = &spread
	}

	if req.Baseline != nil {
		// With repeated runs the comparison is against the worst case
		// observed, not the last sample: a flaky estimate must not make a
		// cost check pass by luck.
		current := result.Cost
		if result.Spread != nil {
			current = result.Spread.Max
		}
		comparison := CompareCost(req.Baseline.Cost, current, req.MaxIncreasePercent)
		result.Comparison = &comparison
	}
	return result, nil
}

type parsedEnvelope struct {
	envelope      string
	sourceAccount string
	contractID    string
	function      string
	argStrings    []string
	namedArgs     []NamedArg
	signature     string
	abiNote       string
	auth          []xdr.SorobanAuthorizationEntry
}

// parseEnvelope decodes and validates a prebuilt base64 TransactionEnvelope.
//
// It rejects non-Soroban operations, footprint-only maintenance operations,
// non-invocation host functions (upload/create), and multi-operation
// transactions with a clear explanation before any RPC call is made.
func (p *Prober) parseEnvelope(ctx context.Context, raw string) (parsedEnvelope, error) {
	cleaned := strings.Join(strings.Fields(raw), "")
	if cleaned == "" {
		return parsedEnvelope{}, fmt.Errorf("%w: transaction envelope is empty", ErrInvalidEnvelope)
	}

	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(cleaned, &env); err != nil {
		return parsedEnvelope{}, fmt.Errorf("%w: decode base64 XDR: %w", ErrInvalidEnvelope, err)
	}

	ops := env.Operations()
	if len(ops) == 0 {
		return parsedEnvelope{}, fmt.Errorf("%w: transaction has no operations; Soroban simulation requires exactly 1 InvokeHostFunction operation", ErrInvalidEnvelope)
	}
	if len(ops) > 1 {
		return parsedEnvelope{}, fmt.Errorf("%w: multi-operation transactions are not supported (got %d operations); Soroban transactions allow only 1 InvokeHostFunction operation", ErrInvalidEnvelope, len(ops))
	}

	op := ops[0]
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
		switch op.Body.Type {
		case xdr.OperationTypeExtendFootprintTtl, xdr.OperationTypeRestoreFootprint:
			return parsedEnvelope{}, fmt.Errorf("%w: operation is %s (ledger footprint maintenance), not a contract invocation (InvokeHostFunction)", ErrInvalidEnvelope, op.Body.Type)
		default:
			return parsedEnvelope{}, fmt.Errorf("%w: non-Soroban operation %s; simulate requires an InvokeHostFunction contract invocation", ErrInvalidEnvelope, op.Body.Type)
		}
	}

	invokeOp, ok := op.Body.GetInvokeHostFunctionOp()
	if !ok {
		return parsedEnvelope{}, fmt.Errorf("%w: operation body is missing InvokeHostFunction payload", ErrInvalidEnvelope)
	}
	if invokeOp.HostFunction.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
		return parsedEnvelope{}, fmt.Errorf("%w: host function is %s, not InvokeContract; only contract function invocations can be simulated", ErrInvalidEnvelope, invokeOp.HostFunction.Type)
	}
	invokeContract, ok := invokeOp.HostFunction.GetInvokeContract()
	if !ok {
		return parsedEnvelope{}, fmt.Errorf("%w: InvokeContract payload is missing", ErrInvalidEnvelope)
	}

	contractID, err := invokeContract.ContractAddress.String()
	if err != nil || !strings.HasPrefix(contractID, "C") {
		return parsedEnvelope{}, fmt.Errorf("%w: invalid target contract address in InvokeContract", ErrInvalidEnvelope)
	}
	function := string(invokeContract.FunctionName)
	if function == "" {
		return parsedEnvelope{}, fmt.Errorf("%w: InvokeContract has an empty function name", ErrInvalidEnvelope)
	}

	sourceAccount := envelopeSourceAccount(env, op)

	decodedVals := make([]any, 0, len(invokeContract.Args))
	argStrings := make([]string, 0, len(invokeContract.Args))
	namedArgs := make([]NamedArg, 0, len(invokeContract.Args))
	for i, arg := range invokeContract.Args {
		val, decErr := p.codec.Decode(arg)
		if decErr != nil {
			return parsedEnvelope{}, fmt.Errorf("%w: decode argument %d: %w", ErrInvalidEnvelope, i, decErr)
		}
		decodedVals = append(decodedVals, val)
		typeName := scValKindName(arg.Type)
		argStrings = append(argStrings, formatScValArgString(typeName, val))
		namedArgs = append(namedArgs, NamedArg{
			Type:  typeName,
			Value: val,
		})
	}

	out := parsedEnvelope{
		envelope:      cleaned,
		sourceAccount: sourceAccount,
		contractID:    contractID,
		function:      function,
		argStrings:    argStrings,
		namedArgs:     namedArgs,
		auth:          invokeOp.Auth,
	}

	if p.abi != nil {
		fn, note, err := p.lookupABIFunction(ctx, contractID, function)
		if err != nil {
			return parsedEnvelope{}, err
		}
		out.abiNote = note
		if fn != nil {
			out.signature = fn.Signature()
			for i := range out.namedArgs {
				if i < len(fn.Inputs) {
					out.namedArgs[i].Name = fn.Inputs[i].Name
					out.namedArgs[i].Type = fn.Inputs[i].Type.Display
				}
			}
			if len(fn.Inputs) != len(out.namedArgs) && out.abiNote == "" {
				out.abiNote = fmt.Sprintf("envelope passes %d argument(s), but %s declares %d",
					len(out.namedArgs), fn.Signature(), len(fn.Inputs))
			}
		}
	}

	return out, nil
}

func envelopeSourceAccount(env xdr.TransactionEnvelope, op xdr.Operation) string {
	if op.SourceAccount != nil {
		acct := op.SourceAccount.ToAccountId()
		if addr := acct.Address(); addr != "" {
			return addr
		}
	}
	src := env.SourceAccount()
	acct := src.ToAccountId()
	return acct.Address()
}

func scValKindName(t xdr.ScValType) string {
	switch t {
	case xdr.ScValTypeScvBool:
		return "bool"
	case xdr.ScValTypeScvVoid:
		return "void"
	case xdr.ScValTypeScvError:
		return "error"
	case xdr.ScValTypeScvU32:
		return "u32"
	case xdr.ScValTypeScvI32:
		return "i32"
	case xdr.ScValTypeScvU64:
		return "u64"
	case xdr.ScValTypeScvI64:
		return "i64"
	case xdr.ScValTypeScvTimepoint:
		return "timepoint"
	case xdr.ScValTypeScvDuration:
		return "duration"
	case xdr.ScValTypeScvU128:
		return "u128"
	case xdr.ScValTypeScvI128:
		return "i128"
	case xdr.ScValTypeScvU256:
		return "u256"
	case xdr.ScValTypeScvI256:
		return "i256"
	case xdr.ScValTypeScvBytes:
		return "bytes"
	case xdr.ScValTypeScvString:
		return "string"
	case xdr.ScValTypeScvSymbol:
		return "symbol"
	case xdr.ScValTypeScvVec:
		return "vec"
	case xdr.ScValTypeScvMap:
		return "map"
	case xdr.ScValTypeScvAddress:
		return "address"
	default:
		return t.String()
	}
}

func formatScValArgString(typeName string, val any) string {
	if typeName == "void" || val == nil {
		return "void"
	}
	switch v := val.(type) {
	case string:
		return typeName + ":" + v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%s:%v", typeName, v)
		}
		return typeName + ":" + string(b)
	}
}

// lookupABIFunction resolves a contract function from the configured ABI
// source, returning a human-readable note when the contract or function is
// unavailable so envelope decoding degrades gracefully.
func (p *Prober) lookupABIFunction(ctx context.Context, contractID, function string) (*abi.Function, string, error) {
	if p.abi == nil || contractID == "" || function == "" {
		return nil, "", nil
	}
	iface, err := p.abi.Interface(ctx, p.network, contractID)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		var note string
		switch {
		case errors.Is(err, abi.ErrUnknownContract):
			note = "contract not found in the interface registry; argument types were decoded from XDR"
		case errors.Is(err, abi.ErrNoWasm):
			note = "stellar asset contract has no WASM interface; argument types were decoded from XDR"
		default:
			note = "interface registry unavailable; argument types were decoded from XDR: " + err.Error()
		}
		p.log.Debug("abi lookup failed", "contract", contractID, "err", err)
		return nil, note, nil
	}
	fn, err := iface.Lookup(function)
	if err != nil {
		return nil, err.Error(), nil
	}
	return fn, "", nil
}

// checkBaseline rejects a baseline that does not describe the call being
// simulated.
//
// A baseline is a report of one contract's one function. Its costs say
// nothing about another call, so comparing them would report a change that
// never happened — which is worse than no comparison at all, because it looks
// like a measurement.
func checkBaseline(req SimulateRequest) error {
	if req.Baseline == nil {
		return nil
	}
	if req.Baseline.ContractID == "" || req.Baseline.Function == "" {
		return fmt.Errorf("%w: the baseline must name the contract and function it was taken against",
			ErrBaselineMismatch)
	}
	if req.Baseline.ContractID != req.ContractID {
		return fmt.Errorf("%w: the baseline is for contract %s, not %s",
			ErrBaselineMismatch, req.Baseline.ContractID, req.ContractID)
	}
	if req.Baseline.Function != req.Function {
		return fmt.Errorf("%w: the baseline is for function %s, not %s",
			ErrBaselineMismatch, req.Baseline.Function, req.Function)
	}
	return nil
}

// typedArgs is the outcome of typing a request's arguments.
type typedArgs struct {
	args      []string
	signature string
	note      string
	changed   bool
}

// typeArgs types req.Args from the contract's interface when an ABI source
// is configured.
//
// A contract the source does not know, or a source that cannot be reached,
// falls back to inference with a note, because the probe is still worth
// running. A function the interface does not export, or the wrong number of
// arguments, is a definite error: the simulation could only fail, and less
// helpfully.
func (p *Prober) typeArgs(ctx context.Context, req SimulateRequest) (typedArgs, error) {
	out := typedArgs{args: req.Args}
	if p.abi == nil {
		return out, nil
	}
	typer, ok := p.codec.(interface{ HasType(string) bool })
	if !ok {
		// A custom codec with its own syntax: SoroProbe cannot tell which
		// arguments already carry a type, so it does not rewrite any.
		return out, nil
	}

	iface, err := p.abi.Interface(ctx, p.network, req.ContractID)
	if err != nil {
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		if errors.Is(err, abi.ErrUnknownContract) {
			out.note = "contract not found in the interface registry; argument types were inferred"
		} else if errors.Is(err, abi.ErrNoWasm) {
			out.note = "stellar asset contract has no WASM interface; argument types were inferred"
		} else {
			out.note = "interface registry unavailable; argument types were inferred: " + err.Error()
		}
		p.log.Debug("abi lookup failed", "contract", req.ContractID, "err", err)
		return out, nil
	}

	fn, err := iface.Lookup(req.Function)
	if err != nil {
		return out, err
	}

	args, err := abi.Annotate(fn, req.Args, typer.HasType)
	if err != nil {
		return out, err
	}

	out.args = args
	out.signature = fn.Signature()
	for i := range args {
		if args[i] != req.Args[i] {
			out.changed = true
			break
		}
	}
	return out, nil
}

func (p *Prober) buildSimulateResultWithAuth(
	ctx context.Context,
	req SimulateRequest,
	resp protocol.SimulateTransactionResponse,
	sourceAddr string,
	envelopeAuth []xdr.SorobanAuthorizationEntry,
) (*SimulateResult, error) {
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
		var data xdr.SorobanTransactionData
		if err := xdr.SafeUnmarshalBase64(resp.RestorePreamble.TransactionDataXDR, &data); err != nil {
			return nil, fmt.Errorf("decode restore transaction data: %w", err)
		}
		for _, key := range data.Resources.Footprint.ReadWrite {
			result.RestoreRequired.ArchivedEntries = append(result.RestoreRequired.ArchivedEntries, describeLedgerKey(key))
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

	if len(resp.Results) > 0 && resp.Results[0].AuthXDR != nil && len(*resp.Results[0].AuthXDR) > 0 {
		result.Auth = p.decodeAuthEntries(ctx, *resp.Results[0].AuthXDR, sourceAddr)
	} else if len(envelopeAuth) > 0 {
		result.Auth = p.decodeAuthEntryStructs(ctx, envelopeAuth, sourceAddr)
	}

	result.Cost.EstimatedTotalFee = EstimateTotalFee(result.Cost.ResourceFee, p.baseFee)

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

func (p *Prober) decodeAuthEntries(ctx context.Context, raw []string, sourceAddr string) []AuthEntry {
	if len(raw) == 0 {
		return nil
	}
	out := make([]AuthEntry, 0, len(raw))
	for _, b64 := range raw {
		var entry xdr.SorobanAuthorizationEntry
		if err := xdr.SafeUnmarshalBase64(b64, &entry); err != nil {
			out = append(out, AuthEntry{RawXDR: b64})
			continue
		}
		ae, ok := p.decodeOneAuthEntry(ctx, entry, sourceAddr)
		if !ok {
			out = append(out, AuthEntry{RawXDR: b64})
			continue
		}
		out = append(out, ae)
	}
	return out
}

func (p *Prober) decodeAuthEntryStructs(ctx context.Context, entries []xdr.SorobanAuthorizationEntry, sourceAddr string) []AuthEntry {
	if len(entries) == 0 {
		return nil
	}
	out := make([]AuthEntry, 0, len(entries))
	for _, entry := range entries {
		ae, ok := p.decodeOneAuthEntry(ctx, entry, sourceAddr)
		if !ok {
			if b64, err := xdr.MarshalBase64(entry); err == nil {
				out = append(out, AuthEntry{RawXDR: b64})
			}
			continue
		}
		out = append(out, ae)
	}
	return out
}

func (p *Prober) decodeOneAuthEntry(ctx context.Context, entry xdr.SorobanAuthorizationEntry, sourceAddr string) (AuthEntry, bool) {
	if sourceAddr == "" {
		sourceAddr = p.source
	}
	ae := AuthEntry{}
	switch entry.Credentials.Type {
	case xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount:
		ae.Address = sourceAddr
		ae.IsSourceAccount = true
	case xdr.SorobanCredentialsTypeSorobanCredentialsAddress:
		if entry.Credentials.Address != nil {
			if s, err := entry.Credentials.Address.Address.String(); err == nil {
				ae.Address = s
			}
		}
		ae.IsSourceAccount = (ae.Address != "" && ae.Address == sourceAddr)
	case xdr.SorobanCredentialsTypeSorobanCredentialsAddressV2:
		if entry.Credentials.AddressV2 != nil {
			if s, err := entry.Credentials.AddressV2.Address.String(); err == nil {
				ae.Address = s
			}
		}
		ae.IsSourceAccount = (ae.Address != "" && ae.Address == sourceAddr)
	case xdr.SorobanCredentialsTypeSorobanCredentialsAddressWithDelegates:
		if entry.Credentials.AddressWithDelegates != nil {
			if s, err := entry.Credentials.AddressWithDelegates.AddressCredentials.Address.String(); err == nil {
				ae.Address = s
			}
		}
		ae.IsSourceAccount = (ae.Address != "" && ae.Address == sourceAddr)
	default:
		ae.IsSourceAccount = false
	}

	inv, err := p.decodeAuthorizedInvocation(ctx, entry.RootInvocation)
	if err != nil {
		return AuthEntry{}, false
	}
	ae.RootInvocation = &inv
	return ae, true
}

func (p *Prober) decodeAuthorizedInvocation(ctx context.Context, inv xdr.SorobanAuthorizedInvocation) (AuthorizedInvocation, error) {
	out := AuthorizedInvocation{}
	switch inv.Function.Type {
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn:
		fn := inv.Function.ContractFn
		if fn != nil {
			if s, err := fn.ContractAddress.String(); err == nil {
				out.ContractID = s
			}
			out.Function = string(fn.FunctionName)
			var abiFn *abi.Function
			if p.abi != nil && out.ContractID != "" && out.Function != "" {
				abiFn, _, _ = p.lookupABIFunction(ctx, out.ContractID, out.Function)
				if abiFn != nil {
					out.Signature = abiFn.Signature()
				}
			}
			for i, arg := range fn.Args {
				decoded, err := p.codec.Decode(arg)
				if err != nil {
					return out, err
				}
				out.Args = append(out.Args, decoded)
				if abiFn != nil {
					na := NamedArg{
						Type:  scValKindName(arg.Type),
						Value: decoded,
					}
					if i < len(abiFn.Inputs) {
						na.Name = abiFn.Inputs[i].Name
						na.Type = abiFn.Inputs[i].Type.Display
					}
					out.NamedArgs = append(out.NamedArgs, na)
				}
			}
		}
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractHostFn:
		out.Function = "create_contract"
	case xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeCreateContractV2HostFn:
		out.Function = "create_contract_v2"
	default:
		return out, fmt.Errorf("unknown authorized function type: %v", inv.Function.Type)
	}

	for _, sub := range inv.SubInvocations {
		subInv, err := p.decodeAuthorizedInvocation(ctx, sub)
		if err != nil {
			return out, err
		}
		out.SubInvocations = append(out.SubInvocations, subInv)
	}
	return out, nil
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

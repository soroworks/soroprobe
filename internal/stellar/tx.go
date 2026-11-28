package stellar

import (
	"errors"
	"fmt"
	"strings"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrInvalidEnvelope reports a transaction envelope that cannot be simulated
// as a Soroban contract invocation.
var ErrInvalidEnvelope = errors.New("invalid transaction envelope")

// ParsedInvocation holds the contract call extracted from a prebuilt
// transaction envelope.
type ParsedInvocation struct {
	Envelope   string
	ContractID string
	Function   string
	Args       []xdr.ScVal
	Auth       []xdr.SorobanAuthorizationEntry
}

// ParseInvocationEnvelope decodes a base64-encoded TransactionEnvelope and
// extracts its single Soroban InvokeContract operation. Multi-operation or
// non-Soroban envelopes are rejected with a descriptive error wrapping
// ErrInvalidEnvelope.
func ParseInvocationEnvelope(rawBase64 string) (*ParsedInvocation, error) {
	trimmed := strings.TrimSpace(rawBase64)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: transaction envelope is empty", ErrInvalidEnvelope)
	}

	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(trimmed, &env); err != nil {
		return nil, fmt.Errorf("%w: could not decode base64 XDR transaction envelope: %w", ErrInvalidEnvelope, err)
	}

	ops := env.Operations()
	if len(ops) == 0 {
		return nil, fmt.Errorf("%w: transaction envelope contains no operations; Soroban simulation requires a single InvokeHostFunction operation", ErrInvalidEnvelope)
	}
	if len(ops) > 1 {
		return nil, fmt.Errorf("%w: transaction envelope contains %d operations; Soroban transactions allow only a single operation", ErrInvalidEnvelope, len(ops))
	}

	op := ops[0]
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction || op.Body.InvokeHostFunctionOp == nil {
		return nil, fmt.Errorf("%w: non-Soroban operation %s cannot be simulated as a contract invocation (expected InvokeHostFunction)", ErrInvalidEnvelope, op.Body.Type)
	}

	ihf := op.Body.InvokeHostFunctionOp
	if ihf.HostFunction.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract || ihf.HostFunction.InvokeContract == nil {
		return nil, fmt.Errorf("%w: unsupported Soroban host function %s (expected InvokeContract)", ErrInvalidEnvelope, ihf.HostFunction.Type)
	}

	inv := ihf.HostFunction.InvokeContract
	contractID, err := inv.ContractAddress.String()
	if err != nil || !strings.HasPrefix(contractID, "C") {
		return nil, fmt.Errorf("%w: invalid target contract address in envelope", ErrInvalidEnvelope)
	}
	fnName := string(inv.FunctionName)
	if fnName == "" {
		return nil, fmt.Errorf("%w: invocation in envelope has an empty function name", ErrInvalidEnvelope)
	}

	return &ParsedInvocation{
		Envelope:   trimmed,
		ContractID: contractID,
		Function:   fnName,
		Args:       inv.Args,
		Auth:       ihf.Auth,
	}, nil
}

// InvocationParams describes the contract call to be simulated.
type InvocationParams struct {
	// SourceAccount is the public key ("G...") used as the transaction source.
	SourceAccount string
	// Contract is the contract being invoked.
	Contract xdr.ScAddress
	// Function is the exported contract function name.
	Function string
	// Args are the already-encoded arguments.
	Args []xdr.ScVal
}

// BuildInvocationTx assembles an unsigned transaction envelope containing a
// single InvokeHostFunction operation, serialized as base64 for
// simulateTransaction.
//
// The envelope is never signed and never submitted. Simulation reads the source
// account only to build a well-formed transaction; the RPC server does not
// verify signatures, and the account need not exist or hold a balance. That is
// why SoroProbe asks for a public key and never a secret key.
//
// The sequence number is fixed at 0 for the same reason: simulation does not
// validate it, so SoroProbe avoids an extra account lookup and works even for
// accounts that have never been funded.
func BuildInvocationTx(p InvocationParams) (string, error) {
	if p.SourceAccount == "" {
		return "", fmt.Errorf("source account is required to build a transaction to simulate")
	}
	if p.Function == "" {
		return "", fmt.Errorf("function name is required")
	}

	args := p.Args
	if args == nil {
		args = []xdr.ScVal{}
	}

	account := txnbuild.NewSimpleAccount(p.SourceAccount, 0)
	op := &txnbuild.InvokeHostFunction{
		HostFunction: xdr.HostFunction{
			Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
			InvokeContract: &xdr.InvokeContractArgs{
				ContractAddress: p.Contract,
				FunctionName:    xdr.ScSymbol(p.Function),
				Args:            args,
			},
		},
		SourceAccount: p.SourceAccount,
	}

	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	if err != nil {
		return "", fmt.Errorf("build transaction: %w", err)
	}

	envelope, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encode transaction envelope: %w", err)
	}
	return envelope, nil
}

// ExtendParams describes a TTL extension to be estimated.
type ExtendParams struct {
	// SourceAccount is the public key ("G...") used as the transaction source.
	SourceAccount string
	// Key is the ledger entry whose TTL is extended. It is the only entry in
	// the footprint, so the fee covers that entry alone.
	Key xdr.LedgerKey
	// ExtendTo is the ledger the entry's TTL is set to.
	ExtendTo uint32
}

// extendResourceLimits are the resource limits declared on an extension.
//
// The RPC recomputes the resources during a simulation and reports what the
// operation really costs, so these are only there to keep the transaction
// well-formed. They match what the Stellar CLI declares for the same
// operation.
var extendResourceLimits = xdr.SorobanResources{
	Instructions:  0,
	DiskReadBytes: 0,
	WriteBytes:    0,
}

// BuildExtendFootprintTx assembles an unsigned transaction envelope holding a
// single ExtendFootprintTTL operation for one ledger entry.
//
// The entry sits in the read-only footprint, which is where the Stellar CLI
// puts it: the operation changes the entry's TTL, not its contents, and the
// host extends the entries of both footprint sets.
//
// The envelope is never signed and never submitted, exactly like the
// invocation envelope. Simulation reads the source account only to build a
// well-formed transaction, so the placeholder account SoroProbe uses works
// here too.
func BuildExtendFootprintTx(p ExtendParams) (string, error) {
	if p.SourceAccount == "" {
		return "", fmt.Errorf("source account is required to build a transaction to simulate")
	}

	op := &txnbuild.ExtendFootprintTtl{
		ExtendTo:      p.ExtendTo,
		SourceAccount: p.SourceAccount,
		Ext: xdr.TransactionExt{
			V: 1,
			SorobanData: &xdr.SorobanTransactionData{
				Resources: xdr.SorobanResources{
					Footprint: xdr.LedgerFootprint{
						ReadOnly:  []xdr.LedgerKey{p.Key},
						ReadWrite: []xdr.LedgerKey{},
					},
					Instructions:  extendResourceLimits.Instructions,
					DiskReadBytes: extendResourceLimits.DiskReadBytes,
					WriteBytes:    extendResourceLimits.WriteBytes,
				},
				ResourceFee: 0,
			},
		},
	}

	account := txnbuild.NewSimpleAccount(p.SourceAccount, 0)
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{op},
		BaseFee:              txnbuild.MinBaseFee,
		Preconditions:        txnbuild.Preconditions{TimeBounds: txnbuild.NewInfiniteTimeout()},
	})
	if err != nil {
		return "", fmt.Errorf("build transaction: %w", err)
	}

	envelope, err := tx.Base64()
	if err != nil {
		return "", fmt.Errorf("encode transaction envelope: %w", err)
	}
	return envelope, nil
}

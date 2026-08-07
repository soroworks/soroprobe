package stellar

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

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

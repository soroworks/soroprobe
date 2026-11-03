package probe_test

import (
	"context"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func buildTransferEnvelopeWithAuth(t *testing.T, withEmbeddedAuth bool) string {
	t.Helper()

	sacAddr, err := stellar.ParseContractID(stellartest.SACContract)
	require.NoError(t, err)
	wasmAddr, err := stellar.ParseContractID(stellartest.WasmContract)
	require.NoError(t, err)

	sourceAcct := xdr.MustAddress(stellartest.SourceAccount)
	fromScAddr := xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &sourceAcct,
	}
	fromVal := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &fromScAddr}
	toVal := xdr.ScVal{Type: xdr.ScValTypeScvAddress, Address: &wasmAddr}
	hi := xdr.Int64(0)
	lo := xdr.Uint64(250)
	parts := xdr.Int128Parts{Hi: hi, Lo: lo}
	amountVal := xdr.ScVal{Type: xdr.ScValTypeScvI128, I128: &parts}

	var auth []xdr.SorobanAuthorizationEntry
	if withEmbeddedAuth {
		subFn := xdr.InvokeContractArgs{
			ContractAddress: wasmAddr,
			FunctionName:    xdr.ScSymbol("on_transfer"),
			Args:            []xdr.ScVal{fromVal, amountVal},
		}
		rootFn := xdr.InvokeContractArgs{
			ContractAddress: sacAddr,
			FunctionName:    xdr.ScSymbol("transfer"),
			Args:            []xdr.ScVal{fromVal, toVal, amountVal},
		}
		auth = []xdr.SorobanAuthorizationEntry{
			{
				Credentials: xdr.SorobanCredentials{
					Type: xdr.SorobanCredentialsTypeSorobanCredentialsSourceAccount,
				},
				RootInvocation: xdr.SorobanAuthorizedInvocation{
					Function: xdr.SorobanAuthorizedFunction{
						Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
						ContractFn: &rootFn,
					},
					SubInvocations: []xdr.SorobanAuthorizedInvocation{
						{
							Function: xdr.SorobanAuthorizedFunction{
								Type:       xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn,
								ContractFn: &subFn,
							},
						},
					},
				},
			},
		}
	}

	env := xdr.TransactionEnvelope{
		Type: xdr.EnvelopeTypeEnvelopeTypeTx,
		V1: &xdr.TransactionV1Envelope{
			Tx: xdr.Transaction{
				SourceAccount: sourceAcct.ToMuxedAccount(),
				Fee:           4200,
				SeqNum:        xdr.SequenceNumber(987654321),
				Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
				Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
				Operations: []xdr.Operation{
					{
						Body: xdr.OperationBody{
							Type: xdr.OperationTypeInvokeHostFunction,
							InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
								HostFunction: xdr.HostFunction{
									Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
									InvokeContract: &xdr.InvokeContractArgs{
										ContractAddress: sacAddr,
										FunctionName:    xdr.ScSymbol("transfer"),
										Args:            []xdr.ScVal{fromVal, toVal, amountVal},
									},
								},
								Auth: auth,
							},
						},
					},
				},
				Ext: xdr.TransactionExt{
					V:           1,
					SorobanData: &xdr.SorobanTransactionData{},
				},
			},
		},
	}

	b64, err := xdr.MarshalBase64(env)
	require.NoError(t, err)
	return b64
}

func TestSimulatePrebuiltEnvelopeAsGiven(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
	})
	require.NoError(t, err)

	env := buildTransferEnvelopeWithAuth(t, false)
	result, err := p.Simulate(context.Background(), probe.SimulateRequest{
		Envelope: "  " + env + "\n",
	})
	require.NoError(t, err)
	require.Len(t, fake.SimulateRequests, 1)
	assert.Equal(t, env, fake.SimulateRequests[0].Transaction, "the prebuilt envelope must be simulated as given without rebuilding")

	assert.True(t, result.Success)
	assert.Equal(t, stellartest.SACContract, result.ContractID)
	assert.Equal(t, "transfer", result.Function)
	require.Len(t, result.NamedArgs, 3)
	assert.Equal(t, "address", result.NamedArgs[0].Type)
	assert.Equal(t, stellartest.SourceAccount, result.NamedArgs[0].Value)
	assert.Equal(t, "address", result.NamedArgs[1].Type)
	assert.Equal(t, stellartest.WasmContract, result.NamedArgs[1].Value)
	assert.Equal(t, "i128", result.NamedArgs[2].Type)
	assert.Equal(t, "250", result.NamedArgs[2].Value)
}

func TestSimulateEnvelopeDecodesEveryInvocationWithABI(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	// Clear recorded AuthXDR on transfer so the envelope's embedded multi-contract
	// authorization tree is exercised, then also test RPC-returned AuthXDR.
	simResp := fake.Simulations["transfer"]
	if len(simResp.Results) > 0 {
		simResp.Results[0].AuthXDR = nil
	}
	fake.Simulations["transfer"] = simResp

	src := &fakeABI{
		ifaces: map[string]*abi.Interface{
			stellartest.SACContract: {
				Functions: []abi.Function{
					{
						Name: "transfer",
						Inputs: []abi.Param{
							{Name: "from", Type: abi.Type{Kind: "address", Display: "Address"}},
							{Name: "to", Type: abi.Type{Kind: "address", Display: "Address"}},
							{Name: "amount", Type: abi.Type{Kind: "i128", Display: "i128"}},
						},
					},
				},
			},
			stellartest.WasmContract: {
				Functions: []abi.Function{
					{
						Name: "on_transfer",
						Inputs: []abi.Param{
							{Name: "sender", Type: abi.Type{Kind: "address", Display: "Address"}},
							{Name: "transferred", Type: abi.Type{Kind: "i128", Display: "i128"}},
						},
						Outputs: []abi.Type{{Kind: "bool", Display: "bool"}},
					},
				},
			},
		},
	}

	p := newABIProber(t, fake, src)
	env := buildTransferEnvelopeWithAuth(t, true)

	result, err := p.Simulate(context.Background(), probe.SimulateRequest{
		Envelope: env,
	})
	require.NoError(t, err)

	assert.Equal(t, "fn transfer(from: Address, to: Address, amount: i128)", result.Signature)
	require.Len(t, result.NamedArgs, 3)
	assert.Equal(t, probe.NamedArg{Name: "from", Type: "Address", Value: stellartest.SourceAccount}, result.NamedArgs[0])
	assert.Equal(t, probe.NamedArg{Name: "to", Type: "Address", Value: stellartest.WasmContract}, result.NamedArgs[1])
	assert.Equal(t, probe.NamedArg{Name: "amount", Type: "i128", Value: "250"}, result.NamedArgs[2])

	require.Len(t, result.Auth, 1)
	root := result.Auth[0].RootInvocation
	require.NotNil(t, root)
	assert.Equal(t, "fn transfer(from: Address, to: Address, amount: i128)", root.Signature)
	require.Len(t, root.NamedArgs, 3)
	assert.Equal(t, "from", root.NamedArgs[0].Name)
	assert.Equal(t, "to", root.NamedArgs[1].Name)
	assert.Equal(t, "amount", root.NamedArgs[2].Name)

	require.Len(t, root.SubInvocations, 1)
	sub := root.SubInvocations[0]
	assert.Equal(t, stellartest.WasmContract, sub.ContractID)
	assert.Equal(t, "on_transfer", sub.Function)
	assert.Equal(t, "fn on_transfer(sender: Address, transferred: i128) -> bool", sub.Signature)
	require.Len(t, sub.NamedArgs, 2)
	assert.Equal(t, probe.NamedArg{Name: "sender", Type: "Address", Value: stellartest.SourceAccount}, sub.NamedArgs[0])
	assert.Equal(t, probe.NamedArg{Name: "transferred", Type: "i128", Value: "250"}, sub.NamedArgs[1])
}

func TestSimulateEnvelopeFallsBackWhenABIUnavailable(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p := newABIProber(t, fake, &fakeABI{})

	env := buildTransferEnvelopeWithAuth(t, false)
	result, err := p.Simulate(context.Background(), probe.SimulateRequest{
		Envelope: env,
	})
	require.NoError(t, err)
	assert.True(t, result.Success)
	assert.Empty(t, result.Signature)
	assert.Contains(t, result.ABINote, "not found in the interface registry")
	require.Len(t, result.NamedArgs, 3)
	assert.Empty(t, result.NamedArgs[0].Name)
	assert.Equal(t, "address", result.NamedArgs[0].Type)
}

func TestSimulateEnvelopeRejectsInvalidEnvelopes(t *testing.T) {
	t.Parallel()

	sourceAcct := xdr.MustAddress(stellartest.SourceAccount)
	sacAddr, err := stellar.ParseContractID(stellartest.SACContract)
	require.NoError(t, err)

	marshalEnv := func(ops []xdr.Operation) string {
		env := xdr.TransactionEnvelope{
			Type: xdr.EnvelopeTypeEnvelopeTypeTx,
			V1: &xdr.TransactionV1Envelope{
				Tx: xdr.Transaction{
					SourceAccount: sourceAcct.ToMuxedAccount(),
					Fee:           100,
					SeqNum:        1,
					Cond:          xdr.Preconditions{Type: xdr.PreconditionTypePrecondNone},
					Memo:          xdr.Memo{Type: xdr.MemoTypeMemoNone},
					Operations:    ops,
				},
			},
		}
		b64, marshalErr := xdr.MarshalBase64(env)
		require.NoError(t, marshalErr)
		return b64
	}

	invokeOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
					InvokeContract: &xdr.InvokeContractArgs{
						ContractAddress: sacAddr,
						FunctionName:    xdr.ScSymbol("decimals"),
					},
				},
			},
		},
	}

	paymentOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypePayment,
			PaymentOp: &xdr.PaymentOp{
				Destination: sourceAcct.ToMuxedAccount(),
				Asset:       xdr.Asset{Type: xdr.AssetTypeAssetTypeNative},
				Amount:      1000,
			},
		},
	}

	wasmBytes := []byte{0x00, 0x61, 0x73, 0x6d}
	uploadOp := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: xdr.HostFunction{
					Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm,
					Wasm: &wasmBytes,
				},
			},
		},
	}

	extendEnv, err := stellar.BuildExtendFootprintTx(stellar.ExtendParams{
		SourceAccount: stellartest.SourceAccount,
		Key:           stellar.InstanceKey(sacAddr),
		ExtendTo:      5000000,
	})
	require.NoError(t, err)

	tests := []struct {
		name string
		req  probe.SimulateRequest
		want string
	}{
		{
			name: "malformed base64 xdr",
			req:  probe.SimulateRequest{Envelope: "not-valid-base64-xdr!!!"},
			want: "decode base64 XDR",
		},
		{
			name: "zero operations",
			req:  probe.SimulateRequest{Envelope: marshalEnv(nil)},
			want: "no operations",
		},
		{
			name: "multi-operation transaction",
			req:  probe.SimulateRequest{Envelope: marshalEnv([]xdr.Operation{invokeOp, invokeOp})},
			want: "multi-operation transactions are not supported (got 2 operations)",
		},
		{
			name: "classic non-Soroban payment operation",
			req:  probe.SimulateRequest{Envelope: marshalEnv([]xdr.Operation{paymentOp})},
			want: "non-Soroban operation OperationTypePayment",
		},
		{
			name: "footprint extend operation",
			req:  probe.SimulateRequest{Envelope: extendEnv},
			want: "OperationTypeExtendFootprintTtl (ledger footprint maintenance), not a contract invocation",
		},
		{
			name: "upload wasm host function",
			req:  probe.SimulateRequest{Envelope: marshalEnv([]xdr.Operation{uploadOp})},
			want: "HostFunctionTypeHostFunctionTypeUploadContractWasm, not InvokeContract",
		},
		{
			name: "both contract call and envelope",
			req: probe.SimulateRequest{
				ContractID: stellartest.SACContract,
				Function:   "decimals",
				Envelope:   marshalEnv([]xdr.Operation{invokeOp}),
			},
			want: "not both",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fake := stellartest.NewFake(t)
			p, newErr := probe.New(probe.Options{
				Client:        fake,
				SourceAccount: stellartest.SourceAccount,
			})
			require.NoError(t, newErr)

			_, simErr := p.Simulate(context.Background(), tt.req)
			require.ErrorIs(t, simErr, probe.ErrInvalidEnvelope)
			assert.Contains(t, simErr.Error(), tt.want)
			assert.Empty(t, fake.SimulateRequests, "invalid envelope must be rejected before reaching RPC")
		})
	}
}

package probe_test

import (
	"context"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/abi/abitest"
	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func errorEnumInterface() *abi.Interface {
	return &abi.Interface{Types: []abi.UDT{
		{Kind: abi.UDTEnum, Name: "Shade", Cases: []abi.Case{{Name: "Red", Value: 3}}},
		{Kind: abi.UDTErrorEnum, Name: "TokenError", Cases: []abi.Case{
			{Name: "NotInitialized", Value: 1},
			{Name: "InsufficientBalance", Value: 3, Doc: "The sender holds less than the amount."},
		}},
	}}
}

func TestParseErrorHost(t *testing.T) {
	got := probe.ParseError("HostError: Error(Storage, MissingValue)\n\nEvent log ...")
	require.NotNil(t, got)
	assert.Equal(t, probe.ErrorKindHost, got.Kind)
	assert.Equal(t, "Error(Storage, MissingValue)", got.Raw)
	assert.Equal(t, "Storage", got.Type)
	assert.Equal(t, "MissingValue", got.Name)
	assert.Contains(t, got.Description, "ledger entry")
	assert.Contains(t, got.Description, "required but not provided")
	assert.Nil(t, got.Code)
	assert.Empty(t, got.Note)
}

func TestParseErrorUnknownHostCode(t *testing.T) {
	got := probe.ParseError("Error(Frobnicate, Wobble)")
	require.NotNil(t, got)
	assert.Equal(t, probe.ErrorKindHost, got.Kind)
	assert.Empty(t, got.Description)
	assert.Equal(t, "unrecognized host error type and code", got.Note)
}

func TestParseErrorNoError(t *testing.T) {
	assert.Nil(t, probe.ParseError(""))
	assert.Nil(t, probe.ParseError("transaction simulation failed: bad envelope"))
}

func TestExplainErrorNamesContractError(t *testing.T) {
	got := probe.ExplainError("HostError: Error(Contract, #3)", errorEnumInterface())
	require.NotNil(t, got)
	assert.Equal(t, probe.ErrorKindContract, got.Kind)
	require.NotNil(t, got.Code)
	assert.Equal(t, uint32(3), *got.Code)
	// The plain enum Shade also has a case 3; only the error enum counts.
	assert.Equal(t, "TokenError", got.Enum)
	assert.Equal(t, "InsufficientBalance", got.Name)
	assert.Equal(t, "The sender holds less than the amount.", got.Doc)
	assert.Empty(t, got.Note)
}

func TestExplainErrorCodeNotInEnum(t *testing.T) {
	got := probe.ExplainError("Error(Contract, #42)", errorEnumInterface())
	require.NotNil(t, got)
	require.NotNil(t, got.Code)
	assert.Equal(t, uint32(42), *got.Code)
	assert.Empty(t, got.Name)
	assert.Equal(t, "code 42 is not a case of the contract's error enum", got.Note)
}

func TestExplainErrorWithoutEnumOrInterface(t *testing.T) {
	got := probe.ExplainError("Error(Contract, #1)", &abi.Interface{})
	require.NotNil(t, got)
	assert.Equal(t, "the contract declares no error enum, so the code cannot be named", got.Note)

	got = probe.ExplainError("Error(Contract, #1)", nil)
	require.NotNil(t, got)
	assert.Contains(t, got.Note, "interface is not available")
}

func TestExplainErrorLeavesHostErrorsAlone(t *testing.T) {
	got := probe.ExplainError("Error(Auth, InvalidAction)", errorEnumInterface())
	require.NotNil(t, got)
	assert.Equal(t, probe.ErrorKindHost, got.Kind)
	assert.Empty(t, got.Enum)
	assert.Contains(t, got.Description, "authorization failed")
}

func TestParseWasmKeepsErrorCaseDocs(t *testing.T) {
	wasm := abitest.Wasm(t, abitest.ErrorEnum("TokenError",
		abitest.ErrorCase("InsufficientBalance", 3, "The sender holds less than the amount.")))
	iface, err := abi.ParseWasm(wasm)
	require.NoError(t, err)
	require.Len(t, iface.Types, 1)
	assert.Equal(t, "The sender holds less than the amount.", iface.Types[0].Cases[0].Doc)
}

func TestSimulateExplainsHostError(t *testing.T) {
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.Thresholds{})

	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "no_such_fn",
	})
	require.NoError(t, err)
	require.False(t, res.Success)
	require.NotNil(t, res.ErrorExplanation)
	assert.Equal(t, probe.ErrorKindHost, res.ErrorExplanation.Kind)
	assert.Equal(t, "Error(Value, InvalidInput)", res.ErrorExplanation.Raw)
	assert.Contains(t, res.ErrorExplanation.Description, "unknown function name")
}

func TestSimulateNamesContractErrorFromChain(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.DeployWasm(t, stellartest.UndeployedContract, abitest.Wasm(t, abitest.ErrorEnum("TokenError",
		abitest.ErrorCase("InsufficientBalance", 3, "The sender holds less than the amount."))))
	fake.Simulations["withdraw"] = protocol.SimulateTransactionResponse{
		Error:        "HostError: Error(Contract, #3)\n\nEvent log (newest first): ...",
		LatestLedger: fake.LatestLedger,
	}
	p := newProber(t, fake, health.Thresholds{})

	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.UndeployedContract,
		Function:   "withdraw",
	})
	require.NoError(t, err)
	require.NotNil(t, res.ErrorExplanation)
	assert.Equal(t, "TokenError", res.ErrorExplanation.Enum)
	assert.Equal(t, "InsufficientBalance", res.ErrorExplanation.Name)
	assert.Equal(t, "The sender holds less than the amount.", res.ErrorExplanation.Doc)
}

func TestSimulateContractErrorWithoutInterface(t *testing.T) {
	fake := stellartest.NewFake(t)
	// The SAC has no Wasm, so its interface cannot be read.
	fake.Simulations["burn"] = protocol.SimulateTransactionResponse{Error: "HostError: Error(Contract, #10)"}
	p := newProber(t, fake, health.Thresholds{})

	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "burn",
	})
	require.NoError(t, err)
	require.NotNil(t, res.ErrorExplanation)
	require.NotNil(t, res.ErrorExplanation.Code)
	assert.Equal(t, uint32(10), *res.ErrorExplanation.Code)
	assert.Empty(t, res.ErrorExplanation.Name)
	assert.Contains(t, res.ErrorExplanation.Note, "could not be read")
}

func TestSimulateSuccessHasNoExplanation(t *testing.T) {
	fake := stellartest.NewFake(t)
	p := newProber(t, fake, health.Thresholds{})
	res, err := p.Simulate(context.Background(), probe.SimulateRequest{
		ContractID: stellartest.SACContract,
		Function:   "decimals",
	})
	require.NoError(t, err)
	require.True(t, res.Success)
	assert.Nil(t, res.ErrorExplanation)
}

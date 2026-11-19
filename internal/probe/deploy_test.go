package probe_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi/abitest"
	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

// deployFake is a fake that prices both deployment steps with the recorded
// successful simulation.
func deployFake(t *testing.T) *stellartest.Fake {
	t.Helper()
	fake := stellartest.NewFake(t)
	fake.Simulations[stellartest.UploadContractWasm] = fake.Simulations["decimals"]
	fake.Simulations[stellartest.CreateContract] = fake.Simulations["decimals"]
	return fake
}

func constructorWasm(t *testing.T) []byte {
	t.Helper()
	return abitest.Wasm(t,
		abitest.Fn("__constructor", nil, abitest.Input("admin", abitest.Address), abitest.Input("cap", abitest.U32)),
		abitest.ErrorEnum("InitError", abitest.ErrorCase("CapTooLow", 1, "The cap must be at least one.")),
	)
}

func envelopeOf(t *testing.T, raw string) xdr.TransactionEnvelope {
	t.Helper()
	var env xdr.TransactionEnvelope
	require.NoError(t, xdr.SafeUnmarshalBase64(raw, &env))
	return env
}

func TestEstimateDeployPricesBothSteps(t *testing.T) {
	fake := deployFake(t)
	p := newProber(t, fake, health.Thresholds{})
	wasm := constructorWasm(t)
	salt := xdr.Uint256{7}

	res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{
		Wasm:            wasm,
		ConstructorArgs: []string{stellartest.SourceAccount, "10"},
		Salt:            &salt,
	})
	require.NoError(t, err)

	sum := sha256.Sum256(wasm)
	assert.Equal(t, hex.EncodeToString(sum[:]), res.WasmHash)
	assert.Equal(t, len(wasm), res.WasmSize)
	assert.False(t, res.AlreadyUploaded)
	assert.Equal(t, stellartest.SourceAccount, res.Deployer)
	assert.Equal(t, hex.EncodeToString(salt[:]), res.Salt)
	assert.Equal(t, "fn __constructor(admin: address, cap: u32)", res.Constructor)
	assert.Equal(t, []string{"address:" + stellartest.SourceAccount, "u32:10"}, res.ConstructorArgs)

	require.True(t, res.Upload.Simulated)
	require.True(t, res.Upload.Success)
	require.True(t, res.Instantiate.Simulated)
	require.True(t, res.Instantiate.Success)
	assert.Positive(t, res.Upload.Cost.ResourceFee)
	assert.Equal(t, res.Upload.Cost.EstimatedTotalFee+res.Instantiate.Cost.EstimatedTotalFee, res.TotalEstimatedFee)

	// The upload carries the Wasm and the instantiation the typed constructor
	// arguments and the salt, against the uploaded hash.
	assert.Equal(t, []string{stellartest.UploadContractWasm, stellartest.CreateContract}, fake.Functions)
	upload := envelopeOf(t, fake.SimulateRequests[0].Transaction)
	assert.Equal(t, wasm, upload.Operations()[0].Body.InvokeHostFunctionOp.HostFunction.MustWasm())
	create := envelopeOf(t, fake.SimulateRequests[1].Transaction).Operations()[0].Body.InvokeHostFunctionOp.HostFunction.MustCreateContractV2()
	assert.Equal(t, sum[:], create.Executable.WasmHash[:])
	assert.Equal(t, salt, create.ContractIdPreimage.FromAddress.Salt)
	require.Len(t, create.ConstructorArgs, 2)
	assert.Equal(t, xdr.ScValTypeScvU32, create.ConstructorArgs[1].Type)

	// The predicted address is the one the network would derive.
	preimage, err := stellar.DeployPreimage(stellartest.SourceAccount, salt)
	require.NoError(t, err)
	want, err := stellar.DeployedContractID("Test SDF Network ; September 2015", preimage)
	require.NoError(t, err)
	assert.Equal(t, want, res.ContractID)
}

func TestEstimateDeployRandomSaltDiffers(t *testing.T) {
	p := newProber(t, deployFake(t), health.Thresholds{})
	wasm := constructorWasm(t)
	req := probe.DeployEstimateRequest{Wasm: wasm, ConstructorArgs: []string{stellartest.SourceAccount, "1"}}

	a, err := p.EstimateDeploy(context.Background(), req)
	require.NoError(t, err)
	b, err := p.EstimateDeploy(context.Background(), req)
	require.NoError(t, err)
	assert.NotEqual(t, a.Salt, b.Salt)
	assert.NotEqual(t, a.ContractID, b.ContractID)
}

func TestEstimateDeployDetectsWasmOnChain(t *testing.T) {
	fake := deployFake(t)
	wasm := constructorWasm(t)
	fake.DeployWasm(t, stellartest.UndeployedContract, wasm)
	p := newProber(t, fake, health.Thresholds{})

	res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{
		Wasm:            wasm,
		ConstructorArgs: []string{stellartest.SourceAccount, "1"},
	})
	require.NoError(t, err)
	assert.True(t, res.AlreadyUploaded)
	assert.False(t, res.Upload.Simulated)
	assert.Zero(t, res.Upload.Cost)
	assert.Contains(t, res.Upload.Note, "already on chain")
	assert.Equal(t, res.Instantiate.Cost.EstimatedTotalFee, res.TotalEstimatedFee)
	// Only the instantiation was simulated.
	assert.Equal(t, []string{stellartest.CreateContract}, fake.Functions)
}

func TestEstimateDeployChecksSizeLimit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		limit uint32
		fits  bool
	}{
		{"fits", 1 << 20, true},
		{"too large", 8, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := deployFake(t)
			limit := xdr.Uint32(tc.limit)
			fake.StoreEntry(t, stellar.ConfigSettingKey(xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes), xdr.LedgerEntryData{
				Type: xdr.LedgerEntryTypeConfigSetting,
				ConfigSetting: &xdr.ConfigSettingEntry{
					ConfigSettingId:      xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes,
					ContractMaxSizeBytes: &limit,
				},
			})
			p := newProber(t, fake, health.Thresholds{})
			res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{Wasm: abitest.Wasm(t)})
			require.NoError(t, err)
			assert.Equal(t, tc.limit, res.MaxWasmSize)
			require.NotNil(t, res.FitsSizeLimit)
			assert.Equal(t, tc.fits, *res.FitsSizeLimit)
		})
	}
}

func TestEstimateDeployUnknownSizeLimit(t *testing.T) {
	p := newProber(t, deployFake(t), health.Thresholds{})
	res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{Wasm: abitest.Wasm(t)})
	require.NoError(t, err)
	assert.Zero(t, res.MaxWasmSize)
	assert.Nil(t, res.FitsSizeLimit)
}

func TestEstimateDeployInstantiateNeedsUploadFirst(t *testing.T) {
	fake := deployFake(t)
	fake.Simulations[stellartest.CreateContract] = protocol.SimulateTransactionResponse{
		Error: "HostError: Error(Storage, MissingValue)",
	}
	p := newProber(t, fake, health.Thresholds{})
	res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{Wasm: abitest.Wasm(t)})
	require.NoError(t, err)
	assert.True(t, res.Upload.Success)
	assert.False(t, res.Instantiate.Success)
	assert.Contains(t, res.Instantiate.Note, "only be priced exactly after the upload")
	require.NotNil(t, res.Instantiate.ErrorExplanation)
	assert.Equal(t, probe.ErrorKindHost, res.Instantiate.ErrorExplanation.Kind)
}

func TestEstimateDeployNamesConstructorError(t *testing.T) {
	fake := deployFake(t)
	fake.Simulations[stellartest.CreateContract] = protocol.SimulateTransactionResponse{
		Error: "HostError: Error(Contract, #1)",
	}
	p := newProber(t, fake, health.Thresholds{})
	res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{
		Wasm:            constructorWasm(t),
		ConstructorArgs: []string{stellartest.SourceAccount, "0"},
	})
	require.NoError(t, err)
	require.NotNil(t, res.Instantiate.ErrorExplanation)
	assert.Equal(t, "InitError", res.Instantiate.ErrorExplanation.Enum)
	assert.Equal(t, "CapTooLow", res.Instantiate.ErrorExplanation.Name)
}

func TestEstimateDeployArgumentErrors(t *testing.T) {
	p := newProber(t, deployFake(t), health.Thresholds{})
	ctx := context.Background()

	_, err := p.EstimateDeploy(ctx, probe.DeployEstimateRequest{})
	require.Error(t, err)

	// A wasm without a constructor takes no arguments.
	_, err = p.EstimateDeploy(ctx, probe.DeployEstimateRequest{Wasm: abitest.Wasm(t), ConstructorArgs: []string{"1"}})
	require.ErrorContains(t, err, "declares no constructor")

	// The constructor's arity is checked before anything is simulated.
	_, err = p.EstimateDeploy(ctx, probe.DeployEstimateRequest{Wasm: constructorWasm(t), ConstructorArgs: []string{"1"}})
	require.Error(t, err)

	_, err = p.EstimateDeploy(ctx, probe.DeployEstimateRequest{Wasm: abitest.Wasm(t), Deployer: "not-an-account"})
	require.ErrorContains(t, err, "not an account address")
}

func TestEstimateDeployInfersArgsWithoutInterface(t *testing.T) {
	p := newProber(t, deployFake(t), health.Thresholds{})
	res, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{
		Wasm:            []byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00},
		ConstructorArgs: []string{"u32:5"},
	})
	require.NoError(t, err)
	assert.Contains(t, res.ABINote, "inferred")
	assert.Equal(t, []string{"u32:5"}, res.ConstructorArgs)
}

func TestEstimateDeployNeverSubmits(t *testing.T) {
	// The stellar.Client interface has no way to submit; what reaches the
	// network is simulations and reads only.
	fake := deployFake(t)
	p := newProber(t, fake, health.Thresholds{})
	_, err := p.EstimateDeploy(context.Background(), probe.DeployEstimateRequest{Wasm: abitest.Wasm(t)})
	require.NoError(t, err)
	assert.Len(t, fake.SimulateRequests, 2)
}

package probe

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar"
)

// constructorName is the function a contract's constructor is exported as.
const constructorName = "__constructor"

// DeployEstimateRequest describes a deployment to price.
type DeployEstimateRequest struct {
	// Wasm is the contract module to deploy.
	Wasm []byte
	// Deployer is the account ("G...") that would deploy the contract. It
	// determines the new contract's address. Empty means the source account
	// the Prober builds every simulation with.
	Deployer string
	// ConstructorArgs are the constructor's arguments in the scval syntax.
	// They are typed from the Wasm's own interface when it declares a
	// constructor.
	ConstructorArgs []string
	// Salt fixes the deployment salt. Nil means a random one, which is
	// what a fresh deploy uses.
	Salt *xdr.Uint256
}

// DeployStep is the estimate for one of the two deployment transactions.
type DeployStep struct {
	// Simulated is false when the step was not simulated, as for the
	// upload of a Wasm already on chain.
	Simulated bool `json:"simulated"`
	// Success is true when the step would succeed.
	Success bool `json:"success"`
	// Error carries the simulation failure, if any.
	Error string `json:"error,omitempty"`
	// ErrorExplanation names the error in Error.
	ErrorExplanation *ErrorExplanation `json:"error_explanation,omitempty"`
	// Cost is what the step would cost. It is all zero for a skipped upload.
	Cost Cost `json:"cost"`
	// Note explains a skipped step or a result that needs context.
	Note string `json:"note,omitempty"`
}

// DeployEstimate is what deploying a Wasm would cost. Nothing is submitted.
type DeployEstimate struct {
	// WasmHash is the hex SHA-256 of the module: the key its code entry is
	// stored under.
	WasmHash string `json:"wasm_hash"`
	// WasmSize is the module's size in bytes.
	WasmSize int `json:"wasm_size_bytes"`
	// MaxWasmSize is the network's contract size limit, when it could be
	// read. Zero means unknown.
	MaxWasmSize uint32 `json:"max_wasm_size_bytes,omitempty"`
	// FitsSizeLimit is false when the module is larger than MaxWasmSize. It
	// is nil when the limit could not be read.
	FitsSizeLimit *bool `json:"fits_size_limit,omitempty"`
	// AlreadyUploaded is true when the network already stores this Wasm,
	// so the upload costs nothing.
	AlreadyUploaded bool `json:"already_uploaded"`

	// Deployer and Salt are what the new contract's address derives from.
	Deployer string `json:"deployer"`
	Salt     string `json:"salt"`
	// ContractID is the address the contract would get. A real deploy from
	// another account or with another salt gets a different one.
	ContractID string `json:"contract_id"`
	// Constructor is the constructor's signature, when the Wasm declares one.
	Constructor string `json:"constructor,omitempty"`
	// ConstructorArgs are the arguments as encoded, after typing.
	ConstructorArgs []string `json:"constructor_args,omitempty"`
	// ABINote explains why the constructor's arguments were inferred.
	ABINote string `json:"abi_note,omitempty"`

	Upload      DeployStep `json:"upload"`
	Instantiate DeployStep `json:"instantiate"`
	// TotalEstimatedFee is the sum of both steps' estimated total fees.
	TotalEstimatedFee int64 `json:"total_estimated_fee_stroops"`

	// LatestLedger is the ledger the estimate was taken at.
	LatestLedger uint32 `json:"latest_ledger"`
}

// EstimateDeploy simulates uploading a Wasm and instantiating it, and reports
// the resources and fees each would take. It never submits either.
//
// A Wasm already on chain is detected by its code entry, and its upload is
// reported at zero rather than simulated. The instantiation can only run
// against code the network already has, so for a new Wasm it is simulated
// anyway and, when it fails for that reason, the note says so.
func (p *Prober) EstimateDeploy(ctx context.Context, req DeployEstimateRequest) (*DeployEstimate, error) {
	if len(req.Wasm) == 0 {
		return nil, errors.New("the wasm is empty")
	}
	deployer := req.Deployer
	if deployer == "" {
		deployer = p.source
	}
	salt := req.Salt
	if salt == nil {
		var random xdr.Uint256
		if _, err := rand.Read(random[:]); err != nil {
			return nil, fmt.Errorf("generate salt: %w", err)
		}
		salt = &random
	}

	hash := xdr.Hash(sha256.Sum256(req.Wasm))
	out := &DeployEstimate{
		WasmHash: hex.EncodeToString(hash[:]),
		WasmSize: len(req.Wasm),
		Deployer: deployer,
		Salt:     hex.EncodeToString(salt[:]),
	}

	iface, ifaceErr := abi.ParseWasm(req.Wasm)
	args, err := p.typeConstructorArgs(req.ConstructorArgs, iface, ifaceErr, out)
	if err != nil {
		return nil, err
	}

	preimage, err := stellar.DeployPreimage(deployer, *salt)
	if err != nil {
		return nil, err
	}
	network, err := p.client.GetNetwork(ctx)
	if err != nil {
		return nil, err
	}
	out.ContractID, err = stellar.DeployedContractID(network.Passphrase, preimage)
	if err != nil {
		return nil, err
	}

	if err := p.readCodeAndLimit(ctx, hash, out); err != nil {
		return nil, err
	}

	if out.AlreadyUploaded {
		out.Upload = DeployStep{Success: true, Note: "this wasm is already on chain, so uploading it costs nothing"}
	} else {
		out.Upload, err = p.simulateStep(ctx, deployer, stellar.UploadWasmFunction(req.Wasm), nil)
		if err != nil {
			return nil, err
		}
	}

	out.Instantiate, err = p.simulateStep(ctx, deployer, stellar.CreateContractFunction(preimage, hash, args), iface)
	if err != nil {
		return nil, err
	}
	if !out.Instantiate.Success && !out.AlreadyUploaded {
		out.Instantiate.Note = "the wasm is not on chain yet, and instantiating needs it there: " +
			"this step can only be priced exactly after the upload"
	}

	out.TotalEstimatedFee = out.Upload.Cost.EstimatedTotalFee + out.Instantiate.Cost.EstimatedTotalFee
	return out, nil
}

// typeConstructorArgs types the constructor's arguments from the Wasm's own
// interface and encodes them.
func (p *Prober) typeConstructorArgs(specs []string, iface *abi.Interface, ifaceErr error, out *DeployEstimate) ([]xdr.ScVal, error) {
	var err error
	switch {
	case ifaceErr != nil && len(specs) > 0:
		out.ABINote = "the wasm has no readable interface; constructor argument types were inferred: " + ifaceErr.Error()
	case ifaceErr != nil:
		// No interface and nothing to type: there is nothing to report.
	default:
		fn, lookupErr := iface.Lookup(constructorName)
		if lookupErr != nil {
			if len(specs) > 0 {
				return nil, fmt.Errorf("the contract declares no constructor, but %d argument(s) were given", len(specs))
			}
			break
		}
		out.Constructor = fn.Signature()
		typer, ok := p.codec.(interface{ HasType(string) bool })
		if ok {
			specs, err = abi.Annotate(fn, specs, typer.HasType)
			if err != nil {
				return nil, fmt.Errorf("constructor: %w", err)
			}
		}
	}
	if len(specs) > 0 {
		out.ConstructorArgs = specs
	}
	return p.codec.EncodeAll(specs)
}

// readCodeAndLimit reports whether the Wasm is already on chain and whether
// it fits the network's contract size limit, in one ledger read.
func (p *Prober) readCodeAndLimit(ctx context.Context, hash xdr.Hash, out *DeployEstimate) error {
	codeKey, err := stellar.EncodeKey(stellar.CodeKey(hash))
	if err != nil {
		return err
	}
	limitKey, err := stellar.EncodeKey(stellar.ConfigSettingKey(xdr.ConfigSettingIdConfigSettingContractMaxSizeBytes))
	if err != nil {
		return err
	}
	resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{codeKey, limitKey}})
	if err != nil {
		return err
	}
	out.LatestLedger = resp.LatestLedger
	for _, entry := range resp.Entries {
		switch entry.KeyXDR {
		case codeKey:
			out.AlreadyUploaded = true
		case limitKey:
			var data xdr.LedgerEntryData
			if err := xdr.SafeUnmarshalBase64(entry.DataXDR, &data); err != nil {
				p.log.Debug("could not decode the contract size limit", "err", err)
				continue
			}
			if setting, ok := data.GetConfigSetting(); ok {
				if limit, ok := setting.GetContractMaxSizeBytes(); ok {
					out.MaxWasmSize = uint32(limit)
					fits := out.WasmSize <= int(limit)
					out.FitsSizeLimit = &fits
				}
			}
		}
	}
	return nil
}

// simulateStep simulates one deployment transaction. A contract error, which
// only the constructor can raise, is named from iface: the Wasm's own
// interface, since the contract is not on chain to read it from.
func (p *Prober) simulateStep(ctx context.Context, source string, fn xdr.HostFunction, iface *abi.Interface) (DeployStep, error) {
	envelope, err := stellar.BuildHostFunctionTx(source, fn)
	if err != nil {
		return DeployStep{}, err
	}
	resp, err := p.client.SimulateTransaction(ctx, protocol.SimulateTransactionRequest{Transaction: envelope})
	if err != nil {
		return DeployStep{}, err
	}
	built, err := p.buildSimulateResult(SimulateRequest{}, resp)
	if err != nil {
		return DeployStep{}, err
	}
	step := DeployStep{Simulated: true, Success: built.Success, Error: built.Error, Cost: built.Cost}
	if !step.Success {
		step.ErrorExplanation = ExplainError(step.Error, iface)
	}
	return step, nil
}

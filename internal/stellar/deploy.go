package stellar

import (
	"crypto/sha256"
	"fmt"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/txnbuild"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// UploadWasmFunction returns the host function that uploads wasm.
func UploadWasmFunction(wasm []byte) xdr.HostFunction {
	return xdr.HostFunction{Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm, Wasm: &wasm}
}

// DeployPreimage is the preimage a contract deployed by deployer with salt
// derives its address from.
func DeployPreimage(deployer string, salt xdr.Uint256) (xdr.ContractIdPreimage, error) {
	var id xdr.AccountId
	if err := id.SetAddress(deployer); err != nil {
		return xdr.ContractIdPreimage{}, fmt.Errorf("deployer %q is not an account address: %w", deployer, err)
	}
	return xdr.ContractIdPreimage{
		Type: xdr.ContractIdPreimageTypeContractIdPreimageFromAddress,
		FromAddress: &xdr.ContractIdPreimageFromAddress{
			Address: xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeAccount, AccountId: &id},
			Salt:    salt,
		},
	}, nil
}

// CreateContractFunction returns the host function that instantiates the
// uploaded Wasm with the given hash, passing args to its constructor.
func CreateContractFunction(preimage xdr.ContractIdPreimage, wasmHash xdr.Hash, args []xdr.ScVal) xdr.HostFunction {
	if args == nil {
		args = []xdr.ScVal{}
	}
	hash := wasmHash
	return xdr.HostFunction{
		Type: xdr.HostFunctionTypeHostFunctionTypeCreateContractV2,
		CreateContractV2: &xdr.CreateContractArgsV2{
			ContractIdPreimage: preimage,
			Executable: xdr.ContractExecutable{
				Type:     xdr.ContractExecutableTypeContractExecutableWasm,
				WasmHash: &hash,
			},
			ConstructorArgs: args,
		},
	}
}

// DeployedContractID is the address a contract created from preimage gets on
// the network with the given passphrase.
func DeployedContractID(passphrase string, preimage xdr.ContractIdPreimage) (string, error) {
	networkID := xdr.Hash(sha256.Sum256([]byte(passphrase)))
	full := xdr.HashIdPreimage{
		Type:       xdr.EnvelopeTypeEnvelopeTypeContractId,
		ContractId: &xdr.HashIdPreimageContractId{NetworkId: networkID, ContractIdPreimage: preimage},
	}
	raw, err := full.MarshalBinary()
	if err != nil {
		return "", fmt.Errorf("encode contract id preimage: %w", err)
	}
	id := sha256.Sum256(raw)
	return strkey.Encode(strkey.VersionByteContract, id[:])
}

// BuildHostFunctionTx assembles an unsigned transaction envelope holding a
// single InvokeHostFunction operation for fn, serialized as base64 for
// simulateTransaction. Like BuildInvocationTx it is never signed or
// submitted, so a public key is all the source needs to be.
func BuildHostFunctionTx(sourceAccount string, fn xdr.HostFunction) (string, error) {
	if sourceAccount == "" {
		return "", fmt.Errorf("source account is required to build a transaction to simulate")
	}
	account := txnbuild.NewSimpleAccount(sourceAccount, 0)
	tx, err := txnbuild.NewTransaction(txnbuild.TransactionParams{
		SourceAccount:        &account,
		IncrementSequenceNum: true,
		Operations:           []txnbuild.Operation{&txnbuild.InvokeHostFunction{HostFunction: fn, SourceAccount: sourceAccount}},
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

// ConfigSettingKey is the ledger key of a network configuration setting.
func ConfigSettingKey(id xdr.ConfigSettingId) xdr.LedgerKey {
	return xdr.LedgerKey{
		Type:          xdr.LedgerEntryTypeConfigSetting,
		ConfigSetting: &xdr.LedgerKeyConfigSetting{ConfigSettingId: id},
	}
}

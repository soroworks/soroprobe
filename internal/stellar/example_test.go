package stellar_test

import (
	"fmt"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// ExampleInstanceKey builds the ledger key for a contract's instance entry,
// which is the persistent ContractData entry keyed by
// ScvLedgerKeyContractInstance.
func ExampleInstanceKey() {
	contract, err := stellar.ParseContractID("CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	key := stellar.InstanceKey(contract)
	fmt.Println(key.Type)
	fmt.Println(key.ContractData.Key.Type)
	fmt.Println(key.ContractData.Durability)
	// Output:
	// LedgerEntryTypeContractData
	// ScValTypeScvLedgerKeyContractInstance
	// ContractDataDurabilityPersistent
}

// ExampleBuildExtendFootprintTx builds an unsigned envelope that extends a
// contract instance's TTL, then decodes it to show what it holds.
func ExampleBuildExtendFootprintTx() {
	contract, err := stellar.ParseContractID("CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC")
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	envelope, err := stellar.BuildExtendFootprintTx(stellar.ExtendParams{
		SourceAccount: "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		Key:           stellar.InstanceKey(contract),
		ExtendTo:      100_000,
	})
	if err != nil {
		fmt.Println("error:", err)
		return
	}

	var tx xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(envelope, &tx); err != nil {
		fmt.Println("error:", err)
		return
	}
	op := tx.Operations()[0].Body
	footprint := tx.V1.Tx.Ext.SorobanData.Resources.Footprint
	fmt.Println(op.Type)
	fmt.Println("extend to:", op.ExtendFootprintTtlOp.ExtendTo)
	fmt.Println("read-only entries:", len(footprint.ReadOnly))
	fmt.Println("read-write entries:", len(footprint.ReadWrite))
	// Output:
	// OperationTypeExtendFootprintTtl
	// extend to: 100000
	// read-only entries: 1
	// read-write entries: 0
}

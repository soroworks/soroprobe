package xdrjson_test

import (
	"encoding/json"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/scval"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
	"github.com/soroworks/soroprobe/internal/xdrjson"
)

const account = "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF"

func roundTrip(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func TestDecodeScVal(t *testing.T) {
	b64, err := xdrjson.EncodeScVal(nil, "u32:7")
	require.NoError(t, err)

	got, err := xdrjson.Decode("scval", b64)
	require.NoError(t, err)
	assert.Equal(t, "scval", got.Type)
	assert.Equal(t, uint32(7), got.Value)
}

func TestEncodeScValRoundTripsCollections(t *testing.T) {
	spec := `vec:["sym:Balance","addr:` + account + `"]`
	b64, err := xdrjson.EncodeScVal(scval.NewRegistry(), spec)
	require.NoError(t, err)

	var v xdr.ScVal
	require.NoError(t, xdr.SafeUnmarshalBase64(b64, &v))
	assert.Equal(t, xdr.ScValTypeScvVec, v.Type)
	require.Len(t, **v.Vec, 2)
}

func TestEncodeScValRejectsBadSpec(t *testing.T) {
	_, err := xdrjson.EncodeScVal(nil, "u32:not-a-number")
	require.Error(t, err)
}

func TestDecodeEnvelopeRendersReadably(t *testing.T) {
	contract, err := stellar.ParseContractID(stellartest.WasmContract)
	require.NoError(t, err)
	env, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: account,
		Contract:      contract,
		Function:      "transfer",
		Args:          []xdr.ScVal{{Type: xdr.ScValTypeScvBool, B: ptr(true)}},
	})
	require.NoError(t, err)

	got, err := xdrjson.Decode("envelope", env)
	require.NoError(t, err)
	assert.Equal(t, "envelope", got.Type)

	raw, err := json.Marshal(got.Value)
	require.NoError(t, err)
	text := string(raw)
	// Accounts and contracts are strkeys, enums are named, ScVals use the
	// scval notation and no nil union arm leaks into the output.
	assert.Contains(t, text, `"`+account+`"`)
	assert.Contains(t, text, `"`+stellartest.WasmContract+`"`)
	assert.Contains(t, text, `"EnvelopeTypeEnvelopeTypeTx"`)
	assert.Contains(t, text, `"function_name":"transfer"`)
	assert.Contains(t, text, `"args":[true]`)
	assert.NotContains(t, text, "null")
}

func TestDecodeLedgerKeyAndEntryData(t *testing.T) {
	contract, err := stellar.ParseContractID(stellartest.WasmContract)
	require.NoError(t, err)
	key, err := stellar.EncodeKey(stellar.InstanceKey(contract))
	require.NoError(t, err)

	got, err := xdrjson.Decode("ledger-key", key)
	require.NoError(t, err)
	value := roundTrip(t, got.Value)
	assert.Equal(t, "LedgerEntryTypeContractData", value["type"])

	hash := xdr.Hash{0xab, 0xcd}
	data := xdr.LedgerEntryData{
		Type:         xdr.LedgerEntryTypeContractCode,
		ContractCode: &xdr.ContractCodeEntry{Hash: hash, Code: []byte{0, 'a', 's', 'm'}},
	}
	b64, err := xdr.MarshalBase64(data)
	require.NoError(t, err)
	got, err = xdrjson.Decode("ledger-entry-data", b64)
	require.NoError(t, err)
	code := roundTrip(t, got.Value)["contract_code"].(map[string]any)
	assert.Equal(t, "abcd000000000000000000000000000000000000000000000000000000000000", code["hash"])
	assert.Equal(t, "0061736d", code["code"])
}

func TestDecodeAutoDetectsUnambiguousType(t *testing.T) {
	contract, err := stellar.ParseContractID(stellartest.WasmContract)
	require.NoError(t, err)
	env, err := stellar.BuildInvocationTx(stellar.InvocationParams{
		SourceAccount: account,
		Contract:      contract,
		Function:      "decimals",
	})
	require.NoError(t, err)

	got, err := xdrjson.Decode("", env)
	require.NoError(t, err)
	assert.Equal(t, "envelope", got.Type)
}

func TestDecodeAutoRefusesToGuess(t *testing.T) {
	// Eight zero bytes are both a bool ScVal (false) and an empty v0
	// TransactionMeta, so auto-detection must not pick one.
	_, err := xdrjson.Decode("auto", "AAAAAAAAAAA=")
	require.ErrorIs(t, err, xdrjson.ErrAmbiguous)
	assert.Contains(t, err.Error(), "meta, scval")

	// Naming the type settles it.
	got, err := xdrjson.Decode("scval", "AAAAAAAAAAA=")
	require.NoError(t, err)
	assert.Equal(t, false, got.Value)
}

func TestDecodeErrors(t *testing.T) {
	_, err := xdrjson.Decode("scval", "")
	require.Error(t, err)

	_, err = xdrjson.Decode("nonsense", "AAAA")
	require.ErrorIs(t, err, xdrjson.ErrUnknownType)
	assert.Contains(t, err.Error(), "ledger-key")

	_, err = xdrjson.Decode("envelope", "!!not base64!!")
	require.Error(t, err)

	_, err = xdrjson.Decode("auto", "/////w==")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not decode as any known type")
}

func TestTypesListsEveryType(t *testing.T) {
	var names []string
	for _, ty := range xdrjson.Types() {
		names = append(names, ty[0])
		assert.NotEmpty(t, ty[1])
	}
	for _, want := range []string{"envelope", "result", "meta", "ledger-key", "ledger-entry", "scval"} {
		assert.Contains(t, names, want)
	}
}

func FuzzDecodeNeverPanics(f *testing.F) {
	f.Add("AAAAAwAAAAc=")
	f.Add("")
	f.Add("////")
	f.Fuzz(func(_ *testing.T, b64 string) {
		_, _ = xdrjson.Decode("auto", b64)
	})
}

func ptr[T any](v T) *T { return &v }

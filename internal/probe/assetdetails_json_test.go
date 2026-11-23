package probe

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAssetDetailsMarshalsAndUnmarshals(t *testing.T) {
	t.Parallel()

	found := true
	original := AssetDetails{
		Type:        AssetAlphaNum4,
		Code:        "USDC",
		Issuer:      "GDQNY3PBOJOKYZSRMK2S7LHHGWZIUISD4QORETLMXEWXBI7KFZZMKTL3",
		Admin:       "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		IssuerFound: &found,
		IssuerFlags: []string{"AUTH_REQUIRED", "AUTH_REVOCABLE", "CLAWBACK"},
		Note:        "issuer flags read from the ledger",
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded AssetDetails
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip mismatch:\noriginal: %#v\ndecoded:  %#v", original, decoded)
	}
}

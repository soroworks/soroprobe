package probe

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAuthorizedInvocationMarshalsAndUnmarshals(t *testing.T) {
	t.Parallel()

	original := AuthorizedInvocation{
		ContractID: "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		Function:   "transfer",
		Args:       []any{"GDQNY3PBOJOKYZSRMK2S7LHHGWZIUISD4QORETLMXEWXBI7KFZZMKTL3", float64(100), true},
		SubInvocations: []AuthorizedInvocation{
			{
				ContractID: "CBGU2FIKJ3NHF5T2Z5RPQXK7RVXRJZDVXFVH5TZ5K5QLAJR2ZQ2WJZ2Z",
				Function:   "approve",
				Args:       []any{"sym:allowance", float64(1)},
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded AuthorizedInvocation
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if !reflect.DeepEqual(decoded, original) {
		t.Fatalf("round trip mismatch:\noriginal: %#v\ndecoded:  %#v", original, decoded)
	}
}

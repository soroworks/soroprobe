package scval_test

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/scval"
)

// roundTrip encodes a spec and decodes the result, so the assertions read as
// the values a contract would see.
func roundTrip(t *testing.T, spec string) any {
	t.Helper()
	codec := scval.NewRegistry()
	v, err := codec.Encode(spec)
	require.NoError(t, err, spec)
	got, err := codec.Decode(v)
	require.NoError(t, err, spec)
	return got
}

func TestEncodeVec(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []any{uint32(1), uint32(2)}, roundTrip(t, `vec:["u32:1", "u32:2"]`))
	assert.Equal(t, []any{}, roundTrip(t, `vec:[]`))

	// Bare JSON values follow the bare-argument inference rules.
	assert.Equal(t, []any{"5", true, nil, "transfer"}, roundTrip(t, `vec:[5, true, null, "transfer"]`),
		"5 infers i128, which decodes as a decimal string")

	assert.Equal(t, []any{[]any{uint32(1)}, []any{}}, roundTrip(t, `vec:[["u32:1"], []]`),
		"nested arrays are nested vecs")
	assert.Equal(t, []any{[]any{uint32(7)}}, roundTrip(t, `vec:["vec:[\"u32:7\"]"]`),
		"a vec spec inside a string works too")
}

func TestEncodeVecProducesVecScVal(t *testing.T) {
	t.Parallel()
	v, err := scval.NewRegistry().Encode(`vec:["sym:a"]`)
	require.NoError(t, err)
	require.Equal(t, xdr.ScValTypeScvVec, v.Type)
	require.Len(t, **v.Vec, 1)
	assert.Equal(t, xdr.ScValTypeScvSymbol, (**v.Vec)[0].Type)
}

func TestEncodeMap(t *testing.T) {
	t.Parallel()

	assert.Equal(t,
		map[string]any{"a": uint32(1), "b": uint32(2)},
		roundTrip(t, `map:[["sym:a", "u32:1"], ["sym:b", "u32:2"]]`))

	assert.Equal(t,
		[]any{map[string]any{"key": uint32(1), "value": []any{"x"}}},
		roundTrip(t, `map:[["u32:1", ["sym:x"]]]`),
		"values may be collections")

	assert.Equal(t, map[string]any{}, roundTrip(t, `map:[]`))
}

func TestEncodeMapPreservesOrder(t *testing.T) {
	t.Parallel()
	v, err := scval.NewRegistry().Encode(`map:[["sym:b", 1], ["sym:a", 2]]`)
	require.NoError(t, err)
	entries := **v.Map
	require.Len(t, entries, 2)
	assert.Equal(t, "b", string(*entries[0].Key.Sym), "entries are not silently reordered")
}

func TestEncodeCollectionErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		spec, contains string
	}{
		{`vec:1,2`, "must be a JSON array"},
		{`vec:[1,`, "not valid JSON"},
		{`vec:[1.5]`, "not an integer"},
		{`vec:[{"a": 1}]`, "objects are not supported"},
		{`vec:["u32:-1"]`, "vec[0]"},
		{`map:[["sym:a"]]`, "[key, value] pair"},
		{`map:[["sym:a", "u32:x"]]`, "map[0] value"},
		{`map:{"a": 1}`, "must be a JSON array"},
	}
	codec := scval.NewRegistry()
	for _, tt := range tests {
		_, err := codec.Encode(tt.spec)
		require.Error(t, err, tt.spec)
		assert.Contains(t, err.Error(), tt.contains, tt.spec)
	}
}

func TestCollectionsUseCustomEncoders(t *testing.T) {
	t.Parallel()
	codec := scval.NewRegistry()
	codec.Register("answer", func(string) (xdr.ScVal, error) {
		n := xdr.Uint32(42)
		return xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &n}, nil
	})
	v, err := codec.Encode(`vec:["answer:"]`)
	require.NoError(t, err)
	got, err := codec.Decode(v)
	require.NoError(t, err)
	assert.Equal(t, []any{uint32(42)}, got)
}

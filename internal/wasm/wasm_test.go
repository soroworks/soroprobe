package wasm_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
	"github.com/soroworks/soroprobe/internal/wasm"
)

// builder assembles small modules for tests: every function has type
// () -> (), imports come first, and bodies are raw instruction bytes ending
// in 0x0b (end).
type builder struct {
	imports [][2]string
	bodies  [][]byte
	exports []struct {
		name string
		idx  uint32
	}
	custom [][2][]byte
}

func uleb(n uint32) []byte {
	var out []byte
	for {
		b := byte(n & 0x7f)
		n >>= 7
		if n != 0 {
			out = append(out, b|0x80)
			continue
		}
		return append(out, b)
	}
}

func str(s string) []byte { return append(uleb(uint32(len(s))), s...) } //nolint:gosec // test modules are tiny

func section(id byte, body []byte) []byte {
	return append(append([]byte{id}, uleb(uint32(len(body)))...), body...) //nolint:gosec // test modules are tiny
}

func vec(items ...[]byte) []byte {
	out := uleb(uint32(len(items))) //nolint:gosec // test modules are tiny
	for _, it := range items {
		out = append(out, it...)
	}
	return out
}

func (b *builder) importFn(module, name string) uint32 {
	b.imports = append(b.imports, [2]string{module, name})
	return uint32(len(b.imports) - 1) //nolint:gosec // test modules are tiny
}

// fn adds a function whose body is code (without locals or end) and
// returns its index.
func (b *builder) fn(code ...byte) uint32 {
	b.bodies = append(b.bodies, code)
	return uint32(len(b.imports) + len(b.bodies) - 1) //nolint:gosec // test modules are tiny
}

func (b *builder) export(name string, idx uint32) {
	b.exports = append(b.exports, struct {
		name string
		idx  uint32
	}{name, idx})
}

func call(idx uint32) []byte { return append([]byte{0x10}, uleb(idx)...) }

func (b *builder) build() []byte {
	out := []byte{0, 'a', 's', 'm', 1, 0, 0, 0}
	out = append(out, section(1, vec([]byte{0x60, 0x00, 0x00}))...)
	var imps [][]byte
	for _, imp := range b.imports {
		imps = append(imps, append(append(append(str(imp[0]), str(imp[1])...), 0x00), uleb(0)...))
	}
	out = append(out, section(2, vec(imps...))...)
	var funcs [][]byte
	for range b.bodies {
		funcs = append(funcs, uleb(0))
	}
	out = append(out, section(3, vec(funcs...))...)
	var exps [][]byte
	for _, e := range b.exports {
		exps = append(exps, append(append(str(e.name), 0x00), uleb(e.idx)...))
	}
	out = append(out, section(7, vec(exps...))...)
	var codes [][]byte
	for _, body := range b.bodies {
		full := append(append([]byte{0x00}, body...), 0x0b)
		codes = append(codes, append(uleb(uint32(len(full))), full...)) //nolint:gosec // test modules are tiny
	}
	out = append(out, section(10, vec(codes...))...)
	for _, c := range b.custom {
		out = append(out, section(0, append(str(string(c[0])), c[1]...))...)
	}
	return out
}

func metaSection(t *testing.T, kv ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	for i := 0; i+1 < len(kv); i += 2 {
		b, err := xdr.ScMetaEntry{Kind: xdr.ScMetaKindScMetaV0, V0: &xdr.ScMetaV0{Key: kv[i], Val: kv[i+1]}}.MarshalBinary()
		require.NoError(t, err)
		buf.Write(b)
	}
	return buf.Bytes()
}

func envSection(t *testing.T, protocol, pre uint32) []byte {
	t.Helper()
	b, err := xdr.ScEnvMetaEntry{
		Kind:             xdr.ScEnvMetaKindScEnvMetaKindInterfaceVersion,
		InterfaceVersion: &xdr.ScEnvMetaEntryInterfaceVersion{Protocol: xdr.Uint32(protocol), PreRelease: xdr.Uint32(pre)},
	}.MarshalBinary()
	require.NoError(t, err)
	return b
}

func TestParseReadsSectionsAndMetadata(t *testing.T) {
	b := &builder{}
	auth := b.importFn("a", "0")
	b.importFn("x", "1")
	f := b.fn(call(auth)...)
	b.export("hello", f)
	b.custom = append(b.custom,
		[2][]byte{[]byte("contractmetav0"), metaSection(t, "rsver", "1.84.0", "rssdkver", "22.0.0")},
		[2][]byte{[]byte("contractenvmetav0"), envSection(t, 22, 0)},
		[2][]byte{[]byte("contractspecv0"), nil},
	)
	module := b.build()

	m, err := wasm.Parse(module)
	require.NoError(t, err)
	assert.Equal(t, len(module), m.Size)
	require.Len(t, m.Imports, 2)
	assert.Equal(t, "require_auth", m.Imports[0].HostFunction)
	assert.Empty(t, m.Imports[1].HostFunction)
	assert.Equal(t, uint32(2), m.ImportedFunctions())
	assert.Equal(t, []wasm.Export{{Name: "hello", Kind: wasm.KindFunction, Index: 2}}, m.Exports)
	assert.Equal(t, "22.0.0", m.Meta["rssdkver"])
	require.NotNil(t, m.Env)
	assert.Equal(t, wasm.EnvVersion{Protocol: 22}, *m.Env)
	assert.True(t, m.HasSpec)
	assert.Equal(t, []string{"contractmetav0", "contractenvmetav0", "contractspecv0"}, m.CustomSections)
}

func TestParseRejectsMalformedModules(t *testing.T) {
	b := &builder{}
	b.export("f", b.fn(0x41, 0x01, 0x1a))
	good := b.build()
	for name, module := range map[string][]byte{
		"empty":            nil,
		"bad magic":        []byte("\x00elf\x01\x00\x00\x00"),
		"bad version":      {0, 'a', 's', 'm', 2, 0, 0, 0},
		"truncated":        good[:len(good)-1],
		"section too long": append([]byte{0, 'a', 's', 'm', 1, 0, 0, 0, 7}, uleb(1<<20)...),
		"overlong leb":     {0, 'a', 's', 'm', 1, 0, 0, 0, 7, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01},
	} {
		_, err := wasm.Parse(module)
		assert.True(t, errors.Is(err, wasm.ErrMalformed), "%s: %v", name, err)
	}
}

func FuzzParse(f *testing.F) {
	f.Add((&builder{}).build())
	b := &builder{}
	b.export("x", b.fn(0x41, 0x7f, 0x1a))
	f.Add(b.build())
	f.Fuzz(func(_ *testing.T, data []byte) {
		m, err := wasm.Parse(data)
		if err == nil {
			_, _ = m.AuthLint(nil)
		}
	})
}

func TestCallGraphDecodesCommonInstructions(t *testing.T) {
	b := &builder{}
	target := b.fn()
	// block, i32.const, br_if, loop with br_table, i64.const, a load
	// (memarg), memory.size, memory.copy (0xfc 10), then the call.
	code := []byte{
		0x02, 0x40, 0x41, 0x05, 0x0d, 0x00, 0x0b,
		0x03, 0x40, 0x41, 0x00, 0x0e, 0x02, 0x00, 0x00, 0x00, 0x0b,
		0x42, 0xff, 0xff, 0x03, 0x1a,
		0x41, 0x00, 0x28, 0x02, 0x08, 0x1a,
		0x3f, 0x00, 0x1a,
		0x41, 0x00, 0x41, 0x00, 0x41, 0x00, 0xfc, 0x0a, 0x00, 0x00,
	}
	caller := b.fn(append(code, call(target)...)...)
	b.export("caller", caller)
	m, err := wasm.Parse(b.build())
	require.NoError(t, err)
	g, err := m.CallGraph()
	require.NoError(t, err)
	assert.Equal(t, []uint32{target}, g.Calls[caller])
	assert.False(t, g.Indirect[caller])
}

func TestCallGraphRejectsUnknownOpcodes(t *testing.T) {
	b := &builder{}
	b.fn(0xfd, 0x00) // SIMD, which Soroban does not enable
	m, err := wasm.Parse(b.build())
	require.NoError(t, err)
	_, err = m.CallGraph()
	require.ErrorIs(t, err, wasm.ErrMalformed)
}

func TestAuthLint(t *testing.T) {
	b := &builder{}
	requireAuth := b.importFn("a", "0")
	forArgs := b.importFn("a", "_")
	b.importFn("x", "5")

	checkAdmin := b.fn(call(requireAuth)...)
	b.export("upgrade", b.fn(call(checkAdmin)...))              // via a helper
	b.export("set_admin", b.fn(call(forArgs)...))               // directly, with args
	b.export("withdraw", b.fn())                                // never
	b.export("mint_tokens", b.fn(0x41, 0x00, 0x11, 0x00, 0x00)) // only an indirect call
	b.export("balance", b.fn())                                 // not privileged
	m, err := wasm.Parse(b.build())
	require.NoError(t, err)

	findings, err := m.AuthLint(nil)
	require.NoError(t, err)
	byName := map[string]wasm.AuthFinding{}
	for _, f := range findings {
		byName[f.Function] = f
	}
	require.Len(t, byName, 4, "balance is not privileged")

	up := byName["upgrade"]
	assert.Equal(t, wasm.VerdictRequiresAuth, up.Verdict)
	assert.Equal(t, []string{"upgrade", "func[3]", "host:require_auth"}, up.Path)
	assert.Contains(t, up.Reasoning, "upgrade -> func[3] -> host:require_auth")

	assert.Equal(t, wasm.VerdictRequiresAuth, byName["set_admin"].Verdict)
	assert.Equal(t, []string{"set_admin", "host:require_auth_for_args"}, byName["set_admin"].Path)

	assert.Equal(t, wasm.VerdictNoAuth, byName["withdraw"].Verdict)
	assert.Empty(t, byName["withdraw"].Path)

	assert.Equal(t, wasm.VerdictUnknown, byName["mint_tokens"].Verdict)
	assert.Equal(t, "mint", byName["mint_tokens"].Matched)

	custom, err := m.AuthLint([]string{"balance"})
	require.NoError(t, err)
	require.Len(t, custom, 1)
	assert.Equal(t, "balance", custom[0].Function)
}

func TestAuthLintWithoutAuthImports(t *testing.T) {
	b := &builder{}
	b.export("upgrade", b.fn())
	m, err := wasm.Parse(b.build())
	require.NoError(t, err)
	findings, err := m.AuthLint(nil)
	require.NoError(t, err)
	require.Len(t, findings, 1)
	assert.Equal(t, wasm.VerdictNoAuth, findings[0].Verdict)
	assert.Contains(t, findings[0].Reasoning, "imports no require_auth")
}

// fixtureWasm is the recorded testnet contract: a price oracle whose
// set_price is guarded by its admin.
func fixtureWasm(t *testing.T) []byte {
	t.Helper()
	resp := stellartest.LoadLedgerEntries(t, "ledger_entries_wasm_code.json")
	var data xdr.LedgerEntryData
	require.NoError(t, xdr.SafeUnmarshalBase64(resp.Entries[0].DataXDR, &data))
	return data.ContractCode.Code
}

func TestRealContract(t *testing.T) {
	m, err := wasm.Parse(fixtureWasm(t))
	require.NoError(t, err)
	assert.Equal(t, uint32(22), m.Env.Protocol)
	assert.Contains(t, m.Meta["rssdkver"], "22.0.11")
	assert.True(t, m.HasSpec)

	findings, err := m.AuthLint([]string{"set_price", "initialize"})
	require.NoError(t, err)
	verdicts := map[string]string{}
	for _, f := range findings {
		verdicts[f.Function] = f.Verdict
	}
	assert.Equal(t, wasm.VerdictRequiresAuth, verdicts["set_price"])
}

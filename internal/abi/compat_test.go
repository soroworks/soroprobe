package abi_test

import (
	"bytes"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
)

func buildWasmWithEntries(t *testing.T, entries ...xdr.ScSpecEntry) []byte {
	t.Helper()
	var specBuf bytes.Buffer
	for _, entry := range entries {
		raw, err := entry.MarshalBinary()
		require.NoError(t, err)
		specBuf.Write(raw)
	}

	name := "contractspecv0"
	var secPayload bytes.Buffer
	secPayload.WriteByte(byte(len(name)))
	secPayload.WriteString(name)
	secPayload.Write(specBuf.Bytes())

	var wasm bytes.Buffer
	wasm.Write([]byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00})
	wasm.WriteByte(0) // custom section ID
	writeULEBBytes(&wasm, uint32(secPayload.Len()))
	wasm.Write(secPayload.Bytes())
	return wasm.Bytes()
}

func writeULEBBytes(b *bytes.Buffer, v uint32) {
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			c |= 0x80
		}
		b.WriteByte(c)
		if v == 0 {
			return
		}
	}
}

func fnEntry(name string, inputs []xdr.ScSpecFunctionInputV0, outputs ...xdr.ScSpecTypeDef) xdr.ScSpecEntry {
	fn := xdr.ScSpecFunctionV0{
		Name:    xdr.ScSymbol(name),
		Inputs:  inputs,
		Outputs: outputs,
	}
	return xdr.ScSpecEntry{
		Kind:       xdr.ScSpecEntryKindScSpecEntryFunctionV0,
		FunctionV0: &fn,
	}
}

func structEntry(name string, fields ...xdr.ScSpecUdtStructFieldV0) xdr.ScSpecEntry {
	st := xdr.ScSpecUdtStructV0{
		Name:   name,
		Fields: fields,
	}
	return xdr.ScSpecEntry{
		Kind:        xdr.ScSpecEntryKindScSpecEntryUdtStructV0,
		UdtStructV0: &st,
	}
}

func udtType(name string) xdr.ScSpecTypeDef {
	udt := xdr.ScSpecTypeUdt{Name: name}
	return xdr.ScSpecTypeDef{
		Type: xdr.ScSpecTypeScSpecTypeUdt,
		Udt:  &udt,
	}
}

func TestCompareSpecsDetectsBreakingAndNonBreakingChanges(t *testing.T) {
	t.Parallel()

	u32Type := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeU32}
	i128Type := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeI128}
	addrType := xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeAddress}

	oldWasm := buildWasmWithEntries(t,
		structEntry("Config",
			xdr.ScSpecUdtStructFieldV0{Name: "admin", Type: addrType},
			xdr.ScSpecUdtStructFieldV0{Name: "fee_bps", Type: u32Type},
		),
		fnEntry("initialize", []xdr.ScSpecFunctionInputV0{
			{Name: "cfg", Type: udtType("Config")},
		}),
		fnEntry("transfer", []xdr.ScSpecFunctionInputV0{
			{Name: "to", Type: addrType},
			{Name: "amount", Type: i128Type},
		}, u32Type),
		fnEntry("legacy", nil),
	)

	// Compatible evolution: add a new function and keep existing functions/types intact.
	compatibleWasm := buildWasmWithEntries(t,
		structEntry("Config",
			xdr.ScSpecUdtStructFieldV0{Name: "admin", Type: addrType},
			xdr.ScSpecUdtStructFieldV0{Name: "fee_bps", Type: u32Type},
		),
		fnEntry("initialize", []xdr.ScSpecFunctionInputV0{
			{Name: "cfg", Type: udtType("Config")},
		}),
		fnEntry("transfer", []xdr.ScSpecFunctionInputV0{
			{Name: "to", Type: addrType},
			{Name: "amount", Type: i128Type},
		}, u32Type),
		fnEntry("legacy", nil),
		fnEntry("version", nil, u32Type),
	)

	oldSpec, err := abi.ParseWasmSpec(oldWasm)
	require.NoError(t, err)
	compatSpec, err := abi.ParseWasmSpec(compatibleWasm)
	require.NoError(t, err)

	report := abi.CompareSpecs(oldSpec, compatSpec)
	assert.True(t, report.Compatible)
	assert.Empty(t, report.Breaking)
	require.Len(t, report.NonBreaking, 1)
	assert.Contains(t, report.NonBreaking[0].Summary, "version")

	// Incompatible evolution:
	// - removed function `legacy`
	// - renamed struct field `fee_bps` -> `fee` in `Config` (structural change)
	// - changed `transfer` return type `u32` -> `i128`
	breakingWasm := buildWasmWithEntries(t,
		structEntry("Config",
			xdr.ScSpecUdtStructFieldV0{Name: "admin", Type: addrType},
			xdr.ScSpecUdtStructFieldV0{Name: "fee", Type: u32Type},
		),
		fnEntry("initialize", []xdr.ScSpecFunctionInputV0{
			{Name: "cfg", Type: udtType("Config")},
		}),
		fnEntry("transfer", []xdr.ScSpecFunctionInputV0{
			{Name: "to", Type: addrType},
			{Name: "amount", Type: i128Type},
		}, i128Type),
	)

	breakSpec, err := abi.ParseWasmSpec(breakingWasm)
	require.NoError(t, err)

	breakReport := abi.CompareSpecs(oldSpec, breakSpec)
	assert.False(t, breakReport.Compatible)
	assert.NotEmpty(t, breakReport.Breaking)

	var summaries []string
	for _, c := range breakReport.Breaking {
		summaries = append(summaries, c.Summary)
	}
	joined := ""
	for _, s := range summaries {
		joined += s + "\n"
	}
	assert.Contains(t, joined, "legacy")
	assert.Contains(t, joined, `field "fee_bps" renamed to "fee"`)
	assert.Contains(t, joined, "return type changed")
}

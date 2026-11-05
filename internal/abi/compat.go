package abi

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// Spec holds a contract's full parsed ScSpecEntry declarations — functions,
// structs, unions, enums, and error enums — for structural compatibility
// comparison.
type Spec struct {
	Functions  map[string]xdr.ScSpecFunctionV0
	Structs    map[string]xdr.ScSpecUdtStructV0
	Unions     map[string]xdr.ScSpecUdtUnionV0
	Enums      map[string]xdr.ScSpecUdtEnumV0
	ErrorEnums map[string]xdr.ScSpecUdtErrorEnumV0
}

// Change describes one interface difference between an old and a new contract
// specification.
type Change struct {
	Breaking bool   `json:"breaking"`
	Category string `json:"category"`
	Symbol   string `json:"symbol"`
	Summary  string `json:"summary"`
}

// CompatReport summarises the compatibility comparison between two contract
// specifications.
type CompatReport struct {
	Compatible  bool     `json:"compatible"`
	Breaking    []Change `json:"breaking"`
	NonBreaking []Change `json:"non_breaking"`
}

// ParseWasmSpec extracts and decodes the contractspecv0 custom section of a
// Wasm module into a Spec.
func ParseWasmSpec(wasm []byte) (*Spec, error) {
	section, err := customSection(wasm, contractSpecSection)
	if err != nil {
		return nil, err
	}
	return ParseSpecSection(section)
}

// ParseSpecSection decodes a raw contractspecv0 stream of ScSpecEntry values.
func ParseSpecSection(section []byte) (*Spec, error) {
	reader := bytes.NewReader(section)
	spec := &Spec{
		Functions:  make(map[string]xdr.ScSpecFunctionV0),
		Structs:    make(map[string]xdr.ScSpecUdtStructV0),
		Unions:     make(map[string]xdr.ScSpecUdtUnionV0),
		Enums:      make(map[string]xdr.ScSpecUdtEnumV0),
		ErrorEnums: make(map[string]xdr.ScSpecUdtErrorEnumV0),
	}

	for reader.Len() > 0 {
		var entry xdr.ScSpecEntry
		if _, err := xdr.Unmarshal(reader, &entry); err != nil {
			return nil, fmt.Errorf("decode contract spec: %w", err)
		}
		switch entry.Kind {
		case xdr.ScSpecEntryKindScSpecEntryFunctionV0:
			fn := entry.MustFunctionV0()
			spec.Functions[string(fn.Name)] = fn
		case xdr.ScSpecEntryKindScSpecEntryUdtStructV0:
			st := entry.MustUdtStructV0()
			spec.Structs[st.Name] = st
		case xdr.ScSpecEntryKindScSpecEntryUdtUnionV0:
			un := entry.MustUdtUnionV0()
			spec.Unions[un.Name] = un
		case xdr.ScSpecEntryKindScSpecEntryUdtEnumV0:
			en := entry.MustUdtEnumV0()
			spec.Enums[en.Name] = en
		case xdr.ScSpecEntryKindScSpecEntryUdtErrorEnumV0:
			er := entry.MustUdtErrorEnumV0()
			spec.ErrorEnums[er.Name] = er
		}
	}
	return spec, nil
}

// FetchChainSpec fetches a deployed contract's Wasm from the network and parses
// its contractspecv0 section into a Spec.
func FetchChainSpec(ctx context.Context, client stellar.Client, contractID string) (*Spec, error) {
	contract, err := stellar.ParseContractID(contractID)
	if err != nil {
		return nil, err
	}
	instanceKey, err := stellar.EncodeKey(stellar.InstanceKey(contract))
	if err != nil {
		return nil, err
	}
	instanceResponse, err := client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{instanceKey}})
	if err != nil {
		return nil, err
	}
	if len(instanceResponse.Entries) == 0 {
		return nil, ErrUnknownContract
	}
	instanceData, err := stellar.DecodeEntryData(instanceResponse.Entries[0].DataXDR)
	if err != nil {
		return nil, fmt.Errorf("decode contract instance: %w", err)
	}
	if instanceData.ContractData == nil {
		return nil, fmt.Errorf("contract instance entry is not contract data")
	}
	instance, ok := instanceData.ContractData.Val.GetInstance()
	if !ok {
		return nil, fmt.Errorf("contract instance entry does not hold an instance")
	}
	if instance.Executable.Type == xdr.ContractExecutableTypeContractExecutableStellarAsset {
		return nil, ErrNoWasm
	}
	if instance.Executable.Type != xdr.ContractExecutableTypeContractExecutableWasm || instance.Executable.WasmHash == nil {
		return nil, fmt.Errorf("contract executable %s has no Wasm hash", instance.Executable.Type)
	}

	hash := fmt.Sprintf("%x", instance.Executable.WasmHash[:])
	codeKey, err := stellar.EncodeKey(stellar.CodeKey(*instance.Executable.WasmHash))
	if err != nil {
		return nil, err
	}
	codeResponse, err := client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{codeKey}})
	if err != nil {
		return nil, err
	}
	if len(codeResponse.Entries) == 0 {
		return nil, fmt.Errorf("contract Wasm code %s was not found on ledger", hash)
	}
	codeData, err := stellar.DecodeEntryData(codeResponse.Entries[0].DataXDR)
	if err != nil {
		return nil, fmt.Errorf("decode contract Wasm entry: %w", err)
	}
	if codeData.ContractCode == nil {
		return nil, fmt.Errorf("contract code entry does not contain Wasm")
	}
	return ParseWasmSpec(codeData.ContractCode.Code)
}

// CompareSpecs compares oldSpec and newSpec structurally, returning all
// breaking and non-breaking changes in a deterministic order.
func CompareSpecs(oldSpec, newSpec *Spec) CompatReport {
	report := CompatReport{
		Breaking:    []Change{},
		NonBreaking: []Change{},
	}

	addBreaking := func(category, symbol, summary string) {
		report.Breaking = append(report.Breaking, Change{
			Breaking: true,
			Category: category,
			Symbol:   symbol,
			Summary:  summary,
		})
	}
	addNonBreaking := func(category, symbol, summary string) {
		report.NonBreaking = append(report.NonBreaking, Change{
			Breaking: false,
			Category: category,
			Symbol:   symbol,
			Summary:  summary,
		})
	}

	// 1. Compare top-level user-defined types shared between old and new.
	compareUDTs(oldSpec, newSpec, addBreaking, addNonBreaking)

	// 2. Compare functions (including structural comparison of argument and
	// return types so a changed UDT referenced by a function is caught even
	// if its type name changed or stayed the same).
	for _, name := range sortedMapKeys(oldSpec.Functions) {
		oldFn := oldSpec.Functions[name]
		newFn, exists := newSpec.Functions[name]
		if !exists {
			addBreaking("function", name, fmt.Sprintf("function %s was removed", formatSpecFn(oldFn)))
			continue
		}

		if len(oldFn.Inputs) != len(newFn.Inputs) {
			addBreaking("function", name, fmt.Sprintf(
				"function %s changed parameter count from %d to %d",
				name, len(oldFn.Inputs), len(newFn.Inputs)))
		} else {
			for i := range oldFn.Inputs {
				oldParam := oldFn.Inputs[i]
				newParam := newFn.Inputs[i]
				if oldParam.Name != newParam.Name {
					addBreaking("function", name, fmt.Sprintf(
						"function %s parameter %d renamed from %q to %q",
						name, i+1, oldParam.Name, newParam.Name))
				}
				if reason := diffTypeStructural(oldSpec, newSpec, oldParam.Type, newParam.Type, make(map[string]bool)); reason != "" {
					addBreaking("function", name, fmt.Sprintf(
						"function %s parameter %q changed type: %s",
						name, oldParam.Name, reason))
				}
			}
		}

		oldOut := outputTypeOrVoid(oldFn.Outputs)
		newOut := outputTypeOrVoid(newFn.Outputs)
		if reason := diffTypeStructural(oldSpec, newSpec, oldOut, newOut, make(map[string]bool)); reason != "" {
			addBreaking("function", name, fmt.Sprintf(
				"function %s return type changed: %s",
				name, reason))
		}
	}

	for _, name := range sortedMapKeys(newSpec.Functions) {
		if _, exists := oldSpec.Functions[name]; !exists {
			newFn := newSpec.Functions[name]
			addNonBreaking("function", name, fmt.Sprintf("function %s was added", formatSpecFn(newFn)))
		}
	}

	report.Compatible = len(report.Breaking) == 0
	return report
}

func compareUDTs(oldSpec, newSpec *Spec, addBreaking, addNonBreaking func(category, symbol, summary string)) {
	for _, name := range sortedMapKeys(oldSpec.Structs) {
		oldSt := oldSpec.Structs[name]
		newSt, ok := newSpec.Structs[name]
		if !ok {
			if newSpec.hasUDT(name) {
				addBreaking("type", name, fmt.Sprintf("type %s changed kind from struct", name))
			} else {
				addBreaking("type", name, fmt.Sprintf("struct %s was removed", name))
			}
			continue
		}
		if reason := diffStruct(oldSpec, newSpec, oldSt, newSt, make(map[string]bool)); reason != "" {
			addBreaking("type", name, fmt.Sprintf("struct %s changed: %s", name, reason))
		}
	}
	for _, name := range sortedMapKeys(newSpec.Structs) {
		if !oldSpec.hasUDT(name) {
			addNonBreaking("type", name, fmt.Sprintf("struct %s was added", name))
		}
	}

	for _, name := range sortedMapKeys(oldSpec.Unions) {
		oldUn := oldSpec.Unions[name]
		newUn, ok := newSpec.Unions[name]
		if !ok {
			if newSpec.hasUDT(name) {
				addBreaking("type", name, fmt.Sprintf("type %s changed kind from union", name))
			} else {
				addBreaking("type", name, fmt.Sprintf("union %s was removed", name))
			}
			continue
		}
		if reason := diffUnion(oldSpec, newSpec, oldUn, newUn, make(map[string]bool)); reason != "" {
			addBreaking("type", name, fmt.Sprintf("union %s changed: %s", name, reason))
		}
	}
	for _, name := range sortedMapKeys(newSpec.Unions) {
		if !oldSpec.hasUDT(name) {
			addNonBreaking("type", name, fmt.Sprintf("union %s was added", name))
		}
	}

	for _, name := range sortedMapKeys(oldSpec.Enums) {
		oldEn := oldSpec.Enums[name]
		newEn, ok := newSpec.Enums[name]
		if !ok {
			if newSpec.hasUDT(name) {
				addBreaking("type", name, fmt.Sprintf("type %s changed kind from enum", name))
			} else {
				addBreaking("type", name, fmt.Sprintf("enum %s was removed", name))
			}
			continue
		}
		breaking, added := diffEnumCases(oldEn.Cases, newEn.Cases)
		if breaking != "" {
			addBreaking("type", name, fmt.Sprintf("enum %s changed: %s", name, breaking))
		}
		for _, c := range added {
			addNonBreaking("type", name, fmt.Sprintf("enum %s added case %s = %d", name, c.Name, c.Value))
		}
	}
	for _, name := range sortedMapKeys(newSpec.Enums) {
		if !oldSpec.hasUDT(name) {
			addNonBreaking("type", name, fmt.Sprintf("enum %s was added", name))
		}
	}

	for _, name := range sortedMapKeys(oldSpec.ErrorEnums) {
		oldEr := oldSpec.ErrorEnums[name]
		newEr, ok := newSpec.ErrorEnums[name]
		if !ok {
			if newSpec.hasUDT(name) {
				addBreaking("type", name, fmt.Sprintf("type %s changed kind from error enum", name))
			} else {
				addBreaking("type", name, fmt.Sprintf("error enum %s was removed", name))
			}
			continue
		}
		breaking, added := diffErrorEnumCases(oldEr.Cases, newEr.Cases)
		if breaking != "" {
			addBreaking("type", name, fmt.Sprintf("error enum %s changed: %s", name, breaking))
		}
		for _, c := range added {
			addNonBreaking("type", name, fmt.Sprintf("error enum %s added case %s = %d", name, c.Name, c.Value))
		}
	}
	for _, name := range sortedMapKeys(newSpec.ErrorEnums) {
		if !oldSpec.hasUDT(name) {
			addNonBreaking("type", name, fmt.Sprintf("error enum %s was added", name))
		}
	}
}

func (s *Spec) hasUDT(name string) bool {
	_, isStruct := s.Structs[name]
	_, isUnion := s.Unions[name]
	_, isEnum := s.Enums[name]
	_, isErrEnum := s.ErrorEnums[name]
	return isStruct || isUnion || isEnum || isErrEnum
}

// diffTypeStructural compares oldDef and newDef structurally, dereferencing
// user-defined types across oldSpec and newSpec so that two UDTs with identical
// structure are considered compatible and a UDT whose underlying fields changed
// is flagged as incompatible.
func diffTypeStructural(oldSpec, newSpec *Spec, oldDef, newDef xdr.ScSpecTypeDef, visited map[string]bool) string {
	if oldDef.Type != newDef.Type {
		return fmt.Sprintf("%s -> %s", specType(oldDef).Display, specType(newDef).Display)
	}

	switch oldDef.Type {
	case xdr.ScSpecTypeScSpecTypeOption:
		if r := diffTypeStructural(oldSpec, newSpec, oldDef.MustOption().ValueType, newDef.MustOption().ValueType, visited); r != "" {
			return fmt.Sprintf("Option<%s>", r)
		}
		return ""
	case xdr.ScSpecTypeScSpecTypeVec:
		if r := diffTypeStructural(oldSpec, newSpec, oldDef.MustVec().ElementType, newDef.MustVec().ElementType, visited); r != "" {
			return fmt.Sprintf("Vec<%s>", r)
		}
		return ""
	case xdr.ScSpecTypeScSpecTypeMap:
		oldMap, newMap := oldDef.MustMap(), newDef.MustMap()
		if r := diffTypeStructural(oldSpec, newSpec, oldMap.KeyType, newMap.KeyType, visited); r != "" {
			return fmt.Sprintf("Map key (%s)", r)
		}
		if r := diffTypeStructural(oldSpec, newSpec, oldMap.ValueType, newMap.ValueType, visited); r != "" {
			return fmt.Sprintf("Map value (%s)", r)
		}
		return ""
	case xdr.ScSpecTypeScSpecTypeResult:
		oldRes, newRes := oldDef.MustResult(), newDef.MustResult()
		if r := diffTypeStructural(oldSpec, newSpec, oldRes.OkType, newRes.OkType, visited); r != "" {
			return fmt.Sprintf("Result ok (%s)", r)
		}
		if r := diffTypeStructural(oldSpec, newSpec, oldRes.ErrorType, newRes.ErrorType, visited); r != "" {
			return fmt.Sprintf("Result error (%s)", r)
		}
		return ""
	case xdr.ScSpecTypeScSpecTypeTuple:
		oldTup, newTup := oldDef.MustTuple(), newDef.MustTuple()
		if len(oldTup.ValueTypes) != len(newTup.ValueTypes) {
			return fmt.Sprintf("tuple arity %d -> %d", len(oldTup.ValueTypes), len(newTup.ValueTypes))
		}
		for i := range oldTup.ValueTypes {
			if r := diffTypeStructural(oldSpec, newSpec, oldTup.ValueTypes[i], newTup.ValueTypes[i], visited); r != "" {
				return fmt.Sprintf("tuple element %d (%s)", i, r)
			}
		}
		return ""
	case xdr.ScSpecTypeScSpecTypeBytesN:
		oldN, newN := oldDef.MustBytesN().N, newDef.MustBytesN().N
		if oldN != newN {
			return fmt.Sprintf("BytesN<%d> -> BytesN<%d>", oldN, newN)
		}
		return ""
	case xdr.ScSpecTypeScSpecTypeUdt:
		oldName := oldDef.MustUdt().Name
		newName := newDef.MustUdt().Name
		pairKey := oldName + "|" + newName
		if visited[pairKey] {
			return ""
		}
		visited[pairKey] = true
		return diffNamedUDT(oldSpec, newSpec, oldName, newName, visited)
	default:
		return ""
	}
}

func diffNamedUDT(oldSpec, newSpec *Spec, oldName, newName string, visited map[string]bool) string {
	if oldSt, ok := oldSpec.Structs[oldName]; ok {
		newSt, ok2 := newSpec.Structs[newName]
		if !ok2 {
			return fmt.Sprintf("struct %s is not a struct in new spec (%s)", oldName, newName)
		}
		return diffStruct(oldSpec, newSpec, oldSt, newSt, visited)
	}
	if oldUn, ok := oldSpec.Unions[oldName]; ok {
		newUn, ok2 := newSpec.Unions[newName]
		if !ok2 {
			return fmt.Sprintf("union %s is not a union in new spec (%s)", oldName, newName)
		}
		return diffUnion(oldSpec, newSpec, oldUn, newUn, visited)
	}
	if oldEn, ok := oldSpec.Enums[oldName]; ok {
		newEn, ok2 := newSpec.Enums[newName]
		if !ok2 {
			return fmt.Sprintf("enum %s is not an enum in new spec (%s)", oldName, newName)
		}
		b, _ := diffEnumCases(oldEn.Cases, newEn.Cases)
		return b
	}
	if oldEr, ok := oldSpec.ErrorEnums[oldName]; ok {
		newEr, ok2 := newSpec.ErrorEnums[newName]
		if !ok2 {
			return fmt.Sprintf("error enum %s is not an error enum in new spec (%s)", oldName, newName)
		}
		b, _ := diffErrorEnumCases(oldEr.Cases, newEr.Cases)
		return b
	}
	if oldName != newName {
		return fmt.Sprintf("%s -> %s", oldName, newName)
	}
	return ""
}

func diffStruct(oldSpec, newSpec *Spec, oldSt, newSt xdr.ScSpecUdtStructV0, visited map[string]bool) string {
	if len(oldSt.Fields) != len(newSt.Fields) {
		return fmt.Sprintf("field count changed from %d to %d", len(oldSt.Fields), len(newSt.Fields))
	}
	for i := range oldSt.Fields {
		of := oldSt.Fields[i]
		nf := newSt.Fields[i]
		if of.Name != nf.Name {
			return fmt.Sprintf("field %q renamed to %q", of.Name, nf.Name)
		}
		if r := diffTypeStructural(oldSpec, newSpec, of.Type, nf.Type, visited); r != "" {
			return fmt.Sprintf("field %q changed type (%s)", of.Name, r)
		}
	}
	return ""
}

func diffUnion(oldSpec, newSpec *Spec, oldUn, newUn xdr.ScSpecUdtUnionV0, visited map[string]bool) string {
	newCases := make(map[string]xdr.ScSpecUdtUnionCaseV0, len(newUn.Cases))
	for _, c := range newUn.Cases {
		newCases[unionCaseName(c)] = c
	}
	for _, oc := range oldUn.Cases {
		cName := unionCaseName(oc)
		nc, ok := newCases[cName]
		if !ok {
			return fmt.Sprintf("case %q was removed", cName)
		}
		if oc.Kind != nc.Kind {
			return fmt.Sprintf("case %q changed kind", cName)
		}
		if oc.Kind == xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseTupleV0 {
			ot := oc.MustTupleCase().Type
			nt := nc.MustTupleCase().Type
			if len(ot) != len(nt) {
				return fmt.Sprintf("case %q payload count changed from %d to %d", cName, len(ot), len(nt))
			}
			for i := range ot {
				if r := diffTypeStructural(oldSpec, newSpec, ot[i], nt[i], visited); r != "" {
					return fmt.Sprintf("case %q payload %d changed type (%s)", cName, i, r)
				}
			}
		}
	}
	if len(newUn.Cases) != len(oldUn.Cases) {
		return fmt.Sprintf("case count changed from %d to %d", len(oldUn.Cases), len(newUn.Cases))
	}
	return ""
}

func unionCaseName(c xdr.ScSpecUdtUnionCaseV0) string {
	if c.Kind == xdr.ScSpecUdtUnionCaseV0KindScSpecUdtUnionCaseVoidV0 {
		return c.MustVoidCase().Name
	}
	return c.MustTupleCase().Name
}

func diffEnumCases(oldCases, newCases []xdr.ScSpecUdtEnumCaseV0) (string, []xdr.ScSpecUdtEnumCaseV0) {
	byName := make(map[string]uint32, len(newCases))
	oldNames := make(map[string]bool, len(oldCases))
	for _, c := range newCases {
		byName[c.Name] = uint32(c.Value)
	}
	for _, oc := range oldCases {
		oldNames[oc.Name] = true
		nv, ok := byName[oc.Name]
		if !ok {
			return fmt.Sprintf("case %q (%d) was removed", oc.Name, oc.Value), nil
		}
		if uint32(oc.Value) != nv {
			return fmt.Sprintf("case %q changed value from %d to %d", oc.Name, oc.Value, nv), nil
		}
	}
	var added []xdr.ScSpecUdtEnumCaseV0
	for _, nc := range newCases {
		if !oldNames[nc.Name] {
			added = append(added, nc)
		}
	}
	return "", added
}

func diffErrorEnumCases(oldCases, newCases []xdr.ScSpecUdtErrorEnumCaseV0) (string, []xdr.ScSpecUdtErrorEnumCaseV0) {
	byName := make(map[string]uint32, len(newCases))
	oldNames := make(map[string]bool, len(oldCases))
	for _, c := range newCases {
		byName[c.Name] = uint32(c.Value)
	}
	for _, oc := range oldCases {
		oldNames[oc.Name] = true
		nv, ok := byName[oc.Name]
		if !ok {
			return fmt.Sprintf("error case %q (%d) was removed", oc.Name, oc.Value), nil
		}
		if uint32(oc.Value) != nv {
			return fmt.Sprintf("error case %q changed value from %d to %d", oc.Name, oc.Value, nv), nil
		}
	}
	var added []xdr.ScSpecUdtErrorEnumCaseV0
	for _, nc := range newCases {
		if !oldNames[nc.Name] {
			added = append(added, nc)
		}
	}
	return "", added
}

func outputTypeOrVoid(outputs []xdr.ScSpecTypeDef) xdr.ScSpecTypeDef {
	if len(outputs) == 0 {
		return xdr.ScSpecTypeDef{Type: xdr.ScSpecTypeScSpecTypeVoid}
	}
	return outputs[0]
}

func formatSpecFn(fn xdr.ScSpecFunctionV0) string {
	var b strings.Builder
	b.WriteString("fn ")
	b.WriteString(string(fn.Name))
	b.WriteByte('(')
	for i, in := range fn.Inputs {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(in.Name)
		b.WriteString(": ")
		b.WriteString(specType(in.Type).Display)
	}
	b.WriteByte(')')
	if len(fn.Outputs) > 0 {
		b.WriteString(" -> ")
		b.WriteString(specType(fn.Outputs[0]).Display)
	}
	return b.String()
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

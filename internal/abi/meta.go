package abi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

const contractMetaSection = "contractmetav0"

// BuildMeta holds the contract metadata decoded from the Wasm module's
// "contractmetav0" custom section alongside the module's SHA-256 hash.
type BuildMeta struct {
	WasmHash    string            `json:"wasm_hash"`
	Entries     map[string]string `json:"entries,omitempty"`
	RustVersion string            `json:"rust_version,omitempty"`
	SDKVersion  string            `json:"sdk_version,omitempty"`
	SourceRepo  string            `json:"source_repo,omitempty"`
	SourceRev   string            `json:"source_rev,omitempty"`
}

// ParseWasmBuildMeta computes the SHA-256 hash of a Wasm module and decodes its
// "contractmetav0" custom section when present. A module without a metadata
// section is not an error; Entries simply remains empty.
func ParseWasmBuildMeta(wasm []byte) (*BuildMeta, error) {
	if len(wasm) < 8 || !bytes.Equal(wasm[:4], []byte{0x00, 'a', 's', 'm'}) {
		return nil, errors.New("invalid Wasm module")
	}
	sum := sha256.Sum256(wasm)
	meta := &BuildMeta{
		WasmHash: hex.EncodeToString(sum[:]),
		Entries:  make(map[string]string),
	}

	section, err := customSection(wasm, contractMetaSection)
	if err != nil {
		return meta, nil
	}

	reader := bytes.NewReader(section)
	for reader.Len() > 0 {
		var entry xdr.ScMetaEntry
		if _, err := xdr.Unmarshal(reader, &entry); err != nil {
			return nil, fmt.Errorf("decode contract meta: %w", err)
		}
		if entry.Kind == xdr.ScMetaKindScMetaV0 && entry.V0 != nil {
			meta.Entries[entry.V0.Key] = entry.V0.Val
		}
	}

	meta.RustVersion = firstMetaValue(meta.Entries, "rsver", "rust_version", "rustc")
	meta.SDKVersion = firstMetaValue(meta.Entries, "rssdkver", "soroban_sdk", "sdk_version")
	meta.SourceRepo = firstMetaValue(meta.Entries, "source_repo", "source_url", "repository", "repo", "scm_repo")
	meta.SourceRev = firstMetaValue(meta.Entries, "source_rev", "source_commit", "commit", "rev", "git_commit", "scm_revision")

	return meta, nil
}

func firstMetaValue(entries map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(entries[k]); v != "" {
			return v
		}
	}
	return ""
}

// FetchLiveBuildMeta fetches a deployed contract's Wasm from the ledger and
// returns its SHA-256 hash and decoded "contractmetav0" entries.
func FetchLiveBuildMeta(ctx context.Context, client stellar.Client, contractID string) (*BuildMeta, error) {
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
		return nil, errors.New("contract instance entry is not contract data")
	}
	instance, ok := instanceData.ContractData.Val.GetInstance()
	if !ok {
		return nil, errors.New("contract instance entry does not hold an instance")
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
		return nil, errors.New("contract code entry does not contain Wasm")
	}

	meta, err := ParseWasmBuildMeta(codeData.ContractCode.Code)
	if err != nil {
		return nil, err
	}
	return meta, nil
}

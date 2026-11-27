package probe

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/wasm"
)

// mainnetPassphrase identifies the public network, where pre-release
// builds are not accepted.
const mainnetPassphrase = "Public Global Stellar Network ; September 2015"

// envVersionStep compares the Soroban environment interface version a
// contract was built for, from its contractenvmetav0 section, with the
// protocol the network runs. A contract built for a newer protocol cannot
// run; a pre-release build on mainnet is flagged because mainnet only runs
// released protocols.
type envVersionStep struct{ p *Prober }

func (s *envVersionStep) Name() string { return "env_version" }

func (s *envVersionStep) Run(ctx context.Context, req CheckRequest, inspect *InspectResult, _ *SimulateResult) ([]Check, error) {
	check := func(o CheckOutcome, format string, a ...any) []Check {
		return []Check{{Name: "env_version", Outcome: o, Detail: fmt.Sprintf(format, a...)}}
	}
	if inspect != nil && inspect.Executable == "stellar_asset" {
		return check(OutcomeSkip, "stellar asset contracts are built into the host and carry no environment version"), nil
	}

	if inspect == nil || inspect.WasmHash == "" {
		return check(OutcomeSkip, "the contract has no WASM to read an environment version from"), nil
	}
	code, err := s.p.codeByHash(ctx, inspect.WasmHash)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return check(OutcomeWarn, "could not read the contract's WASM: %v", err), nil
	}
	m, err := wasm.Parse(code)
	if err != nil {
		return check(OutcomeWarn, "could not read the contract's WASM: %v", err), nil
	}
	if m.Env == nil {
		return check(OutcomeWarn, "the WASM has no contractenvmetav0 section, so its environment version is unknown"), nil
	}

	network, err := s.p.client.GetNetwork(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return check(OutcomeSkip, "could not read the network's protocol version: %v", err), nil
	}
	built, running := m.Env.Protocol, uint32(network.ProtocolVersion) //nolint:gosec // protocol versions are small positive integers
	switch {
	case network.ProtocolVersion <= 0:
		return check(OutcomeSkip, "built for protocol %d; the network did not report its protocol version", built), nil
	case built > running:
		return check(OutcomeFail, "built for protocol %d, but the network runs protocol %d: the contract cannot run until the network upgrades", built, running), nil
	case m.Env.PreRelease != 0 && network.Passphrase == mainnetPassphrase:
		return check(OutcomeWarn, "a pre-release build (protocol %d, pre-release %d) on mainnet, which only runs released protocols; rebuild with a released SDK", built, m.Env.PreRelease), nil
	case m.Env.PreRelease != 0:
		return check(OutcomePass, "built for protocol %d pre-release %d; the network runs protocol %d", built, m.Env.PreRelease, running), nil
	default:
		return check(OutcomePass, "built for protocol %d; the network runs protocol %d", built, running), nil
	}
}

// codeByHash reads a code entry through the prober's own client, so a batch
// serves it from the entries it already prefetched for the TTL report.
func (p *Prober) codeByHash(ctx context.Context, hexHash string) ([]byte, error) {
	raw, err := hex.DecodeString(hexHash)
	if err != nil || len(raw) != 32 {
		return nil, fmt.Errorf("invalid wasm hash %q", hexHash)
	}
	key, err := stellar.EncodeKey(stellar.CodeKey(xdr.Hash(raw)))
	if err != nil {
		return nil, err
	}
	resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{key}})
	if err != nil {
		return nil, err
	}
	for _, entry := range resp.Entries {
		var data xdr.LedgerEntryData
		if err := xdr.SafeUnmarshalBase64(entry.DataXDR, &data); err != nil {
			return nil, fmt.Errorf("decode code entry: %w", err)
		}
		if c, ok := data.GetContractCode(); ok {
			return c.Code, nil
		}
	}
	return nil, errors.New("the code entry was not found")
}

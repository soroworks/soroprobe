package probe

import (
	"context"
	"errors"
	"fmt"
	"strings"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/stellar"
)

// ErrSecretKey reports a secret key passed where an account address belongs.
// SoroProbe never needs one and refuses to handle one.
var ErrSecretKey = errors.New("that is a secret key; SoroProbe only ever takes a public account address (G...)")

// ErrAccountNotFound reports a --source account that does not exist.
var ErrAccountNotFound = errors.New("account not found")

// simulationSource picks the account a call is simulated as. The
// configured placeholder needs no lookup and uses sequence 0. A requested
// account must exist: its sequence number comes from its ledger entry, so
// the simulated transaction is one that account could really submit.
func (p *Prober) simulationSource(ctx context.Context, requested string) (string, int64, error) {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return p.source, 0, nil
	}
	if strings.HasPrefix(requested, "S") && strkey.IsValidEd25519SecretSeed(requested) {
		return "", 0, ErrSecretKey
	}
	var id xdr.AccountId
	if err := id.SetAddress(requested); err != nil {
		return "", 0, fmt.Errorf("source %q is not an account address (G...): %w", requested, err)
	}
	key, err := stellar.EncodeKey(xdr.LedgerKey{
		Type:    xdr.LedgerEntryTypeAccount,
		Account: &xdr.LedgerKeyAccount{AccountId: id},
	})
	if err != nil {
		return "", 0, err
	}
	resp, err := p.client.GetLedgerEntries(ctx, protocol.GetLedgerEntriesRequest{Keys: []string{key}})
	if err != nil {
		return "", 0, err
	}
	for _, entry := range resp.Entries {
		if entry.KeyXDR != key {
			continue
		}
		var data xdr.LedgerEntryData
		if err := xdr.SafeUnmarshalBase64(entry.DataXDR, &data); err != nil {
			return "", 0, fmt.Errorf("decode source account: %w", err)
		}
		acc, ok := data.GetAccount()
		if !ok {
			return "", 0, fmt.Errorf("source %s: ledger entry is not an account", requested)
		}
		return requested, int64(acc.SeqNum), nil
	}
	return "", 0, fmt.Errorf("%w: %s does not exist on this network; fund it first, or leave --source out to simulate with a placeholder", ErrAccountNotFound, requested)
}

// addLimitsAndFees adds the usage report and fee recommendation to a
// successful simulation. Both are advisory: a network that does not return
// them leaves a note rather than failing the call.
func (p *Prober) addLimitsAndFees(ctx context.Context, req SimulateRequest, result *SimulateResult) {
	limits, err := p.Limits(ctx)
	switch {
	case err != nil:
		p.log.Debug("could not read network limits", "err", err)
		result.Notes = append(result.Notes, "network limits unavailable: "+err.Error())
	default:
		result.Usage = CompareWithLimits(result.Cost, result.Footprint, limits, req.HeadroomPercent)
		if result.Usage == nil {
			result.Notes = append(result.Notes, "the network reported no resource limits to compare with")
		}
	}

	stats, err := p.client.GetFeeStats(ctx)
	if err != nil {
		p.log.Debug("could not read fee stats", "err", err)
		result.Notes = append(result.Notes, "fee stats unavailable: "+err.Error())
		return
	}
	fees := RecommendFees(result.Cost.ResourceFee, p.baseFee, stats)
	result.Fees = &fees
}

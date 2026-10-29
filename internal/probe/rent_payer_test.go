package probe_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func addAccountToFake(t *testing.T, fake *stellartest.Fake, address string, balanceStroops int64, numSubEntries uint32) {
	t.Helper()
	accountID, err := stellar.ParseAccountID(address)
	require.NoError(t, err)

	key := stellar.AccountKey(accountID)
	b64Key, err := stellar.EncodeKey(key)
	require.NoError(t, err)

	entryData := xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.AccountEntry{
			AccountId:     accountID,
			Balance:       xdr.Int64(balanceStroops),
			NumSubEntries: xdr.Uint32(numSubEntries),
		},
	}
	b64Data, err := xdr.MarshalBase64(entryData)
	require.NoError(t, err)

	fake.Entries[b64Key] = protocol.LedgerEntryResult{
		DataXDR: b64Data,
	}
}

func TestCheck_RentPayer_HealthyTTL(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	const payer = "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK"
	// 10 XLM balance, 0 subentries -> 9 XLM spendable
	addAccountToFake(t, fake, payer, 100_000_000, 0)

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		RentPayer:  payer,
	})
	require.NoError(t, err)

	chk := findCheck(res.Checks, "rent_payer")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomePass, chk.Outcome)
	assert.Contains(t, chk.Detail, "all entries have healthy TTL")
	assert.Contains(t, chk.Detail, "spendable")
}

func TestCheck_RentPayer_ExpiringWithSufficientBalance(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	const payer = "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK"
	addAccountToFake(t, fake, payer, 500_000_000, 2) // 50 XLM balance, 4 sub-entry reserves -> 48 XLM spendable

	// Set instance TTL to warning range
	instKey := stellartest.InstanceKeyOf(t, stellartest.SACContract)
	fake.SetTTL(t, instKey, fake.LatestLedger+100)

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		RentPayer:  payer,
	})
	require.NoError(t, err)

	chk := findCheck(res.Checks, "rent_payer")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomePass, chk.Outcome)
	assert.Contains(t, chk.Detail, "rent payer has")
	assert.Contains(t, chk.Detail, "estimated rent is")
}

func TestCheck_RentPayer_ExpiringWithInsufficientBalance(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	const payer = "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK"
	// Balance equals mandatory reserve (0 spendable)
	addAccountToFake(t, fake, payer, 10_000_000, 0)

	// Set instance TTL to warning range
	instKey := stellartest.InstanceKeyOf(t, stellartest.SACContract)
	fake.SetTTL(t, instKey, fake.LatestLedger+100)

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		RentPayer:  payer,
	})
	require.NoError(t, err)

	chk := findCheck(res.Checks, "rent_payer")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomeFail, chk.Outcome)
	assert.Contains(t, chk.Detail, "insufficient rent funds")
}

func TestCheck_RentPayer_NonExistentAccount(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	const payer = "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK"

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		RentPayer:  payer,
	})
	require.NoError(t, err)

	chk := findCheck(res.Checks, "rent_payer")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomeFail, chk.Outcome)
	assert.Contains(t, chk.Detail, "does not exist on-chain")
}

func TestCheck_RentPayer_InvalidAddress(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		RentPayer:  "invalid-strkey-address",
	})
	require.NoError(t, err)

	chk := findCheck(res.Checks, "rent_payer")
	require.NotNil(t, chk)
	assert.Equal(t, probe.OutcomeFail, chk.Outcome)
	assert.Contains(t, chk.Detail, "invalid rent payer address")
}

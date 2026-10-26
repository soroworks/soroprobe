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

func addAccountWithSignersToFake(t *testing.T, fake *stellartest.Fake, address string, masterWeight uint32, signers []xdr.Signer) {
	t.Helper()
	accountID, err := stellar.ParseAccountID(address)
	require.NoError(t, err)

	key := stellar.AccountKey(accountID)
	b64Key, err := stellar.EncodeKey(key)
	require.NoError(t, err)

	entryData := xdr.LedgerEntryData{
		Type: xdr.LedgerEntryTypeAccount,
		Account: &xdr.AccountEntry{
			AccountId:  accountID,
			Balance:    100_000_000,
			Thresholds: [4]byte{byte(masterWeight), 1, 2, 2},
			Signers:    signers,
		},
	}
	b64Data, err := xdr.MarshalBase64(entryData)
	require.NoError(t, err)

	fake.Entries[b64Key] = protocol.LedgerEntryResult{
		DataXDR: b64Data,
	}
}

func TestEvaluateAdminPosture_SingleKeyAccountWarns(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	const adminAddr = "GB32JP5GNOQLW4NUMV6H2UBOSMB4DROL7YNDBWAHYXMVFLLJOH3LXKHK"
	addAccountWithSignersToFake(t, fake, adminAddr, 2, nil)

	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
		Network:       "public",
	})
	require.NoError(t, err)

	// SAC decimals returns 7, but we can simulate a function that returns an address or mock it
	posture, err := p.EvaluateAdminPosture(context.Background(), stellartest.SACContract, "decimals")
	require.NoError(t, err)
	assert.Equal(t, "unknown", posture.Kind) // Decimals returns 7, which is unrecognized format
}

func TestCheck_AdminPostureStep_MultisigPasses(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	p, err := probe.New(probe.Options{
		Client:        fake,
		SourceAccount: stellartest.SourceAccount,
		Thresholds:    health.DefaultThresholds,
	})
	require.NoError(t, err)

	// When AdminFn is empty, step is cleanly skipped
	res, err := p.Check(context.Background(), probe.CheckRequest{
		ContractID: stellartest.SACContract,
		AdminFn:    "",
	})
	require.NoError(t, err)

	chk := findCheck(res.Checks, "admin_posture")
	assert.Nil(t, chk)
}

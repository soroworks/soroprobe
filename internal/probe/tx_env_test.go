package probe_test

import (
	"context"
	"testing"

	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func txFake(t *testing.T) (*stellartest.Fake, map[string]string) {
	t.Helper()
	fake := stellartest.NewFake(t)
	fake.Transactions = map[string]protocol.GetTransactionResponse{}
	hashes := map[string]string{}
	for _, name := range []string{"transaction_success.json", "transaction_failed.json", "transaction_feebump.json"} {
		var r protocol.GetTransactionResponse
		stellartest.Load(t, name, &r)
		fake.Transactions[r.TransactionHash] = r
		hashes[name] = r.TransactionHash
	}
	return fake, hashes
}

func TestTxExplainsASuccessfulCall(t *testing.T) {
	fake, hashes := txFake(t)
	r, err := newProber(t, fake, health.Thresholds{}).Tx(context.Background(), hashes["transaction_success.json"])
	require.NoError(t, err)

	assert.Equal(t, "SUCCESS", r.Status)
	assert.Nil(t, r.Failure)
	assert.Equal(t, "invoke_contract", r.HostFunction)
	assert.Equal(t, "approve", r.Function)
	assert.Equal(t, "CDDD5WEJY43NC6VOTTM23QRLHIB5O3CRNFHXA6ALTYM6V7ENKPYIE3EW", r.ContractID)
	require.Len(t, r.Args, 4)
	assert.Equal(t, "GDP77PIAML465XSK3JCMHOO755QTTIKKUK5IKK4HSIO7M5XXCZHV2VT2", r.Args[0])
	assert.False(t, r.ClosedAt.IsZero())

	require.Len(t, r.Events, 1)
	assert.Equal(t, "contract", r.Events[0].Type)
	assert.Equal(t, r.ContractID, r.Events[0].ContractID)
	assert.Equal(t, "approve", r.Events[0].Topics[0])

	require.NotNil(t, r.Resources)
	assert.Equal(t, uint32(801_750), r.Resources.Instructions)
	assert.Equal(t, int64(550_905), r.Resources.ResourceFee)
	require.NotNil(t, r.Resources.ResourceFeeCharged)
	assert.Equal(t, int64(475_467), *r.Resources.ResourceFeeCharged)
	assert.Equal(t, int64(475_567), r.Resources.FeeCharged)
}

func TestTxExplainsAFailure(t *testing.T) {
	fake, hashes := txFake(t)
	r, err := newProber(t, fake, health.Thresholds{}).Tx(context.Background(), hashes["transaction_failed.json"])
	require.NoError(t, err)
	assert.Equal(t, "FAILED", r.Status)
	require.NotNil(t, r.Failure)
	assert.Equal(t, "TxFailed", r.Failure.Result)
	assert.Equal(t, "Trapped", r.Failure.Operation)
	assert.Equal(t, "Error(Storage, ExceededLimit): trying to access contract data key outside of the footprint", r.Failure.Error)
	assert.Nil(t, r.ReturnValue)
}

func TestTxUnwrapsFeeBumps(t *testing.T) {
	fake, hashes := txFake(t)
	r, err := newProber(t, fake, health.Thresholds{}).Tx(context.Background(), hashes["transaction_feebump.json"])
	require.NoError(t, err)
	assert.True(t, r.FeeBump)
	assert.Equal(t, "set_price_stable", r.Function)
	require.NotNil(t, r.Resources)
	assert.Equal(t, 13, r.Resources.ReadWriteEntries)
}

func TestTxErrors(t *testing.T) {
	fake, _ := txFake(t)
	p := newProber(t, fake, health.Thresholds{})
	_, err := p.Tx(context.Background(), "abc")
	require.ErrorContains(t, err, "not a transaction hash")

	_, err = p.Tx(context.Background(), "00000000000000000000000000000000000000000000000000000000000000ff")
	require.ErrorIs(t, err, probe.ErrTxNotFound)
	assert.Contains(t, err.Error(), "the RPC keeps ledgers")
}

// --- environment version (#157) ---

func envWasm(t *testing.T, protocolVersion, pre uint32) []byte {
	t.Helper()
	entry, err := xdr.ScEnvMetaEntry{
		Kind:             xdr.ScEnvMetaKindScEnvMetaKindInterfaceVersion,
		InterfaceVersion: &xdr.ScEnvMetaEntryInterfaceVersion{Protocol: xdr.Uint32(protocolVersion), PreRelease: xdr.Uint32(pre)},
	}.MarshalBinary()
	require.NoError(t, err)
	name := []byte("contractenvmetav0")
	body := append([]byte{byte(len(name))}, name...) //nolint:gosec // test modules are tiny
	body = append(body, entry...)
	module := []byte{0, 'a', 's', 'm', 1, 0, 0, 0, 0, byte(len(body))} //nolint:gosec // test modules are tiny
	return append(module, body...)
}

func envCheck(t *testing.T, fake *stellartest.Fake) probe.Check {
	t.Helper()
	r, err := newProber(t, fake, health.Thresholds{}).Check(context.Background(), probe.CheckRequest{ContractID: stellartest.UndeployedContract})
	require.NoError(t, err)
	for _, c := range r.Checks {
		if c.Name == "env_version" {
			return c
		}
	}
	t.Fatalf("no env_version check in %+v", r.Checks)
	return probe.Check{}
}

func TestEnvVersionCheck(t *testing.T) {
	for _, tc := range []struct {
		name       string
		protocol   uint32
		pre        uint32
		network    int
		passphrase string
		want       probe.CheckOutcome
		detail     string
	}{
		{"compatible", 22, 0, 23, "", probe.OutcomePass, "built for protocol 22; the network runs protocol 23"},
		{"too new", 25, 0, 23, "", probe.OutcomeFail, "cannot run until the network upgrades"},
		{"pre-release on mainnet", 23, 1, 23, "Public Global Stellar Network ; September 2015", probe.OutcomeWarn, "pre-release build"},
		{"pre-release on testnet", 23, 1, 23, "", probe.OutcomePass, "pre-release 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := stellartest.NewFake(t)
			fake.DeployWasm(t, stellartest.UndeployedContract, envWasm(t, tc.protocol, tc.pre))
			fake.NetworkProtocol = tc.network
			fake.NetworkPassphrase = tc.passphrase
			c := envCheck(t, fake)
			assert.Equal(t, tc.want, c.Outcome, c.Detail)
			assert.Contains(t, c.Detail, tc.detail)
		})
	}
}

func TestEnvVersionCheckWithoutSection(t *testing.T) {
	fake := stellartest.NewFake(t)
	fake.DeployWasm(t, stellartest.UndeployedContract, []byte{0, 'a', 's', 'm', 1, 0, 0, 0})
	c := envCheck(t, fake)
	assert.Equal(t, probe.OutcomeWarn, c.Outcome)
	assert.Contains(t, c.Detail, "no contractenvmetav0")
}

func TestEnvVersionCheckSkipsTheSAC(t *testing.T) {
	r, err := newProber(t, stellartest.NewFake(t), health.Thresholds{}).Check(context.Background(),
		probe.CheckRequest{ContractID: stellartest.SACContract})
	require.NoError(t, err)
	var found bool
	for _, c := range r.Checks {
		if c.Name == "env_version" {
			found = true
			assert.Equal(t, probe.OutcomeSkip, c.Outcome)
		}
	}
	assert.True(t, found)
}

func TestEnvVersionCheckOnTheRecordedContract(t *testing.T) {
	// The recorded Wasm contract was built for protocol 22.
	r, err := newProber(t, stellartest.NewFake(t), health.Thresholds{}).Check(context.Background(),
		probe.CheckRequest{ContractID: stellartest.WasmContract})
	require.NoError(t, err)
	for _, c := range r.Checks {
		if c.Name == "env_version" {
			assert.Equal(t, probe.OutcomePass, c.Outcome)
			assert.Contains(t, c.Detail, "built for protocol 22")
			return
		}
	}
	t.Fatal("no env_version check")
}

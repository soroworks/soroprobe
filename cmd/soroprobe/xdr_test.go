package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/config"
	"github.com/soroworks/soroprobe/internal/probe"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func TestEncodeThenDecodeScVal(t *testing.T) {
	fake := stellartest.NewFake(t)
	out, err := runWithFake(t, fake, "encode", "scval", "u32:42")
	require.NoError(t, err)
	b64 := strings.TrimSpace(out)
	require.NotEmpty(t, b64)

	out, err = runWithFake(t, fake, "decode", "scval", b64)
	require.NoError(t, err)
	var got struct {
		Type  string `json:"type"`
		Value any    `json:"value"`
	}
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "scval", got.Type)
	assert.EqualValues(t, 42, got.Value)

	// Nothing reached the network.
	assert.Empty(t, fake.SimulateRequests)
	assert.Empty(t, fake.EntryRequests)
}

func TestEncodeScValJSON(t *testing.T) {
	out, err := runWithFake(t, stellartest.NewFake(t), "--json", "encode", "scval", "sym:hello")
	require.NoError(t, err)
	var got map[string]string
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	assert.Equal(t, "sym:hello", got["value"])
	assert.NotEmpty(t, got["xdr"])
}

func TestDecodeReadsStdinAndAutoDetects(t *testing.T) {
	fake := stellartest.NewFake(t)
	key := stellartest.InstanceKeyOf(t, stellartest.WasmContract)

	out, err := runWithFakeStdin(t, fake, key+"\n", "decode", "-")
	require.NoError(t, err)
	assert.Contains(t, out, `"type": "ledger-key"`)
	assert.Contains(t, out, stellartest.WasmContract)
}

func TestDecodeUnknownTypeFails(t *testing.T) {
	_, err := runWithFake(t, stellartest.NewFake(t), "decode", "nonsense", "AAAA")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown xdr type")
}

// runWithFakeStdin runs a command with stdin holding in. Only commands that
// never build a prober use it, so the fake is there to prove nothing reached
// the network.
func runWithFakeStdin(t *testing.T, fake *stellartest.Fake, in string, args ...string) (string, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	cmd, opts := newRootCmd()
	opts.newProber = func(config.Config) (*probe.Prober, func(), error) {
		p, err := probe.New(probe.Options{Client: fake, SourceAccount: stellartest.SourceAccount})
		return p, func() {}, err
	}
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetIn(strings.NewReader(in))
	cmd.SetArgs(args)
	err := cmd.Execute()
	return out.String(), err
}

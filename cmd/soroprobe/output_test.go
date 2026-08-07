package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/health"
	"github.com/soroworks/soroprobe/internal/probe"
)

func TestRenderSimulateSuccess(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderSimulate(&buf, &probe.SimulateResult{
		ContractID:   "CDLZFC3SYJYDZT7K67VZ75HPJVIEUVNIXF47ZG2FB2RMQQVU2HHGCYSC",
		Function:     "decimals",
		Success:      true,
		ReturnValue:  uint32(7),
		Cost:         probe.Cost{Instructions: 127569, MinResourceFee: 12279, ResourceFee: 12279},
		Footprint:    probe.Footprint{ReadOnly: []string{"contract instance CDLZ... (persistent)"}},
		LatestLedger: 4014125,
	})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "SUCCESS")
	assert.Contains(t, out, "decimals")
	assert.Contains(t, out, "7")
	assert.Contains(t, out, "127569")
	assert.Contains(t, out, "4014125")
	// Fees are shown in both stroops and XLM, since neither alone is obvious.
	assert.Contains(t, out, "12279 stroops")
	assert.Contains(t, out, "0.0012279 XLM")
}

func TestRenderSimulateFailureIndentsMultilineError(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderSimulate(&buf, &probe.SimulateResult{
		ContractID: "CDLZ",
		Function:   "no_such_fn",
		Success:    false,
		Error:      "HostError: Error(Value, InvalidInput)\n\nEvent log:\n  0: something",
	})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "FAILED")
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "HostError") || strings.Contains(line, "Event log") {
			assert.True(t, strings.HasPrefix(line, "  "), "error lines should be indented: %q", line)
		}
	}
	// A failed call has no return value to show.
	assert.NotContains(t, out, "return value")
}

func TestRenderSimulateRestoreRequired(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderSimulate(&buf, &probe.SimulateResult{
		ContractID:      "CDLZ",
		Function:        "balance",
		Success:         true,
		RestoreRequired: &probe.RestoreInfo{MinResourceFee: 5000},
	})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "RESTORE REQUIRED")
	assert.Contains(t, out, "archived")
	assert.Contains(t, out, "5000 stroops")
}

func TestRenderSimulateVoidReturn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderSimulate(&buf, &probe.SimulateResult{
		ContractID: "CDLZ", Function: "noop", Success: true, ReturnValue: nil,
	})
	require.NoError(t, err)
	assert.Contains(t, buf.String(), "(void)")
}

func TestRenderInspectWasmContract(t *testing.T) {
	t.Parallel()

	liveUntil := uint32(4073187)
	entry := func(status health.Status, summary string) probe.EntryReport {
		return probe.EntryReport{
			Kind: "instance",
			TTL: health.TTL{
				Present: true, Status: status, LiveUntilLedger: &liveUntil, LedgersRemaining: 59100,
			},
			Summary: summary,
		}
	}
	code := entry(health.StatusWarning, "live for 24456 more ledgers")

	var buf bytes.Buffer
	err := renderInspect(&buf, &probe.InspectResult{
		ContractID:   "CCLV77FYLRJMBGTILTKVIM76SI6JY56H7KTT47HE26UF4F265UYRDSR4",
		Deployed:     true,
		Executable:   "wasm",
		WasmHash:     "8608bb30bc798a05aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		Instance:     entry(health.StatusWarning, "live for 59100 more ledgers"),
		Code:         &code,
		Status:       health.StatusWarning,
		LatestLedger: 4014125,
	})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "wasm")
	assert.Contains(t, out, "instance")
	assert.Contains(t, out, "code")
	assert.Contains(t, out, "WARN")
	assert.Contains(t, out, "59100")
	assert.Contains(t, out, "24456")
}

func TestRenderInspectStellarAssetContractExplainsMissingCode(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderInspect(&buf, &probe.InspectResult{
		ContractID: "CDLZ",
		Deployed:   true,
		Executable: "stellar_asset",
		Instance: probe.EntryReport{
			TTL: health.TTL{Present: true, Status: health.StatusHealthy}, Summary: "live",
		},
		Code:   nil,
		Status: health.StatusHealthy,
	})
	require.NoError(t, err)

	// A missing code entry must be explained, not left as a blank.
	assert.Contains(t, buf.String(), "built into the host")
}

func TestRenderInspectNotDeployed(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderInspect(&buf, &probe.InspectResult{
		ContractID: "CCV2",
		Deployed:   false,
		Instance:   probe.EntryReport{TTL: health.TTL{Status: health.StatusMissing}},
		Status:     health.StatusMissing,
	})
	require.NoError(t, err)

	out := buf.String()
	assert.Contains(t, out, "NOT DEPLOYED")
	assert.NotContains(t, out, "instance storage")
}

func TestRenderCheck(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	err := renderCheck(&buf, &probe.CheckResult{
		ContractID: "CDLZ",
		OK:         false,
		Checks: []probe.Check{
			{Name: "deployed", Outcome: probe.OutcomePass, Detail: "instance found"},
			{Name: "instance_ttl", Outcome: probe.OutcomeWarn, Detail: "expiring soon"},
			{Name: "code_ttl", Outcome: probe.OutcomeSkip, Detail: "no code entry"},
			{Name: "simulate", Outcome: probe.OutcomeFail, Detail: "call failed"},
		},
		LatestLedger: 4014125,
	})
	require.NoError(t, err)

	out := buf.String()
	for _, want := range []string{"PASS", "WARN", "SKIP", "FAIL", "deployed", "instance_ttl", "4014125"} {
		assert.Contains(t, out, want)
	}
}

func TestJSONOutputIsMachineReadable(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	require.NoError(t, writeJSON(&buf, &probe.CheckResult{ContractID: "CDLZ", OK: true}))

	var round map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &round))
	assert.Equal(t, "CDLZ", round["contract_id"])
	assert.Equal(t, true, round["ok"])
}

func TestColorIsDisabledForNonTerminals(t *testing.T) {
	t.Parallel()

	// Output redirected to a file or a pipe, as in CI, must stay free of
	// escape codes.
	var buf bytes.Buffer
	err := renderCheck(&buf, &probe.CheckResult{
		ContractID: "CDLZ",
		OK:         true,
		Checks:     []probe.Check{{Name: "deployed", Outcome: probe.OutcomePass, Detail: "ok"}},
	})
	require.NoError(t, err)
	assert.NotContains(t, buf.String(), "\033[")
}

func TestStroopsConversion(t *testing.T) {
	t.Parallel()

	assert.Contains(t, stroops(10_000_000), "1.0000000 XLM")
	assert.Contains(t, stroops(1), "0.0000001 XLM")
	assert.Contains(t, stroops(0), "0 stroops")
}

func TestIndent(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "  a\n  b", indent("a\nb", "  "))
	assert.Equal(t, "  a", indent("a\n", "  "))
}

func TestParseDurability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    health.Durability
		wantErr bool
	}{
		{"", health.DurabilityPersistent, false},
		{"persistent", health.DurabilityPersistent, false},
		{"PERSISTENT", health.DurabilityPersistent, false},
		{"temporary", health.DurabilityTemporary, false},
		{"temp", health.DurabilityTemporary, false},
		{"forever", "", true},
	}

	for _, tt := range tests {
		got, err := parseDurability(tt.in)
		if tt.wantErr {
			require.Error(t, err, "input %q", tt.in)
			continue
		}
		require.NoError(t, err, "input %q", tt.in)
		assert.Equal(t, tt.want, got)
	}
}

func TestResolveVersionAlwaysReturnsSomething(t *testing.T) {
	assert.NotEmpty(t, resolveVersion())
}

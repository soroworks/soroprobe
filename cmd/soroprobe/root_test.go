package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/config"
)

// resolveConfig parses args and runs config resolution, without executing the
// command. It exists because --help short-circuits cobra before the persistent
// pre-run hook, so flag precedence cannot be observed through Execute.
func resolveConfig(t *testing.T, args ...string) config.Config {
	t.Helper()

	// Isolate from any soroprobe.json in the working tree.
	t.Chdir(t.TempDir())

	cmd, opts := newRootCmd()
	sub, rest, err := cmd.Find(args)
	require.NoError(t, err)
	require.NoError(t, sub.ParseFlags(rest))
	require.NoError(t, opts.resolve(sub))

	return opts.cfg
}

// runWith executes a command and returns its error. Every case here fails
// before any network call is attempted.
func runWith(t *testing.T, args ...string) error {
	t.Helper()

	t.Chdir(t.TempDir())

	cmd, _ := newRootCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)

	return cmd.Execute()
}

func TestFlagsOverrideEnvironment(t *testing.T) {
	t.Setenv(config.EnvRPCURL, "https://from-env.example.com")
	t.Setenv(config.EnvLogLevel, "error")

	cfg := resolveConfig(t, "inspect", "--rpc-url", "https://from-flag.example.com")

	assert.Equal(t, "https://from-flag.example.com", cfg.RPCURL, "a flag must beat the environment")
	assert.Equal(t, "error", cfg.LogLevel, "unset flags leave the environment value alone")
}

func TestEnvironmentUsedWhenNoFlags(t *testing.T) {
	t.Setenv(config.EnvRPCURL, "https://from-env.example.com")

	cfg := resolveConfig(t, "inspect")
	assert.Equal(t, "https://from-env.example.com", cfg.RPCURL)
}

func TestAllPersistentFlagsAreApplied(t *testing.T) {
	cfg := resolveConfig(t, "inspect",
		"--rpc-url", "https://rpc.example.com",
		"--network-passphrase", "Custom Network",
		"--source-account", "GAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAWHF",
		"--log-level", "debug",
		"--timeout", "12s",
		"--warn-ledgers", "999",
		"--critical-ledgers", "99",
	)

	assert.Equal(t, "https://rpc.example.com", cfg.RPCURL)
	assert.Equal(t, "Custom Network", cfg.NetworkPassphrase)
	assert.Equal(t, "debug", cfg.LogLevel)
	assert.Equal(t, 12*time.Second, cfg.Timeout)
	assert.EqualValues(t, 999, cfg.WarnLedgers)
	assert.EqualValues(t, 99, cfg.CriticalLedgers)
}

func TestInvalidConfigIsRejectedBeforeAnyNetworkCall(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"bad source account", []string{"inspect", "CDLZ", "--source-account", "not-a-key"}},
		{"secret key as source account", []string{
			"inspect", "CDLZ",
			"--source-account", "SAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		}},
		{"bad log level", []string{"inspect", "CDLZ", "--log-level", "chatty"}},
		{"rpc url without scheme", []string{"inspect", "CDLZ", "--rpc-url", "rpc.example.com"}},
		{"critical above warn", []string{"inspect", "CDLZ", "--warn-ledgers", "10", "--critical-ledgers", "100"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, runWith(t, tt.args...))
		})
	}
}

func TestVersionCommandNeedsNoConfig(t *testing.T) {
	// `version` must work even when the configuration is unusable, so that it
	// stays useful in a bug report.
	t.Setenv(config.EnvRPCURL, "not-a-url")
	t.Chdir(t.TempDir())

	cmd, _ := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"version"})

	require.NoError(t, cmd.Execute())
	assert.NotEmpty(t, out.String())
}

func TestCommandArgumentValidation(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"simulate needs a function", []string{"simulate", "CDLZ"}},
		{"simulate needs a contract", []string{"simulate"}},
		{"inspect needs a contract", []string{"inspect"}},
		{"inspect takes only one contract", []string{"inspect", "CDLZ", "CDEF"}},
		{"check needs a contract", []string{"check"}},
		{"serve takes no arguments", []string{"serve", "extra"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Error(t, runWith(t, tt.args...))
		})
	}
}

func TestUnknownDurabilityIsRejected(t *testing.T) {
	err := runWith(t, "inspect", "CDLZ", "--durability", "forever")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "durability")
}

func TestProberRequiresValidConfig(t *testing.T) {
	opts := &options{cfg: config.Default()}
	p, cleanup, err := opts.prober()
	require.NoError(t, err)
	require.NotNil(t, p)
	cleanup()
}

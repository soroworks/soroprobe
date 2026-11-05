package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar/stellartest"
)

func buildWasmWithMeta(t *testing.T, kv map[string]string) []byte {
	t.Helper()
	var metaBuf bytes.Buffer
	for k, v := range kv {
		entry := xdr.ScMetaEntry{
			Kind: xdr.ScMetaKindScMetaV0,
			V0:   &xdr.ScMetaV0{Key: k, Val: v},
		}
		raw, err := entry.MarshalBinary()
		require.NoError(t, err)
		metaBuf.Write(raw)
	}

	name := "contractmetav0"
	var secPayload bytes.Buffer
	secPayload.WriteByte(byte(len(name)))
	secPayload.WriteString(name)
	secPayload.Write(metaBuf.Bytes())

	var wasm bytes.Buffer
	wasm.Write([]byte{0x00, 'a', 's', 'm', 0x01, 0x00, 0x00, 0x00})
	wasm.WriteByte(0)
	v := uint32(secPayload.Len())
	for {
		c := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			c |= 0x80
		}
		wasm.WriteByte(c)
		if v == 0 {
			break
		}
	}
	wasm.Write(secPayload.Bytes())
	return wasm.Bytes()
}

func TestVerifyReadsMetaAndReportsToolchainAndImage(t *testing.T) {
	t.Parallel()

	wasmBytes := buildWasmWithMeta(t, map[string]string{
		"rsver":       "1.89.0",
		"rssdkver":    "23.0.1#abc123",
		"source_repo": "https://github.com/soroworks/example-contract",
		"source_rev":  "0123456789abcdef0123456789abcdef01234567",
	})
	meta, err := abi.ParseWasmBuildMeta(wasmBytes)
	require.NoError(t, err)
	assert.Equal(t, "1.89.0", meta.RustVersion)
	assert.Equal(t, "23.0.1#abc123", meta.SDKVersion)
	assert.Equal(t, "https://github.com/soroworks/example-contract", meta.SourceRepo)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", meta.SourceRev)

	tmpFile := filepath.Join(t.TempDir(), "contract.wasm")
	require.NoError(t, os.WriteFile(tmpFile, wasmBytes, 0o600))

	cmd, opts := newRootCmd()
	var captured VerifyBuildSpec
	opts.verifyBuilder = func(_ context.Context, spec VerifyBuildSpec) (string, error) {
		captured = spec
		return meta.WasmHash, nil
	}

	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"verify", tmpFile, "--json"})
	require.NoError(t, cmd.Execute())

	assert.Equal(t, "https://github.com/soroworks/example-contract", captured.SourceURL)
	assert.Equal(t, "0123456789abcdef0123456789abcdef01234567", captured.Revision)
	assert.Equal(t, "rust:1.89.0-slim", captured.Image)

	var report VerifyReport
	require.NoError(t, json.Unmarshal(out.Bytes(), &report))
	assert.True(t, report.Verified)
	assert.Equal(t, "rust:1.89.0-slim", report.BuilderImage)
	assert.Contains(t, report.Toolchain, "rustc 1.89.0")
	assert.Contains(t, report.Toolchain, "soroban-sdk 23.0.1#abc123")
	assert.Equal(t, meta.WasmHash, report.LiveWasmHash)
	assert.Equal(t, meta.WasmHash, report.BuiltWasmHash)
}

func TestVerifyAgainstLiveFixtureAndMismatchFails(t *testing.T) {
	t.Parallel()

	fake := stellartest.NewFake(t)
	liveMeta, err := abi.FetchLiveBuildMeta(context.Background(), fake, stellartest.WasmContract)
	require.NoError(t, err)
	require.NotEmpty(t, liveMeta.WasmHash)

	t.Run("matching hash succeeds", func(t *testing.T) {
		cmd, opts := newRootCmd()
		opts.verifyClient = fake
		opts.verifyBuilder = func(_ context.Context, _ VerifyBuildSpec) (string, error) {
			return liveMeta.WasmHash, nil
		}
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{
			"verify", stellartest.WasmContract,
			"--source", "https://github.com/soroworks/token",
			"--rev", "abcdef1234567890abcdef1234567890abcdef12",
			"--image", "rust:1.85.0-slim",
		})
		require.NoError(t, cmd.Execute())
		assert.Contains(t, out.String(), "VERIFIED")
		assert.Contains(t, out.String(), "rust:1.85.0-slim")
	})

	t.Run("mismatching hash exits with checkFailedError", func(t *testing.T) {
		cmd, opts := newRootCmd()
		opts.verifyClient = fake
		opts.verifyBuilder = func(_ context.Context, _ VerifyBuildSpec) (string, error) {
			return "0000000000000000000000000000000000000000000000000000000000000000", nil
		}
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{
			"verify", stellartest.WasmContract,
			"--source", "https://github.com/soroworks/token",
			"--rev", "abcdef1234567890abcdef1234567890abcdef12",
		})
		err := cmd.Execute()
		require.Error(t, err)
		assert.Contains(t, out.String(), "MISMATCH")
	})
}

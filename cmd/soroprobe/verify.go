package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/soroworks/soroprobe/internal/abi"
	"github.com/soroworks/soroprobe/internal/stellar"
)

// DefaultVerifyBaseImage is the default container repository used when
// --image is not provided. The tag is pinned to the contract's Rust compiler
// version from contractmetav0 when available.
const DefaultVerifyBaseImage = "rust"

// DefaultRustVersion is used when neither --rust-version nor contractmetav0
// specifies a compiler version.
const DefaultRustVersion = "1.84.0"

// VerifyBuildSpec describes a reproducible container build invocation.
type VerifyBuildSpec struct {
	SourceURL   string
	Revision    string
	Package     string
	Image       string
	RustVersion string
}

// VerifyBuilder builds a contract from source and returns the SHA-256 hex digest
// of the resulting Wasm artifact. Tests replace it to exercise verification
// without requiring Docker or network access.
type VerifyBuilder func(ctx context.Context, spec VerifyBuildSpec) (wasmHash string, err error)

// VerifyReport is the outcome of `soroprobe verify`.
type VerifyReport struct {
	ContractID    string            `json:"contract_id"`
	Verified      bool              `json:"verified"`
	SourceURL     string            `json:"source_url"`
	Revision      string            `json:"revision"`
	Package       string            `json:"package,omitempty"`
	BuilderImage  string            `json:"builder_image"`
	Toolchain     string            `json:"toolchain"`
	RustVersion   string            `json:"rust_version,omitempty"`
	SDKVersion    string            `json:"sdk_version,omitempty"`
	LiveWasmHash  string            `json:"live_wasm_hash"`
	BuiltWasmHash string            `json:"built_wasm_hash"`
	Meta          map[string]string `json:"meta,omitempty"`
}

func newVerifyCmd(opts *options) *cobra.Command {
	var (
		sourceURL string
		revision  string
		image     string
		pkg       string
		builtWasm string
	)

	cmd := &cobra.Command{
		Use:   "verify <contract>",
		Short: "Verify a deployed contract against a reproducible build of its source",
		Long: `Verify that a deployed contract's live Wasm matches a reproducible build of
its source repository and commit.

When --source or --rev are omitted, SoroProbe reads the source repository and
commit revision from the contract's "contractmetav0" custom section if present.
The report always states the exact toolchain and container image used.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			meta, err := loadLiveContractMeta(cmd.Context(), opts, args[0])
			if err != nil {
				return err
			}

			resolvedSource := strings.TrimSpace(sourceURL)
			if resolvedSource == "" {
				resolvedSource = meta.SourceRepo
			}
			if resolvedSource == "" {
				return fmt.Errorf("no source repository specified: pass --source <git-url> (contractmetav0 has no source_repo entry)")
			}

			resolvedRev := strings.TrimSpace(revision)
			if resolvedRev == "" {
				resolvedRev = meta.SourceRev
			}
			if resolvedRev == "" {
				return fmt.Errorf("no commit revision specified: pass --rev <commit> (contractmetav0 has no source_rev entry)")
			}

			rustVer := meta.RustVersion
			if idx := strings.IndexByte(rustVer, '#'); idx > 0 {
				rustVer = rustVer[:idx]
			}
			if rustVer == "" {
				rustVer = DefaultRustVersion
			}

			resolvedImage := strings.TrimSpace(image)
			if resolvedImage == "" {
				resolvedImage = fmt.Sprintf("%s:%s-slim", DefaultVerifyBaseImage, rustVer)
			}
			toolchain := fmt.Sprintf("rustc %s (target wasm32v1-none / wasm32-unknown-unknown)", rustVer)
			if meta.SDKVersion != "" {
				toolchain += fmt.Sprintf(", soroban-sdk %s", meta.SDKVersion)
			}

			var builtHash string
			if builtWasm != "" {
				raw, err := os.ReadFile(builtWasm) //nolint:gosec // operator-supplied path
				if err != nil {
					return fmt.Errorf("reading built wasm %s: %w", builtWasm, err)
				}
				sum := sha256.Sum256(raw)
				builtHash = hex.EncodeToString(sum[:])
			} else {
				builder := opts.verifyBuilder
				if builder == nil {
					builder = buildInContainer
				}
				builtHash, err = builder(cmd.Context(), VerifyBuildSpec{
					SourceURL:   resolvedSource,
					Revision:    resolvedRev,
					Package:     strings.TrimSpace(pkg),
					Image:       resolvedImage,
					RustVersion: rustVer,
				})
				if err != nil {
					return err
				}
			}

			report := VerifyReport{
				ContractID:    args[0],
				Verified:      strings.EqualFold(meta.WasmHash, builtHash),
				SourceURL:     resolvedSource,
				Revision:      resolvedRev,
				Package:       strings.TrimSpace(pkg),
				BuilderImage:  resolvedImage,
				Toolchain:     toolchain,
				RustVersion:   meta.RustVersion,
				SDKVersion:    meta.SDKVersion,
				LiveWasmHash:  meta.WasmHash,
				BuiltWasmHash: strings.ToLower(builtHash),
				Meta:          meta.Entries,
			}

			if opts.jsonOut {
				if err := writeJSON(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			} else {
				if err := renderVerify(cmd.OutOrStdout(), report); err != nil {
					return err
				}
			}

			if !report.Verified {
				return &checkFailedError{
					msg: fmt.Sprintf("wasm hash mismatch: live %s != built %s", report.LiveWasmHash, report.BuiltWasmHash),
				}
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&sourceURL, "source", "", "git repository URL (defaults to contractmetav0 source_repo)")
	cmd.Flags().StringVar(&revision, "rev", "", "git commit or tag to build (defaults to contractmetav0 source_rev)")
	cmd.Flags().StringVar(&image, "image", "", "pinned container image for the reproducible build (default: rust:<rsver>-slim)")
	cmd.Flags().StringVar(&pkg, "package", "", "cargo package name when the repository is a workspace")
	cmd.Flags().StringVar(&builtWasm, "wasm", "", "compare against an already-built .wasm file instead of invoking a container")
	return cmd
}

func loadLiveContractMeta(ctx context.Context, opts *options, target string) (*abi.BuildMeta, error) {
	if info, err := os.Stat(target); err == nil && !info.IsDir() {
		raw, err := os.ReadFile(target) //nolint:gosec // CLI operator-supplied path
		if err != nil {
			return nil, err
		}
		return abi.ParseWasmBuildMeta(raw)
	}
	if opts.verifyClient != nil {
		return abi.FetchLiveBuildMeta(ctx, opts.verifyClient, target)
	}
	log := opts.cfg.Logger(os.Stderr)
	client, err := stellar.New(stellar.Options{
		URL:     opts.cfg.RPCURL,
		Timeout: opts.cfg.Timeout,
		Logger:  log,
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()
	return abi.FetchLiveBuildMeta(ctx, client, target)
}

func buildInContainer(ctx context.Context, spec VerifyBuildSpec) (string, error) {
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		return "", fmt.Errorf("docker is required for container builds: %w", err)
	}

	outDir, err := os.MkdirTemp("", "soroprobe-verify-*")
	if err != nil {
		return "", fmt.Errorf("creating temporary build directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(outDir) }()

	pkgFlag := ""
	if spec.Package != "" {
		pkgFlag = "-p " + spec.Package
	}

	script := fmt.Sprintf(
		"set -euo pipefail; "+
			"apt-get update -qq && apt-get install -y -qq git ca-certificates >/dev/null; "+
			"rustup target add wasm32-unknown-unknown >/dev/null 2>&1 || true; "+
			"git clone --quiet %q /work/src && cd /work/src && git checkout --quiet %q; "+
			"export SOURCE_DATE_EPOCH=0 CARGO_INCREMENTAL=0 RUSTFLAGS='--remap-path-prefix=/work/src=. -C link-arg=-zstack-size=65536'; "+
			"cargo build --target wasm32-unknown-unknown --release --locked %s; "+
			"cp target/wasm32-unknown-unknown/release/*.wasm /out/built.wasm",
		spec.SourceURL, spec.Revision, pkgFlag,
	)

	// #nosec G204 -- arguments are controlled and passed as discrete argv elements to docker.
	cmd := exec.CommandContext(ctx, dockerPath,
		"run", "--rm",
		"-v", outDir+":/out",
		spec.Image,
		"sh", "-c", script,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("container build failed in %s: %w\n%s", spec.Image, err, strings.TrimSpace(string(output)))
	}

	built, err := os.ReadFile(filepath.Join(outDir, "built.wasm")) //nolint:gosec // temp dir owned by this process
	if err != nil {
		return "", fmt.Errorf("reading built wasm artifact: %w", err)
	}
	sum := sha256.Sum256(built)
	return hex.EncodeToString(sum[:]), nil
}

func renderVerify(w io.Writer, r VerifyReport) error {
	s := styler{on: colorEnabled(w)}
	bw := &bufWriter{w: w}

	bw.printf("%s %s\n", s.bold("contract     "), r.ContractID)
	bw.printf("%s %s\n", s.bold("source       "), r.SourceURL)
	bw.printf("%s %s\n", s.bold("revision     "), r.Revision)
	bw.printf("%s %s\n", s.bold("builder image"), r.BuilderImage)
	bw.printf("%s %s\n", s.bold("toolchain    "), r.Toolchain)
	bw.printf("%s %s\n", s.bold("live wasm    "), r.LiveWasmHash)
	bw.printf("%s %s\n\n", s.bold("built wasm   "), r.BuiltWasmHash)

	bw.printf("%s ", s.bold("result"))
	if r.Verified {
		bw.printf("%s\n", s.green("VERIFIED  built wasm matches live contract code"))
	} else {
		bw.printf("%s\n", s.red("MISMATCH  built wasm does not match live contract code"))
	}
	return bw.err
}

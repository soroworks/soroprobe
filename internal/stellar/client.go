// Package stellar wraps the Stellar RPC endpoints SoroProbe depends on.
//
// The Client interface is deliberately expressed in terms of the SDK's own
// protocol types. Those types are plain structs that unmarshal directly from a
// JSON-RPC "result" object, which lets tests replay responses recorded from a
// real network without any hand-translation in between.
//
// SoroProbe is read-only: this package intentionally exposes no way to submit a
// transaction. See BuildInvocationTx for why signing is never required.
package stellar

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/stellar/go-stellar-sdk/clients/rpcclient"
	protocol "github.com/stellar/go-stellar-sdk/protocols/rpc"
)

// Client is the subset of Stellar RPC that SoroProbe uses.
//
// Implementations must be safe for concurrent use. To add another RPC method,
// add it here and to both implementations (RPCClient and the test fake).
type Client interface {
	// SimulateTransaction runs a trial execution of an invocation without
	// submitting it to the network.
	SimulateTransaction(ctx context.Context, req protocol.SimulateTransactionRequest) (protocol.SimulateTransactionResponse, error)
	// GetLedgerEntries reads ledger entries by base64 LedgerKey.
	GetLedgerEntries(ctx context.Context, req protocol.GetLedgerEntriesRequest) (protocol.GetLedgerEntriesResponse, error)
	// GetLatestLedger reports the most recently closed ledger.
	GetLatestLedger(ctx context.Context) (protocol.GetLatestLedgerResponse, error)
	// GetLedgers reports close times for a sequence range.
	GetLedgers(ctx context.Context, req protocol.GetLedgersRequest) (protocol.GetLedgersResponse, error)
	// GetNetwork reports the passphrase and protocol version of the network.
	GetNetwork(ctx context.Context) (protocol.GetNetworkResponse, error)
	// GetHealth reports RPC server health and its retention window.
	GetHealth(ctx context.Context) (protocol.GetHealthResponse, error)
}

// RPCClient is the live Client, backed by the Stellar RPC JSON-RPC API.
type RPCClient struct {
	inner     *rpcclient.Client
	timeout   time.Duration
	log       *slog.Logger
	retries   int
	baseDelay time.Duration
}

// compile-time proof that both the SDK client and our wrapper satisfy Client.
var (
	_ Client = (*RPCClient)(nil)
	_ Client = (*rpcclient.Client)(nil)
)

// Options configures an RPCClient.
type Options struct {
	// URL is the Stellar RPC endpoint. Required.
	URL string
	// Timeout bounds each individual request. Defaults to 30s.
	Timeout time.Duration
	// Logger receives debug-level request tracing. Defaults to a discarding logger.
	Logger *slog.Logger
	// HTTPClient overrides the underlying transport, mostly for tests.
	HTTPClient *http.Client
	// Retries is the maximum number of times transient failures are retried.
	Retries int
	// BaseDelay is the starting delay for exponential backoff. Defaults to 100ms.
	BaseDelay time.Duration
}

// New builds a live RPC-backed Client.
func New(opts Options) (*RPCClient, error) {
	if opts.URL == "" {
		return nil, fmt.Errorf("stellar: rpc url is required")
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 30 * time.Second
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.BaseDelay <= 0 {
		opts.BaseDelay = 100 * time.Millisecond
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: opts.Timeout}
	}
	return &RPCClient{
		inner:     rpcclient.NewClient(opts.URL, httpClient),
		timeout:   opts.Timeout,
		log:       opts.Logger,
		retries:   opts.Retries,
		baseDelay: opts.BaseDelay,
	}, nil
}

// Close releases resources held by the underlying client.
func (c *RPCClient) Close() error { return c.inner.Close() }

func (c *RPCClient) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.timeout)
}

var httpStatusRegex = regexp.MustCompile(`unexpected HTTP status (\d{3})`)

// isTransient reports whether an RPC error is transient and safe to retry.
func isTransient(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()

	// Check HTTP status code
	if m := httpStatusRegex.FindStringSubmatch(msg); len(m) > 1 {
		code, _ := strconv.Atoi(m[1])
		if code == http.StatusTooManyRequests || (code >= 500 && code <= 599) {
			return true
		}
		return false
	}

	// Transport/connection errors are wrapped by the SDK under JSON-RPC -32603:
	// e.g. "Post ...: dial tcp ...: connect: connection refused"
	if strings.Contains(msg, "connect: connection refused") ||
		strings.Contains(msg, "connection reset by peer") ||
		strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "temporary failure in name resolution") ||
		strings.Contains(msg, "TLS handshake timeout") {
		return true
	}

	return false
}

func (c *RPCClient) retry(ctx context.Context, method string, fn func(reqCtx context.Context) error) error {
	var err error
	for attempt := 0; attempt <= c.retries; attempt++ {
		if attempt > 0 {
			// Exponential backoff with jitter: baseDelay * 2^(attempt-1) + random jitter
			backoff := c.baseDelay * (1 << (attempt - 1))
			jitter := time.Duration(rand.Int63n(int64(c.baseDelay))) //nolint:gosec // jitter doesn't need crypto rand
			sleepDur := backoff + jitter

			c.log.Debug("retrying transient rpc failure", "method", method, "attempt", attempt, "max_retries", c.retries, "delay", sleepDur, "err", err)

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleepDur):
			}
		}

		reqCtx, cancel := c.withTimeout(ctx)
		err = fn(reqCtx)
		cancel()

		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if !isTransient(err) {
			return err
		}
	}
	return err
}

// SimulateTransaction implements Client.
func (c *RPCClient) SimulateTransaction(ctx context.Context, req protocol.SimulateTransactionRequest) (protocol.SimulateTransactionResponse, error) {
	var resp protocol.SimulateTransactionResponse
	start := time.Now()
	err := c.retry(ctx, "simulateTransaction", func(reqCtx context.Context) error {
		var callErr error
		resp, callErr = c.inner.SimulateTransaction(reqCtx, req)
		return callErr
	})
	c.log.Debug("simulateTransaction", "took", time.Since(start), "err", err, "sim_error", resp.Error)
	if err != nil {
		return resp, fmt.Errorf("simulateTransaction: %w", err)
	}
	return resp, nil
}

// GetLedgerEntries implements Client.
func (c *RPCClient) GetLedgerEntries(ctx context.Context, req protocol.GetLedgerEntriesRequest) (protocol.GetLedgerEntriesResponse, error) {
	var resp protocol.GetLedgerEntriesResponse
	start := time.Now()
	err := c.retry(ctx, "getLedgerEntries", func(reqCtx context.Context) error {
		var callErr error
		resp, callErr = c.inner.GetLedgerEntries(reqCtx, req)
		return callErr
	})
	c.log.Debug("getLedgerEntries", "keys", len(req.Keys), "found", len(resp.Entries), "took", time.Since(start), "err", err)
	if err != nil {
		return resp, fmt.Errorf("getLedgerEntries: %w", err)
	}
	return resp, nil
}

// GetLatestLedger implements Client.
func (c *RPCClient) GetLatestLedger(ctx context.Context) (protocol.GetLatestLedgerResponse, error) {
	var resp protocol.GetLatestLedgerResponse
	err := c.retry(ctx, "getLatestLedger", func(reqCtx context.Context) error {
		var callErr error
		resp, callErr = c.inner.GetLatestLedger(reqCtx)
		return callErr
	})
	if err != nil {
		return resp, fmt.Errorf("getLatestLedger: %w", err)
	}
	return resp, nil
}

// GetLedgers implements Client.
func (c *RPCClient) GetLedgers(ctx context.Context, req protocol.GetLedgersRequest) (protocol.GetLedgersResponse, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	resp, err := c.inner.GetLedgers(ctx, req)
	if err != nil {
		return resp, fmt.Errorf("getLedgers: %w", err)
	}
	return resp, nil
}

// GetNetwork implements Client.
func (c *RPCClient) GetNetwork(ctx context.Context) (protocol.GetNetworkResponse, error) {
	var resp protocol.GetNetworkResponse
	err := c.retry(ctx, "getNetwork", func(reqCtx context.Context) error {
		var callErr error
		resp, callErr = c.inner.GetNetwork(reqCtx)
		return callErr
	})
	if err != nil {
		return resp, fmt.Errorf("getNetwork: %w", err)
	}
	return resp, nil
}

// GetHealth implements Client.
func (c *RPCClient) GetHealth(ctx context.Context) (protocol.GetHealthResponse, error) {
	var resp protocol.GetHealthResponse
	err := c.retry(ctx, "getHealth", func(reqCtx context.Context) error {
		var callErr error
		resp, callErr = c.inner.GetHealth(reqCtx)
		return callErr
	})
	if err != nil {
		return resp, fmt.Errorf("getHealth: %w", err)
	}
	return resp, nil
}

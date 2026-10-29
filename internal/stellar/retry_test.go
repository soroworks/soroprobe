package stellar_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/soroworks/soroprobe/internal/stellar"
)

func TestRPCClientRetryTransientFailures(t *testing.T) {
	t.Parallel()

	t.Run("fails N times with 503 then succeeds", func(t *testing.T) {
		t.Parallel()
		var attempts int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			att := atomic.AddInt32(&attempts, 1)
			if att <= 2 {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			var req struct {
				ID any `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"status": "healthy"},
			})
		}))
		defer srv.Close()

		client, err := stellar.New(stellar.Options{
			URL:       srv.URL,
			Retries:   3,
			BaseDelay: 5 * time.Millisecond,
		})
		require.NoError(t, err)
		defer client.Close()

		resp, err := client.GetHealth(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "healthy", resp.Status)
		assert.EqualValues(t, 3, atomic.LoadInt32(&attempts))
	})

	t.Run("fails N times with 429 then succeeds", func(t *testing.T) {
		t.Parallel()
		var attempts int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			att := atomic.AddInt32(&attempts, 1)
			if att <= 1 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			var req struct {
				ID any `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"status": "healthy"},
			})
		}))
		defer srv.Close()

		client, err := stellar.New(stellar.Options{
			URL:       srv.URL,
			Retries:   2,
			BaseDelay: 5 * time.Millisecond,
		})
		require.NoError(t, err)
		defer client.Close()

		resp, err := client.GetHealth(context.Background())
		require.NoError(t, err)
		assert.Equal(t, "healthy", resp.Status)
		assert.EqualValues(t, 2, atomic.LoadInt32(&attempts))
	})

	t.Run("non-transient JSON-RPC error is not retried", func(t *testing.T) {
		t.Parallel()
		var attempts int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			var req struct {
				ID any `json:"id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"error":   map[string]any{"code": -32602, "message": "Invalid params"},
			})
		}))
		defer srv.Close()

		client, err := stellar.New(stellar.Options{
			URL:       srv.URL,
			Retries:   3,
			BaseDelay: 5 * time.Millisecond,
		})
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetHealth(context.Background())
		require.Error(t, err)
		assert.EqualValues(t, 1, atomic.LoadInt32(&attempts), "should only attempt once for non-transient error")
	})

	t.Run("retries=0 disables retrying", func(t *testing.T) {
		t.Parallel()
		var attempts int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&attempts, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		client, err := stellar.New(stellar.Options{
			URL:       srv.URL,
			Retries:   0,
			BaseDelay: 5 * time.Millisecond,
		})
		require.NoError(t, err)
		defer client.Close()

		_, err = client.GetHealth(context.Background())
		require.Error(t, err)
		assert.EqualValues(t, 1, atomic.LoadInt32(&attempts))
	})

	t.Run("respects context cancellation during retry backoff", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		client, err := stellar.New(stellar.Options{
			URL:       srv.URL,
			Retries:   5,
			BaseDelay: 500 * time.Millisecond,
		})
		require.NoError(t, err)
		defer client.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		_, err = client.GetHealth(ctx)
		require.Error(t, err)
		assert.ErrorIs(t, err, context.DeadlineExceeded)
	})
}

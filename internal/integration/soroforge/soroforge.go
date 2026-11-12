package soroforge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// maxResponseBytes bounds the response read from SoroForge to 4MB.
const maxResponseBytes = 4 << 20

// TrackedContract represents a contract deployment recorded in SoroForge.
type TrackedContract struct {
	ID         string `json:"id,omitempty"`
	ContractID string `json:"contract_id"`
	Name       string `json:"name,omitempty"`
	Network    string `json:"network,omitempty"`
	WasmHash   string `json:"wasm_hash,omitempty"`
}

// Client fetches tracked contracts from SoroForge.
type Client interface {
	ListContracts(ctx context.Context, network string) ([]TrackedContract, error)
}

// HTTPClient is the concrete implementation of Client talking to SoroForge's REST API.
type HTTPClient struct {
	baseURL string
	token   string
	http    *http.Client
}

var _ Client = (*HTTPClient)(nil)

// New constructs an HTTPClient.
func New(baseURL, token string, client *http.Client) (*HTTPClient, error) {
	u, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("soroforge url %q must be an absolute http:// or https:// URL", baseURL)
	}
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPClient{
		baseURL: u.String(),
		token:   strings.TrimSpace(token),
		http:    client,
	}, nil
}

// ListContracts retrieves all contracts tracked by SoroForge, filtering by network if specified.
// Handles pagination and clear authentication error reporting.
func (c *HTTPClient) ListContracts(ctx context.Context, network string) ([]TrackedContract, error) {
	var all []TrackedContract
	page := 1

	for {
		endpoint := fmt.Sprintf("%s/api/v1/contracts?page=%d", c.baseURL, page)
		if network != "" {
			endpoint += "&network=" + url.QueryEscape(network)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "application/json")
		if c.token != "" {
			req.Header.Set("Authorization", "Bearer "+c.token)
		}

		resp, err := c.http.Do(req)
		if err != nil {
			return nil, fmt.Errorf("soroforge: %w", err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return nil, fmt.Errorf("soroforge: reading response: %w", err)
		}

		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			msg := errorMessage(body, resp.Status)
			return nil, fmt.Errorf("soroforge: authentication failed (%s): %s", resp.Status, msg)
		}
		if resp.StatusCode != http.StatusOK {
			msg := errorMessage(body, resp.Status)
			return nil, fmt.Errorf("soroforge: request failed with status %s: %s", resp.Status, msg)
		}

		var pageResp struct {
			Contracts []TrackedContract `json:"contracts"`
			HasMore   bool              `json:"has_more"`
			NextPage  int               `json:"next_page"`
			Total     int               `json:"total"`
		}

		if err := json.Unmarshal(body, &pageResp); err == nil && pageResp.Contracts != nil {
			all = append(all, pageResp.Contracts...)
			if !pageResp.HasMore || pageResp.NextPage <= page || pageResp.NextPage == 0 {
				break
			}
			page = pageResp.NextPage
			continue
		}

		// Fallback for direct JSON array responses
		var rawList []TrackedContract
		if err := json.Unmarshal(body, &rawList); err == nil {
			all = append(all, rawList...)
			break
		}

		return nil, fmt.Errorf("soroforge: decoding response: %s", string(body))
	}

	for i := range all {
		if all[i].ContractID == "" && all[i].ID != "" {
			all[i].ContractID = all[i].ID
		}
	}

	return all, nil
}

func errorMessage(body []byte, status string) string {
	var e struct {
		Error   string `json:"error"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(body, &e); err == nil {
		if e.Error != "" {
			return e.Error
		}
		if e.Message != "" {
			return e.Message
		}
	}
	if len(body) > 0 && len(body) < 200 {
		return string(body)
	}
	return status
}

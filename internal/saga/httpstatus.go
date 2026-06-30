package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPStatusChecker queries a service's statusUrl over HTTP for the canonical
// order status (white paper section 11.2). The provider is expected to expose
// GET {statusUrl}/{orderId} returning {"status":"DONE|NOT_DONE|..."}.
type HTTPStatusChecker struct {
	client *http.Client
}

// NewHTTPStatusChecker builds a checker with a bounded timeout.
func NewHTTPStatusChecker(timeout time.Duration) *HTTPStatusChecker {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &HTTPStatusChecker{client: &http.Client{Timeout: timeout}}
}

type statusResponse struct {
	Status string `json:"status"`
}

func (c *HTTPStatusChecker) CheckStatus(ctx context.Context, statusURL, orderID string) (ProviderStatus, error) {
	if statusURL == "" {
		// No statusUrl configured: cannot determine — stay conservative.
		return ProviderUnknown, nil
	}
	endpoint, err := url.JoinPath(statusURL, orderID)
	if err != nil {
		return ProviderUnknown, fmt.Errorf("build status url: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ProviderUnknown, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		// Network error: unknown, never compensate on this alone.
		return ProviderUnknown, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ProviderUnknown, fmt.Errorf("status endpoint returned %d", resp.StatusCode)
	}

	var sr statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&sr); err != nil {
		return ProviderUnknown, fmt.Errorf("decode status: %w", err)
	}
	return normalizeStatus(sr.Status), nil
}

// normalizeStatus maps provider status strings onto ProviderStatus. Accepts a
// few common spellings; anything unrecognized is UNKNOWN (conservative).
func normalizeStatus(s string) ProviderStatus {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "DONE", "SUCCESS", "COMPLETED":
		return ProviderDone
	case "NOT_DONE", "FAILED", "NOTFOUND", "NOT_FOUND":
		return ProviderNotDone
	default:
		return ProviderUnknown
	}
}

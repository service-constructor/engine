package saga

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// HTTPExecutor calls a service provider's executeUrl over HTTP (white paper
// section 9). It wraps the raw call with a timeout, bounded idempotent retries
// with backoff (safe because execute is idempotent by orderId), and a
// per-service circuit breaker so one failing provider cannot stall the platform.
type HTTPExecutor struct {
	client  *http.Client
	breaker *breakerSet
	// maxRetries is the number of additional attempts after the first.
	maxRetries int
	// baseBackoff is the first retry delay; it grows exponentially.
	baseBackoff time.Duration
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
}

// HTTPExecutorOption configures an HTTPExecutor.
type HTTPExecutorOption func(*HTTPExecutor)

func WithExecHTTPClient(c *http.Client) HTTPExecutorOption {
	return func(e *HTTPExecutor) { e.client = c }
}
func WithMaxRetries(n int) HTTPExecutorOption {
	return func(e *HTTPExecutor) { e.maxRetries = n }
}
func WithBaseBackoff(d time.Duration) HTTPExecutorOption {
	return func(e *HTTPExecutor) { e.baseBackoff = d }
}

// NewHTTPExecutor builds an HTTP executor. timeout bounds each individual call;
// breakerThreshold consecutive failures opens a service's breaker for
// breakerCooldown.
func NewHTTPExecutor(timeout time.Duration, breakerThreshold int, breakerCooldown time.Duration, opts ...HTTPExecutorOption) *HTTPExecutor {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	e := &HTTPExecutor{
		client:      &http.Client{Timeout: timeout},
		breaker:     newBreakerSet(breakerThreshold, breakerCooldown),
		maxRetries:  2,
		baseBackoff: 200 * time.Millisecond,
		now:         time.Now,
		sleep:       sleepCtx,
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

// executePayload is the JSON body posted to the provider (white paper section 9).
type executePayload struct {
	OrderID    string         `json:"orderId"`
	ServiceID  string         `json:"serviceId"`
	UserID     string         `json:"userId"`
	Amount     string         `json:"amount"`
	CurrencyID int64          `json:"currencyId"`
	QuoteNonce string         `json:"quoteNonce"`
	Metadata   map[string]any `json:"metadata,omitempty"`
}

// executeResponse is the provider's reply.
type executeResponse struct {
	Status      string `json:"status"`
	ExternalRef string `json:"externalRef"`
	Reason      string `json:"reason"`
}

func (e *HTTPExecutor) Execute(ctx context.Context, req ExecuteRequest) (ExecuteResult, error) {
	if req.ExecuteURL == "" {
		return ExecuteResult{}, fmt.Errorf("executeUrl is empty")
	}
	// Circuit breaker: refuse fast when the service is known-bad.
	if !e.breaker.allow(req.ServiceID, e.now()) {
		return ExecuteResult{}, fmt.Errorf("circuit open for service %s", req.ServiceID)
	}

	body, err := json.Marshal(executePayload{
		OrderID:    req.OrderID,
		ServiceID:  req.ServiceID,
		UserID:     req.UserID,
		Amount:     req.Amount,
		CurrencyID: req.CurrencyID,
		QuoteNonce: req.QuoteNonce,
		Metadata:   req.Metadata,
	})
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("marshal execute payload: %w", err)
	}

	var lastErr error
	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff before a retry.
			delay := time.Duration(float64(e.baseBackoff) * math.Pow(2, float64(attempt-1)))
			if err := e.sleep(ctx, delay); err != nil {
				return ExecuteResult{}, err
			}
		}

		res, retryable, err := e.callOnce(ctx, req.ExecuteURL, body)
		if err == nil {
			// A definite provider verdict (SUCCESS/FAILED/PENDING) is a success
			// for the breaker — the service responded.
			e.breaker.record(req.ServiceID, e.now(), true)
			return res, nil
		}
		lastErr = err
		if !retryable {
			// 4xx / malformed: a retry will not help. Count as a failure.
			e.breaker.record(req.ServiceID, e.now(), false)
			return ExecuteResult{}, err
		}
		// Retryable (network/5xx/timeout): loop will back off and try again.
	}

	// Exhausted retries: trip the breaker toward open.
	e.breaker.record(req.ServiceID, e.now(), false)
	return ExecuteResult{}, fmt.Errorf("execute failed after %d attempts: %w", e.maxRetries+1, lastErr)
}

// callOnce performs a single HTTP attempt. retryable reports whether a failure
// is worth retrying (network errors, timeouts, 5xx, 429).
func (e *HTTPExecutor) callOnce(ctx context.Context, url string, body []byte) (ExecuteResult, bool, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return ExecuteResult{}, false, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := e.client.Do(httpReq)
	if err != nil {
		return ExecuteResult{}, true, err // network/timeout: retryable
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests {
		io.Copy(io.Discard, resp.Body)
		return ExecuteResult{}, true, fmt.Errorf("provider returned %d", resp.StatusCode)
	}
	if resp.StatusCode != http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return ExecuteResult{}, false, fmt.Errorf("provider returned %d", resp.StatusCode)
	}

	var er executeResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return ExecuteResult{}, false, fmt.Errorf("decode execute response: %w", err)
	}
	status, ok := normalizeExecuteStatus(er.Status)
	if !ok {
		return ExecuteResult{}, false, fmt.Errorf("unknown execute status %q", er.Status)
	}
	return ExecuteResult{Status: status, ExternalRef: er.ExternalRef, Reason: er.Reason}, false, nil
}

func normalizeExecuteStatus(s string) (ExecuteStatus, bool) {
	switch s {
	case "SUCCESS":
		return ExecuteSuccess, true
	case "FAILED":
		return ExecuteFailed, true
	case "PENDING":
		return ExecutePending, true
	default:
		return "", false
	}
}

// sleepCtx sleeps for d unless ctx is cancelled first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

package saga

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// noSleep removes backoff delay from tests.
func noSleep(e *HTTPExecutor) { e.sleep = func(context.Context, time.Duration) error { return nil } }

func TestHTTPExecutorSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(`{"status":"SUCCESS","externalRef":"ext-1"}`))
	}))
	defer srv.Close()

	e := NewHTTPExecutor(time.Second, 5, time.Second)
	res, err := e.Execute(context.Background(), ExecuteRequest{ExecuteURL: srv.URL, ServiceID: "s1", OrderID: "o1"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != ExecuteSuccess || res.ExternalRef != "ext-1" {
		t.Fatalf("got %+v", res)
	}
}

func TestHTTPExecutorMapsPendingAndFailed(t *testing.T) {
	for _, tc := range []struct {
		body string
		want ExecuteStatus
	}{
		{`{"status":"PENDING","externalRef":"r"}`, ExecutePending},
		{`{"status":"FAILED","reason":"oos"}`, ExecuteFailed},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(tc.body))
		}))
		e := NewHTTPExecutor(time.Second, 5, time.Second)
		res, err := e.Execute(context.Background(), ExecuteRequest{ExecuteURL: srv.URL, ServiceID: "s", OrderID: "o"})
		srv.Close()
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if res.Status != tc.want {
			t.Fatalf("status = %s, want %s", res.Status, tc.want)
		}
	}
}

func TestHTTPExecutorRetriesOn5xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(&calls, 1) < 3 {
			w.WriteHeader(http.StatusBadGateway) // 502: retryable
			return
		}
		w.Write([]byte(`{"status":"SUCCESS","externalRef":"ok"}`))
	}))
	defer srv.Close()

	e := NewHTTPExecutor(time.Second, 5, time.Second, WithMaxRetries(3))
	noSleep(e)
	res, err := e.Execute(context.Background(), ExecuteRequest{ExecuteURL: srv.URL, ServiceID: "s", OrderID: "o"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.Status != ExecuteSuccess {
		t.Fatalf("status = %s, want SUCCESS", res.Status)
	}
	if got := atomic.LoadInt32(&calls); got != 3 {
		t.Fatalf("calls = %d, want 3 (2 failures + 1 success)", got)
	}
}

func TestHTTPExecutorNoRetryOn4xx(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest) // 400: not retryable
	}))
	defer srv.Close()

	e := NewHTTPExecutor(time.Second, 5, time.Second, WithMaxRetries(3))
	noSleep(e)
	if _, err := e.Execute(context.Background(), ExecuteRequest{ExecuteURL: srv.URL, ServiceID: "s", OrderID: "o"}); err == nil {
		t.Fatal("expected error on 400")
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 4xx)", got)
	}
}

func TestHTTPExecutorCircuitOpens(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusInternalServerError) // always 500
	}))
	defer srv.Close()

	// threshold=2, no retries so each Execute is one failure.
	e := NewHTTPExecutor(time.Second, 2, time.Minute, WithMaxRetries(0))
	noSleep(e)
	ctx := context.Background()
	req := ExecuteRequest{ExecuteURL: srv.URL, ServiceID: "flaky", OrderID: "o"}

	// Two failures trip the breaker.
	e.Execute(ctx, req)
	e.Execute(ctx, req)
	callsAfterTrip := atomic.LoadInt32(&calls)

	// Third call should be rejected by the open breaker without hitting the server.
	if _, err := e.Execute(ctx, req); err == nil {
		t.Fatal("expected circuit-open error")
	}
	if got := atomic.LoadInt32(&calls); got != callsAfterTrip {
		t.Fatalf("server hit while breaker open: calls went %d -> %d", callsAfterTrip, got)
	}
}

func TestHTTPExecutorCircuitHalfOpenRecovers(t *testing.T) {
	var fail atomic.Bool
	fail.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Write([]byte(`{"status":"SUCCESS","externalRef":"ok"}`))
	}))
	defer srv.Close()

	now := time.Unix(1000, 0)
	e := NewHTTPExecutor(time.Second, 1, 10*time.Second, WithMaxRetries(0))
	noSleep(e)
	e.now = func() time.Time { return now }

	req := ExecuteRequest{ExecuteURL: srv.URL, ServiceID: "svc", OrderID: "o"}
	// One failure opens the breaker (threshold=1).
	e.Execute(context.Background(), req)
	// Immediately: rejected.
	if _, err := e.Execute(context.Background(), req); err == nil {
		t.Fatal("expected open breaker to reject")
	}
	// Advance past cooldown and let the server recover.
	now = now.Add(11 * time.Second)
	fail.Store(false)
	res, err := e.Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("half-open trial should succeed: %v", err)
	}
	if res.Status != ExecuteSuccess {
		t.Fatalf("status = %s, want SUCCESS", res.Status)
	}
}

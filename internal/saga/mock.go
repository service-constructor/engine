package saga

import (
	"context"
	"sync"
)

// MockLedger is an in-memory Ledger for local runs and tests. It records
// operations per order and enforces idempotency (a repeated op is a no-op).
type MockLedger struct {
	mu       sync.Mutex
	frozen   map[string]FreezeRequest
	captured map[string]CaptureRequest
	released map[string]bool
}

// NewMockLedger builds an empty mock ledger.
func NewMockLedger() *MockLedger {
	return &MockLedger{
		frozen:   map[string]FreezeRequest{},
		captured: map[string]CaptureRequest{},
		released: map[string]bool{},
	}
}

func (m *MockLedger) Freeze(_ context.Context, req FreezeRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.frozen[req.OrderID] = req // idempotent overwrite
	return nil
}

func (m *MockLedger) Capture(_ context.Context, req CaptureRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.captured[req.OrderID] = req
	return nil
}

func (m *MockLedger) Release(_ context.Context, orderID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.released[orderID] = true
	return nil
}

// Frozen / Captured / Released expose state for assertions in tests.
func (m *MockLedger) Frozen(orderID string) (FreezeRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.frozen[orderID]
	return v, ok
}

func (m *MockLedger) Captured(orderID string) (CaptureRequest, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.captured[orderID]
	return v, ok
}

func (m *MockLedger) Released(orderID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.released[orderID]
}

// MockExecutor returns a fixed result, simulating a provider. Useful for local
// runs where no real executeUrl exists.
type MockExecutor struct {
	Result ExecuteResult
	Err    error
}

// NewMockExecutor returns an executor that always reports SUCCESS.
func NewMockExecutor() *MockExecutor {
	return &MockExecutor{Result: ExecuteResult{Status: ExecuteSuccess, ExternalRef: "mock-ref"}}
}

func (m *MockExecutor) Execute(_ context.Context, _ ExecuteRequest) (ExecuteResult, error) {
	return m.Result, m.Err
}

package saga

import (
	"context"
	"sync"
	"time"

	"github.com/service-constructor/engine/internal/domain"
)

// MemOrderStore is an in-memory OrderStore (and OutboxStore) for local runs and
// tests. The outbox lives in the same struct so SaveWithOutbox is atomic under
// the single mutex.
type MemOrderStore struct {
	mu          sync.Mutex
	byID        map[string]*domain.Order
	byNonce     map[string]string                    // serviceID|nonce -> orderID
	transitions map[string][]*domain.OrderTransition // orderID -> append-only trail
	outbox      []*domain.OutboxEntry
	nextID      int64
	nextTransID int64
}

// NewMemOrderStore builds an empty store.
func NewMemOrderStore() *MemOrderStore {
	return &MemOrderStore{
		byID:        map[string]*domain.Order{},
		byNonce:     map[string]string{},
		transitions: map[string][]*domain.OrderTransition{},
	}
}

func nonceKey(serviceID, nonce string) string { return serviceID + "|" + nonce }

// recordTransition appends an audit row, assigning Seq as the next per-order
// counter. Caller must hold s.mu.
func (s *MemOrderStore) recordTransition(rec *domain.OrderTransition) {
	s.nextTransID++
	cp := *rec
	cp.ID = s.nextTransID
	cp.Seq = len(s.transitions[rec.OrderID]) + 1
	s.transitions[rec.OrderID] = append(s.transitions[rec.OrderID], &cp)
}

func (s *MemOrderStore) Create(_ context.Context, o *domain.Order, rec *domain.OrderTransition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[o.ID]; ok {
		return domain.ErrAlreadyExists
	}
	if _, ok := s.byNonce[nonceKey(o.ServiceID, o.QuoteNonce)]; ok {
		return domain.ErrIdempotencyConflict
	}
	cp := *o
	s.byID[o.ID] = &cp
	s.byNonce[nonceKey(o.ServiceID, o.QuoteNonce)] = o.ID
	s.recordTransition(rec)
	return nil
}

func (s *MemOrderStore) Get(_ context.Context, id string) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.byID[id]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	cp := *o
	return &cp, nil
}

func (s *MemOrderStore) FindByNonce(_ context.Context, serviceID, nonce string) (*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.byNonce[nonceKey(serviceID, nonce)]
	if !ok {
		return nil, domain.ErrOrderNotFound
	}
	cp := *s.byID[id]
	return &cp, nil
}

func (s *MemOrderStore) Save(_ context.Context, o *domain.Order, rec *domain.OrderTransition) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[o.ID]; !ok {
		return domain.ErrOrderNotFound
	}
	cp := *o
	s.byID[o.ID] = &cp
	s.recordTransition(rec)
	return nil
}

func (s *MemOrderStore) SaveWithOutbox(_ context.Context, o *domain.Order, rec *domain.OrderTransition, entry *domain.OutboxEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[o.ID]; !ok {
		return domain.ErrOrderNotFound
	}
	cp := *o
	s.byID[o.ID] = &cp
	s.recordTransition(rec)
	s.nextID++
	e := *entry
	e.ID = s.nextID
	s.outbox = append(s.outbox, &e)
	return nil
}

func (s *MemOrderStore) ListTransitions(_ context.Context, orderID string) ([]*domain.OrderTransition, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.OrderTransition
	for _, t := range s.transitions[orderID] {
		cp := *t
		out = append(out, &cp)
	}
	return out, nil
}

func (s *MemOrderStore) ListUndispatched(_ context.Context, limit int) ([]*domain.OutboxEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.OutboxEntry
	for _, e := range s.outbox {
		if e.DispatchedAt != nil {
			continue
		}
		cp := *e
		out = append(out, &cp)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemOrderStore) MarkDispatched(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.outbox {
		if e.ID == id {
			now := time.Now().UTC()
			e.DispatchedAt = &now
			return nil
		}
	}
	return nil
}

func (s *MemOrderStore) ListStuck(_ context.Context, olderThan time.Time, limit int) ([]*domain.Order, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*domain.Order
	for _, o := range s.byID {
		if o.State != domain.OrderPending && o.State != domain.OrderExecuted {
			continue
		}
		if !o.FreezeExpiresAt.IsZero() && o.FreezeExpiresAt.After(olderThan) {
			continue
		}
		cp := *o
		out = append(out, &cp)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

package saga

import (
	"context"
	"sync"
	"time"

	"github.com/nvsces/service-constructor/internal/domain"
)

// MemOrderStore is an in-memory OrderStore for local runs and tests.
type MemOrderStore struct {
	mu      sync.Mutex
	byID    map[string]*domain.Order
	byNonce map[string]string // serviceID|nonce -> orderID
}

// NewMemOrderStore builds an empty store.
func NewMemOrderStore() *MemOrderStore {
	return &MemOrderStore{
		byID:    map[string]*domain.Order{},
		byNonce: map[string]string{},
	}
}

func nonceKey(serviceID, nonce string) string { return serviceID + "|" + nonce }

func (s *MemOrderStore) Create(_ context.Context, o *domain.Order) error {
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

func (s *MemOrderStore) Save(_ context.Context, o *domain.Order) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.byID[o.ID]; !ok {
		return domain.ErrOrderNotFound
	}
	cp := *o
	s.byID[o.ID] = &cp
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

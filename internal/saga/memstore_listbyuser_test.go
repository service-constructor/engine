package saga

import (
	"context"
	"testing"
	"time"

	"github.com/service-constructor/engine/internal/domain"
)

// TestMemOrderStoreListByUser locks in the two guarantees ListOrders relies on:
// results are scoped to the requesting user, and returned newest-first.
func TestMemOrderStoreListByUser(t *testing.T) {
	s := NewMemOrderStore()
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	mk := func(id, user string, createdOffset time.Duration) {
		o := &domain.Order{
			ID:         id,
			ServiceID:  "svc-" + id,
			UserID:     user,
			QuoteNonce: "n-" + id,
			State:      domain.OrderCompleted,
			CreatedAt:  base.Add(createdOffset),
		}
		rec := &domain.OrderTransition{OrderID: id, ToState: domain.OrderCompleted}
		if err := s.Create(ctx, o, rec); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	// alice has three orders across mini-apps; bob has one (must not leak).
	mk("a1", "alice", 1*time.Hour)
	mk("a2", "alice", 3*time.Hour) // newest
	mk("a3", "alice", 2*time.Hour)
	mk("b1", "bob", 5*time.Hour)

	got, err := s.ListByUser(ctx, "alice")
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (bob's order must not leak): %+v", len(got), got)
	}
	// Newest first: a2 (t+3h), a3 (t+2h), a1 (t+1h).
	wantOrder := []string{"a2", "a3", "a1"}
	for i, id := range wantOrder {
		if got[i].ID != id {
			t.Errorf("order[%d] = %s, want %s (newest-first)", i, got[i].ID, id)
		}
	}

	// Unknown user yields an empty (non-error) result.
	none, err := s.ListByUser(ctx, "carol")
	if err != nil {
		t.Fatalf("ListByUser(carol): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("carol orders = %d, want 0", len(none))
	}
}

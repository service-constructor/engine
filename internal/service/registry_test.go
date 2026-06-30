package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/nvsces/service-constructor/internal/domain"
	"github.com/nvsces/service-constructor/internal/service"
)

// memRepo is an in-memory Repository for exercising the use-case layer without
// a database.
type memRepo struct {
	items map[string]*domain.Service
}

func newMemRepo() *memRepo { return &memRepo{items: map[string]*domain.Service{}} }

func (m *memRepo) Create(_ context.Context, s *domain.Service) error {
	if _, ok := m.items[s.ID]; ok {
		return domain.ErrAlreadyExists
	}
	cp := *s
	m.items[s.ID] = &cp
	return nil
}

func (m *memRepo) Get(_ context.Context, id string) (*domain.Service, error) {
	s, ok := m.items[id]
	if !ok {
		return nil, domain.ErrNotFound
	}
	cp := *s
	return &cp, nil
}

func (m *memRepo) List(_ context.Context, f service.ListFilter) ([]*domain.Service, string, error) {
	var out []*domain.Service
	for _, s := range m.items {
		if f.Status != "" && s.Status != f.Status {
			continue
		}
		cp := *s
		out = append(out, &cp)
	}
	return out, "", nil
}

func (m *memRepo) Update(_ context.Context, s *domain.Service) error {
	if _, ok := m.items[s.ID]; !ok {
		return domain.ErrNotFound
	}
	cp := *s
	m.items[s.ID] = &cp
	return nil
}

func (m *memRepo) Delete(_ context.Context, id string) error {
	if _, ok := m.items[id]; !ok {
		return domain.ErrNotFound
	}
	delete(m.items, id)
	return nil
}

func newTestRegistry() *service.Registry {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	n := 0
	return service.NewRegistry(newMemRepo(),
		service.WithClock(func() time.Time { return fixed }),
		service.WithIDGenerator(func() string { n++; return "svc_test_" + itoa(n) }),
	)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestCreateAssignsIDAndDefaultsStatus(t *testing.T) {
	reg := newTestRegistry()
	got, err := reg.Create(context.Background(), &domain.Service{Name: "eSIM"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.ID == "" {
		t.Error("expected generated id")
	}
	if got.Status != domain.StatusDraft {
		t.Errorf("status = %q, want draft", got.Status)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Error("expected timestamps to be set")
	}
}

func TestCreateRejectsMissingName(t *testing.T) {
	reg := newTestRegistry()
	_, err := reg.Create(context.Background(), &domain.Service{})
	if !errors.Is(err, domain.ErrInvalidArgument) {
		t.Fatalf("err = %v, want ErrInvalidArgument", err)
	}
}

func TestGetUpdateDeleteRoundTrip(t *testing.T) {
	reg := newTestRegistry()
	ctx := context.Background()

	created, err := reg.Create(ctx, &domain.Service{Name: "Topup", Status: domain.StatusActive})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	created.Name = "Topup v2"
	updated, err := reg.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Topup v2" {
		t.Errorf("name = %q, want Topup v2", updated.Name)
	}

	got, err := reg.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "Topup v2" {
		t.Errorf("persisted name = %q", got.Name)
	}

	if err := reg.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := reg.Get(ctx, created.ID); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("Get after delete: err = %v, want ErrNotFound", err)
	}
}

func TestListFilterByStatus(t *testing.T) {
	reg := newTestRegistry()
	ctx := context.Background()
	_, _ = reg.Create(ctx, &domain.Service{Name: "A", Status: domain.StatusActive})
	_, _ = reg.Create(ctx, &domain.Service{Name: "D", Status: domain.StatusDraft})

	active, _, err := reg.List(ctx, service.ListFilter{Status: domain.StatusActive})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(active) != 1 || active[0].Name != "A" {
		t.Fatalf("active = %+v, want [A]", active)
	}
}

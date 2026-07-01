// Package service implements the use-case layer for the service registry: it
// validates input, assigns ids and timestamps, and delegates persistence to a
// Repository. It is transport- and storage-agnostic.
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/service-constructor/engine/internal/domain"
	"github.com/service-constructor/engine/internal/keygen"
)

// Repository is the persistence port for services. Read/write methods take a
// Scope so the storage layer can enforce ownership (defense in depth): a
// non-super-admin can only touch rows whose owner_id matches.
type Repository interface {
	Create(ctx context.Context, s *domain.Service) error
	Get(ctx context.Context, scope Scope, id string) (*domain.Service, error)
	List(ctx context.Context, scope Scope, f ListFilter) ([]*domain.Service, string, error)
	Update(ctx context.Context, scope Scope, s *domain.Service) error
	Delete(ctx context.Context, scope Scope, id string) error
}

// Scope identifies the acting account and its visibility. When AllOwners is
// true (super-admin), ownership filtering is bypassed; otherwise access is
// limited to rows owned by OwnerID.
type Scope struct {
	OwnerID   string
	AllOwners bool
}

// ScopeAll is an unrestricted scope (e.g. for internal callers/tests).
var ScopeAll = Scope{AllOwners: true}

// ListFilter parameterizes a List query. An empty Status matches all.
type ListFilter struct {
	Status    domain.Status
	PageSize  int
	PageToken string
}

// Clock abstracts time for deterministic tests. The zero value is unusable;
// use NewRegistry which defaults to the wall clock.
type Clock func() time.Time

// IDGenerator produces new service ids.
type IDGenerator func() string

// Registry is the service-registry use case.
type Registry struct {
	repo  Repository
	now   Clock
	newID IDGenerator
}

// Option configures a Registry.
type Option func(*Registry)

// WithClock overrides the time source (tests).
func WithClock(c Clock) Option { return func(r *Registry) { r.now = c } }

// WithIDGenerator overrides id generation (tests).
func WithIDGenerator(g IDGenerator) Option { return func(r *Registry) { r.newID = g } }

// NewRegistry constructs a Registry backed by repo.
func NewRegistry(repo Repository, opts ...Option) *Registry {
	r := &Registry{
		repo:  repo,
		now:   time.Now,
		newID: defaultID,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

func defaultID() string {
	return "svc_" + strings.ReplaceAll(uuid.NewString(), "-", "")
}

// Create validates and persists a new service, assigning id, timestamps and
// owner. Any id, created/updated timestamps on the input are ignored; OwnerID
// is taken from the scope (a super-admin creating without a concrete owner ends
// up owning the record itself).
func (r *Registry) Create(ctx context.Context, scope Scope, in *domain.Service) (*domain.Service, error) {
	if in.Status == "" {
		in.Status = domain.StatusDraft
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	now := r.now().UTC()
	in.ID = r.newID()
	in.OwnerID = scope.OwnerID
	in.CreatedAt = now
	in.UpdatedAt = now
	if err := r.repo.Create(ctx, in); err != nil {
		return nil, fmt.Errorf("create service: %w", err)
	}
	return in, nil
}

// Get returns a service by id, scoped to the caller's ownership.
func (r *Registry) Get(ctx context.Context, scope Scope, id string) (*domain.Service, error) {
	if id == "" {
		return nil, domain.ErrInvalidArgument
	}
	return r.repo.Get(ctx, scope, id)
}

// List returns a page of services visible to the caller.
func (r *Registry) List(ctx context.Context, scope Scope, f ListFilter) ([]*domain.Service, string, error) {
	if f.Status != "" && !f.Status.Valid() {
		return nil, "", domain.ErrInvalidArgument
	}
	const (
		defaultPage = 50
		maxPage     = 200
	)
	if f.PageSize <= 0 {
		f.PageSize = defaultPage
	}
	if f.PageSize > maxPage {
		f.PageSize = maxPage
	}
	return r.repo.List(ctx, scope, f)
}

// Update applies the supplied fields to an existing service. The caller is
// responsible for having merged the update mask into in; here we re-validate
// and bump UpdatedAt. id must be set. The update is scoped: a non-owner that is
// not a super-admin gets ErrNotFound.
func (r *Registry) Update(ctx context.Context, scope Scope, in *domain.Service) (*domain.Service, error) {
	if in.ID == "" {
		return nil, domain.ErrInvalidArgument
	}
	if err := in.Validate(); err != nil {
		return nil, err
	}
	in.UpdatedAt = r.now().UTC()
	if err := r.repo.Update(ctx, scope, in); err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}
	return in, nil
}

// Delete removes a service by id, scoped to the caller's ownership.
func (r *Registry) Delete(ctx context.Context, scope Scope, id string) error {
	if id == "" {
		return domain.ErrInvalidArgument
	}
	return r.repo.Delete(ctx, scope, id)
}

// GenerateKey creates a new key pair for the service, appends the public key to
// the registry record, and returns the private key PEM. The private key is not
// persisted. If retireKID is non-empty, that key is removed from the record
// (callers wanting an overlap window simply omit retireKID).
func (r *Registry) GenerateKey(ctx context.Context, scope Scope, serviceID string, alg keygen.Algorithm, retireKID string) (*domain.Service, keygen.KeyPair, error) {
	if serviceID == "" {
		return nil, keygen.KeyPair{}, domain.ErrInvalidArgument
	}
	svc, err := r.repo.Get(ctx, scope, serviceID)
	if err != nil {
		return nil, keygen.KeyPair{}, err
	}

	pair, err := keygen.Generate(alg, serviceID)
	if err != nil {
		return nil, keygen.KeyPair{}, fmt.Errorf("%w: %v", domain.ErrInvalidArgument, err)
	}

	if retireKID != "" {
		kept := svc.PublicKeys[:0]
		for _, k := range svc.PublicKeys {
			if k.KID != retireKID {
				kept = append(kept, k)
			}
		}
		svc.PublicKeys = kept
	}
	svc.PublicKeys = append(svc.PublicKeys, domain.PublicKey{KID: pair.KID, PEM: pair.PublicKeyPEM})
	svc.UpdatedAt = r.now().UTC()

	if err := r.repo.Update(ctx, scope, svc); err != nil {
		return nil, keygen.KeyPair{}, fmt.Errorf("persist key: %w", err)
	}
	return svc, pair, nil
}

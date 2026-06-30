// Package auth defines a pluggable authentication boundary for the admin API.
//
// The platform ships a default JWT authenticator, but an integrator adopting
// this open-source module can drop in their existing auth by implementing the
// Authenticator interface and passing it when constructing the server. Nothing
// in the registry or transport layers depends on a concrete auth scheme.
package auth

import (
	"context"
	"errors"
)

// Principal is the authenticated caller. Implementations populate whatever
// identity their scheme carries; Subject and Roles are the only fields the
// platform inspects (for authorization decisions and audit).
type Principal struct {
	// Subject uniquely identifies the caller (e.g. user id, service account).
	Subject string
	// Roles the caller holds; used by RequireRole. May be empty.
	Roles []string
	// Extra carries scheme-specific claims an integrator may want downstream.
	Extra map[string]any
}

// HasRole reports whether the principal holds the named role.
func (p *Principal) HasRole(role string) bool {
	for _, r := range p.Roles {
		if r == role {
			return true
		}
	}
	return false
}

// Authenticator validates the credentials carried by an incoming request and
// returns the authenticated Principal.
//
// The token argument is the raw bearer credential extracted from the request
// (the "Authorization: Bearer <token>" value, or an equivalent). Returning
// ErrUnauthenticated (or any error) denies the request.
//
// Implementations must be safe for concurrent use.
type Authenticator interface {
	Authenticate(ctx context.Context, token string) (*Principal, error)
}

// Sentinel errors. Transport adapters map these to status codes.
var (
	// ErrUnauthenticated: missing or invalid credentials.
	ErrUnauthenticated = errors.New("unauthenticated")
	// ErrPermissionDenied: authenticated but lacks the required role.
	ErrPermissionDenied = errors.New("permission denied")
)

// principalKey is the context key under which the Principal is stored.
type principalKey struct{}

// WithPrincipal returns a copy of ctx carrying p.
func WithPrincipal(ctx context.Context, p *Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// PrincipalFromContext extracts the Principal placed by the auth middleware.
func PrincipalFromContext(ctx context.Context) (*Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(*Principal)
	return p, ok
}

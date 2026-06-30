package auth

import "context"

// AllowAll is a no-op Authenticator that accepts every request as a fixed
// principal. It exists for local development only; never enable it in
// production. A loud warning is logged at startup when it is selected.
type AllowAll struct {
	// Principal returned for every call. If nil, a default admin principal is
	// used.
	Principal *Principal
}

// Authenticate always succeeds.
func (a AllowAll) Authenticate(_ context.Context, _ string) (*Principal, error) {
	if a.Principal != nil {
		return a.Principal, nil
	}
	return &Principal{Subject: "dev", Roles: []string{RoleAdmin}}, nil
}

// RoleAdmin is the role required to access the admin API. Integrators can map
// their own role names onto it or pass a different required role.
const RoleAdmin = "admin"

// RequireRole returns ErrPermissionDenied unless the principal in ctx holds the
// role. A missing principal yields ErrUnauthenticated.
func RequireRole(ctx context.Context, role string) error {
	p, ok := PrincipalFromContext(ctx)
	if !ok || p == nil {
		return ErrUnauthenticated
	}
	if !p.HasRole(role) {
		return ErrPermissionDenied
	}
	return nil
}

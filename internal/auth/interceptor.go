package auth

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Requirement is the auth requirement for a method, returned by a RoleResolver.
type Requirement int

const (
	// Public: no authentication required.
	Public Requirement = iota
	// AuthOnly: a valid principal is required, but no specific role.
	AuthOnly
	// RequireAdmin: a valid principal holding the admin role.
	RequireAdmin
)

// RoleResolver maps a gRPC full method name (e.g.
// "/serviceconstructor.v1.ServiceRegistry/CreateService") to its auth
// requirement.
type RoleResolver func(fullMethod string) Requirement

// UnaryServerInterceptor authenticates and authorizes incoming unary calls
// according to resolve. Public methods pass through; AuthOnly requires a valid
// principal; RequireAdmin additionally requires the admin role.
//
// The bearer token is taken from the "authorization" metadata entry; the HTTP
// gateway forwards the inbound Authorization header into this metadata, so a
// single interceptor covers both gRPC and REST callers.
func UnaryServerInterceptor(a Authenticator, resolve RoleResolver) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		reqd := resolve(info.FullMethod)
		if reqd == Public {
			return handler(ctx, req)
		}

		token := bearerFromMetadata(ctx)
		principal, err := a.Authenticate(ctx, token)
		if err != nil {
			return nil, toGRPC(err)
		}
		ctx = WithPrincipal(ctx, principal)

		if reqd == RequireAdmin {
			if err := RequireRole(ctx, RoleAdmin); err != nil {
				return nil, toGRPC(err)
			}
		}
		return handler(ctx, req)
	}
}

// DefaultRoleResolver enforces the platform policy: registry/admin methods need
// the admin role; payment methods need an authenticated user; anything else is
// public.
func DefaultRoleResolver(fullMethod string) Requirement {
	switch {
	case strings.Contains(fullMethod, "ServiceRegistry"):
		return RequireAdmin
	case strings.Contains(fullMethod, "PaymentService"):
		return AuthOnly
	default:
		return Public
	}
}

func bearerFromMetadata(ctx context.Context) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return ""
	}
	const prefix = "Bearer "
	v := vals[0]
	if len(v) >= len(prefix) && strings.EqualFold(v[:len(prefix)], prefix) {
		return v[len(prefix):]
	}
	return v
}

func toGRPC(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, ErrPermissionDenied):
		return status.Error(codes.PermissionDenied, err.Error())
	default:
		return status.Error(codes.Unauthenticated, err.Error())
	}
}

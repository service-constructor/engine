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

// Identity metadata keys. The gateway verifies the JWT centrally and forwards
// the caller's identity as these gRPC metadata pairs; engine trusts them (it is
// only reachable in-cluster behind the gateway).
const (
	mdUserID    = "x-user-id"
	mdUserRoles = "x-user-roles"
)

// UnaryServerInterceptor authorizes incoming unary calls according to resolve.
// Public methods pass through; AuthOnly requires an identity; RequireAdmin
// additionally requires the admin role.
//
// Identity comes from the gateway as x-user-id / x-user-roles metadata (the
// gateway already verified the JWT). A missing x-user-id on a non-public method
// is Unauthenticated.
func UnaryServerInterceptor(resolve RoleResolver) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		reqd := resolve(info.FullMethod)
		if reqd == Public {
			return handler(ctx, req)
		}

		principal := principalFromMetadata(ctx)
		if principal == nil {
			return nil, status.Error(codes.Unauthenticated, "missing identity")
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

// principalFromMetadata builds the caller from the gateway's identity metadata,
// or returns nil if x-user-id is absent.
func principalFromMetadata(ctx context.Context) *Principal {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil
	}
	uid := first(md.Get(mdUserID))
	if uid == "" {
		return nil
	}
	var roles []string
	if rs := first(md.Get(mdUserRoles)); rs != "" {
		roles = strings.Split(rs, ",")
	}
	return &Principal{Subject: uid, Roles: roles}
}

func first(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// DefaultRoleResolver enforces the platform policy: registry/admin methods need
// the admin role; the payment Pay/GetOrder methods need an authenticated user;
// the provider Callback is public (authenticated by its signature, not a user
// session); anything else is public.
func DefaultRoleResolver(fullMethod string) Requirement {
	switch {
	case strings.HasSuffix(fullMethod, "/Callback"):
		// Provider webhook: signature is the authentication.
		return Public
	case strings.Contains(fullMethod, "ServiceRegistry"):
		return RequireAdmin
	case strings.Contains(fullMethod, "PaymentService"):
		return AuthOnly
	default:
		return Public
	}
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

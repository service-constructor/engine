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

// MethodPredicate reports whether a gRPC full method name (e.g.
// "/serviceconstructor.v1.ServiceRegistry/CreateService") requires
// authentication. Returning false lets the call through unauthenticated.
type MethodPredicate func(fullMethod string) bool

// UnaryServerInterceptor authenticates incoming unary calls for which protected
// reports true, attaching the Principal to the context. It also enforces that
// the caller holds requiredRole.
//
// The bearer token is taken from the "authorization" metadata entry; the HTTP
// gateway forwards the inbound Authorization header into this metadata, so a
// single interceptor covers both gRPC and REST callers.
func UnaryServerInterceptor(a Authenticator, protected MethodPredicate, requiredRole string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		if !protected(info.FullMethod) {
			return handler(ctx, req)
		}

		token := bearerFromMetadata(ctx)
		principal, err := a.Authenticate(ctx, token)
		if err != nil {
			return nil, toGRPC(err)
		}
		ctx = WithPrincipal(ctx, principal)

		if requiredRole != "" {
			if err := RequireRole(ctx, requiredRole); err != nil {
				return nil, toGRPC(err)
			}
		}
		return handler(ctx, req)
	}
}

// AdminMethods is a MethodPredicate matching the admin surface of the registry:
// any method whose path contains "/admin" or mutates the registry. Here we
// simply protect everything on the ServiceRegistry service, since the current
// API is admin-only.
func AdminMethods(fullMethod string) bool {
	return strings.Contains(fullMethod, "ServiceRegistry")
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

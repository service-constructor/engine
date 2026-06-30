package auth

import (
	"context"
	"fmt"

	"github.com/golang-jwt/jwt/v5"
)

// JWTAuthenticator is the default Authenticator: it verifies an HMAC-signed JWT
// and maps standard claims onto a Principal. It is intended as a sensible
// out-of-the-box scheme; integrators with their own identity provider should
// implement Authenticator instead.
type JWTAuthenticator struct {
	secret []byte
	// rolesClaim is the claim name holding a []string of roles. Defaults to
	// "roles".
	rolesClaim string
	// issuer, audience optionally constrain accepted tokens (empty = skip).
	issuer   string
	audience string
}

// JWTOption configures a JWTAuthenticator.
type JWTOption func(*JWTAuthenticator)

// WithRolesClaim overrides the claim name that carries roles.
func WithRolesClaim(name string) JWTOption {
	return func(a *JWTAuthenticator) { a.rolesClaim = name }
}

// WithIssuer requires tokens to carry the given iss.
func WithIssuer(iss string) JWTOption { return func(a *JWTAuthenticator) { a.issuer = iss } }

// WithAudience requires tokens to carry the given aud.
func WithAudience(aud string) JWTOption { return func(a *JWTAuthenticator) { a.audience = aud } }

// NewJWTAuthenticator builds a JWT authenticator over an HMAC secret.
func NewJWTAuthenticator(secret []byte, opts ...JWTOption) *JWTAuthenticator {
	a := &JWTAuthenticator{secret: secret, rolesClaim: "roles"}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Authenticate verifies the token and extracts the Principal.
func (a *JWTAuthenticator) Authenticate(_ context.Context, token string) (*Principal, error) {
	if token == "" {
		return nil, ErrUnauthenticated
	}

	parserOpts := []jwt.ParserOption{jwt.WithValidMethods([]string{"HS256", "HS384", "HS512"})}
	if a.issuer != "" {
		parserOpts = append(parserOpts, jwt.WithIssuer(a.issuer))
	}
	if a.audience != "" {
		parserOpts = append(parserOpts, jwt.WithAudience(a.audience))
	}

	claims := jwt.MapClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(t *jwt.Token) (any, error) {
		return a.secret, nil
	}, parserOpts...)
	if err != nil || !parsed.Valid {
		return nil, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}

	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, fmt.Errorf("%w: missing sub", ErrUnauthenticated)
	}

	return &Principal{
		Subject: sub,
		Roles:   extractRoles(claims[a.rolesClaim]),
		Extra:   claims,
	}, nil
}

// extractRoles coerces a roles claim (JSON array or single string) to []string.
func extractRoles(v any) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	default:
		return nil
	}
}

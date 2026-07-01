// Package domain holds the core entities of the Service Constructor platform,
// independent of transport (gRPC) and storage (Postgres) concerns.
package domain

import (
	"errors"
	"time"
)

// Status is the lifecycle state of a registered service.
type Status string

const (
	StatusDraft     Status = "draft"
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case StatusDraft, StatusActive, StatusSuspended:
		return true
	default:
		return false
	}
}

// PublicKey is one of a service's asymmetric keys used to verify quote and
// webhook signatures. The private counterpart never leaves the service.
type PublicKey struct {
	// KID identifies the key for rotation (overlap window of two valid keys).
	KID string `json:"kid"`
	// PEM-encoded public key material.
	PEM string `json:"pem"`
}

// ReceivingWallet is a payout wallet for a given currency. The set of
// currencies present here defines which currencies the service accepts.
type ReceivingWallet struct {
	CurrencyID int64  `json:"currency_id"`
	WalletID   string `json:"wallet_id"`
}

// Fee is the platform fee charged on top of a service payment. Amounts are
// kept as decimal strings to avoid float rounding in money math.
type Fee struct {
	Percent string `json:"percent,omitempty"`
	Fixed   string `json:"fixed,omitempty"`
}

// Limits constrains the amount and frequency of operations for a service.
type Limits struct {
	MaxAmount string `json:"max_amount,omitempty"`
	PerHour   int32  `json:"per_hour,omitempty"`
}

// Service is a registry record describing how the platform trusts a service
// and where it routes funds.
type Service struct {
	ID string
	// OwnerID is the subject (account) that owns this service. Set from the
	// authenticated principal at creation; used to scope reads and writes so
	// each account only sees its own services (super-admins see all).
	OwnerID    string
	Name       string
	PublicKeys []PublicKey
	// EncryptionPublicKey is the service's X25519 public key (base64 raw, 32
	// bytes) used to sealed-box encrypt the user id handed to the mini-app. It is
	// distinct from PublicKeys (Ed25519 signature keys).
	EncryptionPublicKey string
	Origins             []string
	ExecuteURL          string
	StatusURL           string
	ReceivingWallets    []ReceivingWallet
	Fee                 Fee
	Limits              Limits
	Status              Status
	// Storefront/catalog display fields (public, non-sensitive): shown by the
	// shell's app list.
	Description string
	IconURL     string
	MiniappURL  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Validation errors surfaced by the service layer. They are sentinel values so
// callers (e.g. the gRPC handler) can map them to status codes.
var (
	ErrNotFound        = errors.New("service not found")
	ErrAlreadyExists   = errors.New("service already exists")
	ErrInvalidArgument = errors.New("invalid argument")
)

// Validate checks the invariants required for a service to be persisted.
func (s *Service) Validate() error {
	if s.Name == "" {
		return wrap("name is required")
	}
	if !s.Status.Valid() {
		return wrap("status is invalid")
	}
	for _, w := range s.ReceivingWallets {
		if w.WalletID == "" {
			return wrap("receiving wallet id is required")
		}
	}
	for _, k := range s.PublicKeys {
		if k.KID == "" || k.PEM == "" {
			return wrap("public key kid and pem are required")
		}
	}
	return nil
}

func wrap(msg string) error {
	return errors.Join(ErrInvalidArgument, errors.New(msg))
}

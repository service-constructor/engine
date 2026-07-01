// Package postgres implements the service Repository over PostgreSQL using pgx.
package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/service-constructor/engine/internal/domain"
	"github.com/service-constructor/engine/internal/service"
)

// ServiceRepository persists domain.Service records.
type ServiceRepository struct {
	pool *pgxpool.Pool
}

// NewServiceRepository wraps a pgx pool.
func NewServiceRepository(pool *pgxpool.Pool) *ServiceRepository {
	return &ServiceRepository{pool: pool}
}

// row mirrors the table; nested fields are stored as JSONB.
const columns = `service_id, owner_id, name, public_keys, origins, execute_url, status_url,
	receiving_wallets, fee, limits, status, created_at, updated_at, encryption_public_key,
	description, icon_url, miniapp_url`

func (r *ServiceRepository) Create(ctx context.Context, s *domain.Service) error {
	pk, rw, fee, lim := encodeJSON(s)

	_, err := r.pool.Exec(ctx, `
		INSERT INTO services (`+columns+`)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		s.ID, s.OwnerID, s.Name, pk, nonNil(s.Origins), s.ExecuteURL, s.StatusURL,
		rw, fee, lim, string(s.Status), s.CreatedAt, s.UpdatedAt, s.EncryptionPublicKey,
		s.Description, s.IconURL, s.MiniappURL,
	)
	if isUniqueViolation(err) {
		return domain.ErrAlreadyExists
	}
	if err != nil {
		return fmt.Errorf("insert service: %w", err)
	}
	return nil
}

func (r *ServiceRepository) Get(ctx context.Context, scope service.Scope, id string) (*domain.Service, error) {
	q := `SELECT ` + columns + ` FROM services WHERE service_id = $1`
	args := []any{id}
	if !scope.AllOwners {
		args = append(args, scope.OwnerID)
		q += " AND owner_id = $2"
	}
	row := r.pool.QueryRow(ctx, q, args...)
	s, err := scanService(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get service: %w", err)
	}
	return s, nil
}

func (r *ServiceRepository) List(ctx context.Context, scope service.Scope, f service.ListFilter) ([]*domain.Service, string, error) {
	// Keyset pagination ordered by (created_at, service_id) for stability.
	var (
		args   []any
		where  []string
		cursor cursor
	)
	if !scope.AllOwners {
		args = append(args, scope.OwnerID)
		where = append(where, fmt.Sprintf("owner_id = $%d", len(args)))
	}
	if f.Status != "" {
		args = append(args, string(f.Status))
		where = append(where, fmt.Sprintf("status = $%d", len(args)))
	}
	if f.PageToken != "" {
		c, err := decodeCursor(f.PageToken)
		if err != nil {
			return nil, "", fmt.Errorf("%w: invalid page token", domain.ErrInvalidArgument)
		}
		cursor = c
		args = append(args, c.CreatedAt, c.ID)
		where = append(where, fmt.Sprintf("(created_at, service_id) > ($%d, $%d)", len(args)-1, len(args)))
	}
	q := `SELECT ` + columns + ` FROM services`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	// Fetch one extra row to detect whether a next page exists.
	args = append(args, f.PageSize+1)
	q += fmt.Sprintf(" ORDER BY created_at, service_id LIMIT $%d", len(args))

	rows, err := r.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list services: %w", err)
	}
	defer rows.Close()

	var out []*domain.Service
	for rows.Next() {
		s, err := scanService(rows)
		if err != nil {
			return nil, "", fmt.Errorf("scan service: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate services: %w", err)
	}

	var next string
	if len(out) > f.PageSize {
		last := out[f.PageSize-1]
		out = out[:f.PageSize]
		next = encodeCursor(cursor.with(last.CreatedAt, last.ID))
	}
	return out, next, nil
}

func (r *ServiceRepository) Update(ctx context.Context, scope service.Scope, s *domain.Service) error {
	pk, rw, fee, lim := encodeJSON(s)

	q := `
		UPDATE services SET
			name = $2, public_keys = $3, origins = $4, execute_url = $5,
			status_url = $6, receiving_wallets = $7, fee = $8, limits = $9,
			status = $10, updated_at = $11, encryption_public_key = $12,
			description = $13, icon_url = $14, miniapp_url = $15
		WHERE service_id = $1`
	args := []any{
		s.ID, s.Name, pk, nonNil(s.Origins), s.ExecuteURL, s.StatusURL,
		rw, fee, lim, string(s.Status), s.UpdatedAt, s.EncryptionPublicKey,
		s.Description, s.IconURL, s.MiniappURL,
	}
	if !scope.AllOwners {
		args = append(args, scope.OwnerID)
		q += fmt.Sprintf(" AND owner_id = $%d", len(args))
	}

	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *ServiceRepository) Delete(ctx context.Context, scope service.Scope, id string) error {
	q := `DELETE FROM services WHERE service_id = $1`
	args := []any{id}
	if !scope.AllOwners {
		args = append(args, scope.OwnerID)
		q += " AND owner_id = $2"
	}
	tag, err := r.pool.Exec(ctx, q, args...)
	if err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// encodeJSON marshals the nested fields to JSONB payloads. Nil slices are
// encoded as empty JSON arrays so the NOT NULL columns never receive `null`.
func encodeJSON(s *domain.Service) (publicKeys, receivingWallets, fee, limits []byte) {
	pk := s.PublicKeys
	if pk == nil {
		pk = []domain.PublicKey{}
	}
	rw := s.ReceivingWallets
	if rw == nil {
		rw = []domain.ReceivingWallet{}
	}
	publicKeys, _ = json.Marshal(pk)
	receivingWallets, _ = json.Marshal(rw)
	fee, _ = json.Marshal(s.Fee)
	limits, _ = json.Marshal(s.Limits)
	return publicKeys, receivingWallets, fee, limits
}

// nonNil returns a non-nil slice so pgx encodes an empty SQL array ('{}')
// instead of NULL for a NOT NULL text[] column.
func nonNil(ss []string) []string {
	if ss == nil {
		return []string{}
	}
	return ss
}

// scanner abstracts pgx.Row and pgx.Rows for scanService.
type scanner interface {
	Scan(dest ...any) error
}

func scanService(row scanner) (*domain.Service, error) {
	var (
		s        domain.Service
		pk, rw   []byte
		fee, lim []byte
		status   string
	)
	if err := row.Scan(
		&s.ID, &s.OwnerID, &s.Name, &pk, &s.Origins, &s.ExecuteURL, &s.StatusURL,
		&rw, &fee, &lim, &status, &s.CreatedAt, &s.UpdatedAt, &s.EncryptionPublicKey,
		&s.Description, &s.IconURL, &s.MiniappURL,
	); err != nil {
		return nil, err
	}
	s.Status = domain.Status(status)
	if err := json.Unmarshal(pk, &s.PublicKeys); err != nil {
		return nil, fmt.Errorf("decode public_keys: %w", err)
	}
	if err := json.Unmarshal(rw, &s.ReceivingWallets); err != nil {
		return nil, fmt.Errorf("decode receiving_wallets: %w", err)
	}
	if err := json.Unmarshal(fee, &s.Fee); err != nil {
		return nil, fmt.Errorf("decode fee: %w", err)
	}
	if err := json.Unmarshal(lim, &s.Limits); err != nil {
		return nil, fmt.Errorf("decode limits: %w", err)
	}
	return &s, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// cursor is the keyset pagination position.
type cursor struct {
	CreatedAt time.Time
	ID        string
}

func (c cursor) with(t time.Time, id string) cursor { return cursor{CreatedAt: t, ID: id} }

func encodeCursor(c cursor) string {
	raw := c.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + c.ID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeCursor(token string) (cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return cursor{}, err
	}
	parts := strings.SplitN(string(raw), "|", 2)
	if len(parts) != 2 {
		return cursor{}, errors.New("malformed cursor")
	}
	t, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return cursor{}, err
	}
	return cursor{CreatedAt: t, ID: parts[1]}, nil
}

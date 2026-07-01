// Package ledgerclient adapts the ledger gRPC service to the saga.Ledger port,
// so the payment saga settles against the real double-entry ledger instead of an
// in-memory mock. All operations are idempotent on orderId in the ledger, so the
// outbox dispatcher can retry safely.
package ledgerclient

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	ledgerv1 "github.com/nvsces/ledger/gen/ledger/v1"

	"github.com/service-constructor/engine/internal/saga"
)

// Client is a saga.Ledger backed by the ledger gRPC service.
type Client struct {
	conn *grpc.ClientConn
	svc  ledgerv1.LedgerServiceClient
}

// Dial connects to the ledger at addr (plaintext; the demo runs on a trusted
// network).
func Dial(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("dial ledger: %w", err)
	}
	return &Client{conn: conn, svc: ledgerv1.NewLedgerServiceClient(conn)}, nil
}

// Close releases the connection.
func (c *Client) Close() error { return c.conn.Close() }

// Freeze reserves funds on the user's wallet.
func (c *Client) Freeze(ctx context.Context, req saga.FreezeRequest) error {
	_, err := c.svc.Freeze(ctx, &ledgerv1.FreezeRequest{
		OrderId:    req.OrderID,
		WalletId:   req.WalletID,
		Amount:     req.Amount,
		CurrencyId: req.CurrencyID,
	})
	return err
}

// Capture settles held funds: net to the service wallet, fee to the platform.
func (c *Client) Capture(ctx context.Context, req saga.CaptureRequest) error {
	_, err := c.svc.Capture(ctx, &ledgerv1.CaptureRequest{
		OrderId:           req.OrderID,
		WalletId:          req.WalletID,
		Net:               req.Net,
		Fee:               req.Fee,
		ReceivingWalletId: req.ReceivingWalletID,
		CurrencyId:        req.CurrencyID,
	})
	return err
}

// Release returns held funds to the user's available balance.
func (c *Client) Release(ctx context.Context, req saga.ReleaseRequest) error {
	_, err := c.svc.Release(ctx, &ledgerv1.ReleaseRequest{
		OrderId:    req.OrderID,
		WalletId:   req.WalletID,
		CurrencyId: req.CurrencyID,
	})
	return err
}

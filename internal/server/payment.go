package server

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	scv1 "github.com/nvsces/service-constructor/gen/serviceconstructor/v1"
	"github.com/nvsces/service-constructor/internal/auth"
	"github.com/nvsces/service-constructor/internal/domain"
	"github.com/nvsces/service-constructor/internal/saga"
)

// ServiceLookup resolves services, unscoped. GetServiceInfo uses Lookup to
// return public info for one service; ListServiceInfo uses ListActive to return
// the public catalog of ACTIVE services a shell can render.
type ServiceLookup interface {
	Lookup(ctx context.Context, serviceID string) (*domain.Service, error)
	ListActive(ctx context.Context) ([]*domain.Service, error)
}

// PaymentServer adapts the PaymentService gRPC contract to the saga
// orchestrator.
type PaymentServer struct {
	scv1.UnimplementedPaymentServiceServer
	orch     *saga.Orchestrator
	orders   saga.OrderStore
	services ServiceLookup
}

// NewPaymentServer wires the payment adapter.
func NewPaymentServer(orch *saga.Orchestrator, orders saga.OrderStore, services ServiceLookup) *PaymentServer {
	return &PaymentServer{orch: orch, orders: orders, services: services}
}

func (s *PaymentServer) Pay(ctx context.Context, req *scv1.PayRequest) (*scv1.Order, error) {
	// Consent is optional: when the platform runs CONSENT_MODE=none, a trusted
	// shell pays over the authenticated session with no device-signed consent.
	// The orchestrator enforces consent only when configured to require it.
	if req.GetQuote() == nil {
		return nil, status.Error(codes.InvalidArgument, "quote is required")
	}

	// The authenticated session identifies the user; quote.userId is checked
	// against it inside the orchestrator.
	authUserID := ""
	if p, ok := auth.PrincipalFromContext(ctx); ok && p != nil {
		authUserID = p.Subject
	}

	cmd := saga.PayCommand{
		Quote:                    quoteToDomain(req.GetQuote()),
		SelectedWalletID:         req.GetSelectedWalletId(),
		SelectedWalletCurrencyID: req.GetSelectedWalletCurrencyId(),
		Consent:                  consentToDomain(req.GetConsent()),
		AuthUserID:               authUserID,
	}

	order, err := s.orch.Pay(ctx, cmd)
	if err != nil {
		return nil, payErrToStatus(err)
	}
	return orderToProto(order), nil
}

func (s *PaymentServer) Callback(ctx context.Context, req *scv1.CallbackRequest) (*scv1.Order, error) {
	if req.GetOrderId() == "" || req.GetSig() == "" {
		return nil, status.Error(codes.InvalidArgument, "orderId and sig are required")
	}
	order, err := s.orch.ProcessCallback(ctx, saga.Callback{
		OrderID:     req.GetOrderId(),
		Status:      req.GetStatus(),
		ExternalRef: req.GetExternalRef(),
		Kid:         req.GetKid(),
		Sig:         req.GetSig(),
	})
	if err != nil {
		return nil, payErrToStatus(err)
	}
	return orderToProto(order), nil
}

func (s *PaymentServer) GetOrder(ctx context.Context, req *scv1.GetOrderRequest) (*scv1.Order, error) {
	order, err := s.orders.Get(ctx, req.GetOrderId())
	if err != nil {
		return nil, payErrToStatus(err)
	}
	// Guard against cross-service order lookups.
	if req.GetServiceId() != "" && order.ServiceID != req.GetServiceId() {
		return nil, status.Error(codes.NotFound, domain.ErrOrderNotFound.Error())
	}
	return orderToProto(order), nil
}

func (s *PaymentServer) GetServiceInfo(ctx context.Context, req *scv1.GetServiceInfoRequest) (*scv1.ServiceInfo, error) {
	if req.GetServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "service_id is required")
	}
	svc, err := s.services.Lookup(ctx, req.GetServiceId())
	if err != nil {
		return nil, payErrToStatus(err)
	}
	return serviceInfoToProto(svc), nil
}

// ListServiceInfo returns the public catalog of ACTIVE services so a shell can
// render its app list. Returns no secrets — only the public ServiceInfo view.
func (s *PaymentServer) ListServiceInfo(ctx context.Context, _ *scv1.ListServiceInfoRequest) (*scv1.ListServiceInfoResponse, error) {
	svcs, err := s.services.ListActive(ctx)
	if err != nil {
		return nil, payErrToStatus(err)
	}
	out := make([]*scv1.ServiceInfo, 0, len(svcs))
	for _, svc := range svcs {
		out = append(out, serviceInfoToProto(svc))
	}
	return &scv1.ListServiceInfoResponse{Services: out}, nil
}

// serviceInfoToProto projects a domain Service to its public catalog view.
func serviceInfoToProto(svc *domain.Service) *scv1.ServiceInfo {
	return &scv1.ServiceInfo{
		ServiceId:           svc.ID,
		Name:                svc.Name,
		Origins:             svc.Origins,
		EncryptionPublicKey: svc.EncryptionPublicKey,
		Description:         svc.Description,
		IconUrl:             svc.IconURL,
		MiniappUrl:          svc.MiniappURL,
	}
}

func quoteToDomain(q *scv1.Quote) saga.Quote {
	meta := make(map[string]any, len(q.GetMetadata()))
	for k, v := range q.GetMetadata() {
		meta[k] = v
	}
	return saga.Quote{
		Version:             int(q.GetVersion()),
		ServiceID:           q.GetServiceId(),
		UserID:              q.GetUserId(),
		Amount:              q.GetAmount(),
		CurrencyID:          q.GetCurrencyId(),
		AcceptedCurrencyIDs: q.GetAcceptedCurrencyIds(),
		Description:         q.GetDescription(),
		Metadata:            meta,
		Nonce:               q.GetNonce(),
		Exp:                 q.GetExp(),
		Kid:                 q.GetKid(),
		Sig:                 q.GetSig(),
	}
}

func consentToDomain(c *scv1.Consent) saga.Consent {
	return saga.Consent{
		QuoteHash: c.GetQuoteHash(),
		WalletID:  c.GetWalletId(),
		Nonce:     c.GetNonce(),
		Ts:        c.GetTs(),
		DeviceKid: c.GetDeviceKid(),
		Sig:       c.GetSig(),
	}
}

func orderToProto(o *domain.Order) *scv1.Order {
	p := &scv1.Order{
		OrderId:     o.ID,
		ServiceId:   o.ServiceID,
		UserId:      o.UserID,
		WalletId:    o.WalletID,
		Amount:      o.Amount,
		CurrencyId:  o.CurrencyID,
		Fee:         o.Fee,
		Net:         o.Net,
		ExternalRef: o.ExternalRef,
		State:       orderStateToProto(o.State),
	}
	if !o.CreatedAt.IsZero() {
		p.CreatedAt = timestamppb.New(o.CreatedAt)
	}
	if !o.UpdatedAt.IsZero() {
		p.UpdatedAt = timestamppb.New(o.UpdatedAt)
	}
	return p
}

func orderStateToProto(s domain.OrderState) scv1.OrderState {
	switch s {
	case domain.OrderCreated:
		return scv1.OrderState_ORDER_STATE_CREATED
	case domain.OrderFrozen:
		return scv1.OrderState_ORDER_STATE_FROZEN
	case domain.OrderExecuting:
		return scv1.OrderState_ORDER_STATE_EXECUTING
	case domain.OrderPending:
		return scv1.OrderState_ORDER_STATE_PENDING
	case domain.OrderExecuted:
		return scv1.OrderState_ORDER_STATE_EXECUTED
	case domain.OrderCompleted:
		return scv1.OrderState_ORDER_STATE_COMPLETED
	case domain.OrderRejected:
		return scv1.OrderState_ORDER_STATE_REJECTED
	case domain.OrderFailed:
		return scv1.OrderState_ORDER_STATE_FAILED
	case domain.OrderReleased:
		return scv1.OrderState_ORDER_STATE_RELEASED
	default:
		return scv1.OrderState_ORDER_STATE_UNSPECIFIED
	}
}

// payErrToStatus maps saga/domain errors to gRPC status codes.
func payErrToStatus(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, domain.ErrOrderNotFound), errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrInvalidSignature), errors.Is(err, domain.ErrConsentMismatch):
		return status.Error(codes.PermissionDenied, err.Error())
	case errors.Is(err, domain.ErrQuoteExpired):
		return status.Error(codes.FailedPrecondition, err.Error())
	case errors.Is(err, domain.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, domain.ErrIdempotencyConflict):
		return status.Error(codes.Aborted, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

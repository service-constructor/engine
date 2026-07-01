// Package server adapts the gRPC ServiceRegistry contract to the use-case
// layer, translating between proto and domain types and mapping domain errors
// to gRPC status codes.
package server

import (
	"context"
	"errors"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	scv1 "github.com/nvsces/service-constructor/gen/serviceconstructor/v1"
	"github.com/nvsces/service-constructor/internal/auth"
	"github.com/nvsces/service-constructor/internal/domain"
	"github.com/nvsces/service-constructor/internal/service"
)

// scopeFromContext derives the data-access scope from the authenticated
// principal: super-admins see all owners, everyone else is limited to the
// services they own (keyed by the principal subject).
func scopeFromContext(ctx context.Context) service.Scope {
	p, ok := auth.PrincipalFromContext(ctx)
	if !ok || p == nil {
		// No principal (e.g. AUTH_MODE=none with the AllowAll authenticator that
		// still injects a dev principal). Treat as a single shared owner.
		return service.Scope{OwnerID: ""}
	}
	if p.HasRole(auth.RoleSuperAdmin) {
		return service.Scope{AllOwners: true}
	}
	return service.Scope{OwnerID: p.Subject}
}

// RegistryServer implements scv1.ServiceRegistryServer.
type RegistryServer struct {
	scv1.UnimplementedServiceRegistryServer
	reg *service.Registry
}

// NewRegistryServer wires the gRPC adapter to the registry use case.
func NewRegistryServer(reg *service.Registry) *RegistryServer {
	return &RegistryServer{reg: reg}
}

func (s *RegistryServer) CreateService(ctx context.Context, req *scv1.CreateServiceRequest) (*scv1.Service, error) {
	if req.GetService() == nil {
		return nil, status.Error(codes.InvalidArgument, "service is required")
	}
	created, err := s.reg.Create(ctx, scopeFromContext(ctx), protoToDomain(req.GetService()))
	if err != nil {
		return nil, toStatus(err)
	}
	return domainToProto(created), nil
}

func (s *RegistryServer) GetService(ctx context.Context, req *scv1.GetServiceRequest) (*scv1.Service, error) {
	svc, err := s.reg.Get(ctx, scopeFromContext(ctx), req.GetServiceId())
	if err != nil {
		return nil, toStatus(err)
	}
	return domainToProto(svc), nil
}

func (s *RegistryServer) ListServices(ctx context.Context, req *scv1.ListServicesRequest) (*scv1.ListServicesResponse, error) {
	svcs, next, err := s.reg.List(ctx, scopeFromContext(ctx), service.ListFilter{
		Status:    statusToDomain(req.GetStatus()),
		PageSize:  int(req.GetPageSize()),
		PageToken: req.GetPageToken(),
	})
	if err != nil {
		return nil, toStatus(err)
	}
	resp := &scv1.ListServicesResponse{NextPageToken: next}
	for _, svc := range svcs {
		resp.Services = append(resp.Services, domainToProto(svc))
	}
	return resp, nil
}

func (s *RegistryServer) UpdateService(ctx context.Context, req *scv1.UpdateServiceRequest) (*scv1.Service, error) {
	in := req.GetService()
	if in == nil || in.GetServiceId() == "" {
		return nil, status.Error(codes.InvalidArgument, "service.service_id is required")
	}
	// Load current, apply masked fields, then persist. This gives PATCH
	// semantics: only fields named in update_mask change (empty mask = all).
	scope := scopeFromContext(ctx)
	current, err := s.reg.Get(ctx, scope, in.GetServiceId())
	if err != nil {
		return nil, toStatus(err)
	}
	merged := mergeUpdate(current, in, req.GetUpdateMask().GetPaths())
	updated, err := s.reg.Update(ctx, scope, merged)
	if err != nil {
		return nil, toStatus(err)
	}
	return domainToProto(updated), nil
}

func (s *RegistryServer) DeleteService(ctx context.Context, req *scv1.DeleteServiceRequest) (*scv1.DeleteServiceResponse, error) {
	if err := s.reg.Delete(ctx, scopeFromContext(ctx), req.GetServiceId()); err != nil {
		return nil, toStatus(err)
	}
	return &scv1.DeleteServiceResponse{}, nil
}

func (s *RegistryServer) GenerateServiceKey(ctx context.Context, req *scv1.GenerateServiceKeyRequest) (*scv1.GenerateServiceKeyResponse, error) {
	return s.generateKey(ctx, req.GetServiceId(), req.GetAlgorithm(), "")
}

func (s *RegistryServer) RotateServiceKey(ctx context.Context, req *scv1.RotateServiceKeyRequest) (*scv1.GenerateServiceKeyResponse, error) {
	return s.generateKey(ctx, req.GetServiceId(), req.GetAlgorithm(), req.GetRetireKid())
}

func (s *RegistryServer) generateKey(ctx context.Context, serviceID string, alg scv1.KeyAlgorithm, retireKID string) (*scv1.GenerateServiceKeyResponse, error) {
	svc, pair, err := s.reg.GenerateKey(ctx, scopeFromContext(ctx), serviceID, algToDomain(alg), retireKID)
	if err != nil {
		return nil, toStatus(err)
	}
	return &scv1.GenerateServiceKeyResponse{
		PublicKey:     &scv1.PublicKey{Kid: pair.KID, Pem: pair.PublicKeyPEM},
		PrivateKeyPem: pair.PrivateKeyPEM,
		Service:       domainToProto(svc),
	}, nil
}

// mergeUpdate returns current with the fields named in paths overwritten from
// the inbound proto. An empty paths slice means "replace all mutable fields".
//
// Mask paths arrive relative to the request message and are prefixed with the
// body field name ("service."); the gateway derives them from the JSON body
// when no explicit update_mask is supplied. We normalize them to bare field
// names before matching.
func mergeUpdate(current *domain.Service, in *scv1.Service, paths []string) *domain.Service {
	incoming := protoToDomain(in)
	out := *current // copy; id and timestamps preserved

	normalized := make(map[string]struct{}, len(paths))
	for _, p := range paths {
		normalized[strings.TrimPrefix(p, "service.")] = struct{}{}
	}

	set := func(path string) bool {
		if len(normalized) == 0 {
			return true
		}
		_, ok := normalized[path]
		return ok
	}

	if set("name") {
		out.Name = incoming.Name
	}
	if set("public_keys") {
		out.PublicKeys = incoming.PublicKeys
	}
	if set("origins") {
		out.Origins = incoming.Origins
	}
	if set("execute_url") {
		out.ExecuteURL = incoming.ExecuteURL
	}
	if set("status_url") {
		out.StatusURL = incoming.StatusURL
	}
	if set("encryption_public_key") {
		out.EncryptionPublicKey = incoming.EncryptionPublicKey
	}
	if set("description") {
		out.Description = incoming.Description
	}
	if set("icon_url") {
		out.IconURL = incoming.IconURL
	}
	if set("miniapp_url") {
		out.MiniappURL = incoming.MiniappURL
	}
	if set("receiving_wallets") {
		out.ReceivingWallets = incoming.ReceivingWallets
	}
	if set("fee") {
		out.Fee = incoming.Fee
	}
	if set("limits") {
		out.Limits = incoming.Limits
	}
	if set("status") && incoming.Status != "" {
		out.Status = incoming.Status
	}
	return &out
}

// toStatus maps domain/use-case errors to gRPC status codes.
func toStatus(err error) error {
	switch {
	case errors.Is(err, domain.ErrNotFound):
		return status.Error(codes.NotFound, err.Error())
	case errors.Is(err, domain.ErrAlreadyExists):
		return status.Error(codes.AlreadyExists, err.Error())
	case errors.Is(err, domain.ErrInvalidArgument):
		return status.Error(codes.InvalidArgument, err.Error())
	default:
		return status.Error(codes.Internal, err.Error())
	}
}

package server

import (
	"google.golang.org/protobuf/types/known/timestamppb"

	scv1 "github.com/nvsces/service-constructor/gen/serviceconstructor/v1"
	"github.com/nvsces/service-constructor/internal/domain"
)

// statusToDomain converts a proto enum to the domain status. UNSPECIFIED maps
// to an empty Status, which the registry treats as "default / match all".
func statusToDomain(s scv1.ServiceStatus) domain.Status {
	switch s {
	case scv1.ServiceStatus_SERVICE_STATUS_DRAFT:
		return domain.StatusDraft
	case scv1.ServiceStatus_SERVICE_STATUS_ACTIVE:
		return domain.StatusActive
	case scv1.ServiceStatus_SERVICE_STATUS_SUSPENDED:
		return domain.StatusSuspended
	default:
		return ""
	}
}

func statusToProto(s domain.Status) scv1.ServiceStatus {
	switch s {
	case domain.StatusDraft:
		return scv1.ServiceStatus_SERVICE_STATUS_DRAFT
	case domain.StatusActive:
		return scv1.ServiceStatus_SERVICE_STATUS_ACTIVE
	case domain.StatusSuspended:
		return scv1.ServiceStatus_SERVICE_STATUS_SUSPENDED
	default:
		return scv1.ServiceStatus_SERVICE_STATUS_UNSPECIFIED
	}
}

// protoToDomain converts an inbound proto Service into a domain Service.
// Server-assigned fields (id, timestamps) are intentionally not copied; the
// caller decides whether to honor them.
func protoToDomain(p *scv1.Service) *domain.Service {
	if p == nil {
		return &domain.Service{}
	}
	d := &domain.Service{
		ID:         p.GetServiceId(),
		Name:       p.GetName(),
		Origins:    p.GetOrigins(),
		ExecuteURL: p.GetExecuteUrl(),
		StatusURL:  p.GetStatusUrl(),
		Status:     statusToDomain(p.GetStatus()),
	}
	for _, k := range p.GetPublicKeys() {
		d.PublicKeys = append(d.PublicKeys, domain.PublicKey{KID: k.GetKid(), PEM: k.GetPem()})
	}
	for _, w := range p.GetReceivingWallets() {
		d.ReceivingWallets = append(d.ReceivingWallets, domain.ReceivingWallet{
			CurrencyID: w.GetCurrencyId(),
			WalletID:   w.GetWalletId(),
		})
	}
	if f := p.GetFee(); f != nil {
		d.Fee = domain.Fee{Percent: f.GetPercent(), Fixed: f.GetFixed()}
	}
	if l := p.GetLimits(); l != nil {
		d.Limits = domain.Limits{MaxAmount: l.GetMaxAmount(), PerHour: l.GetPerHour()}
	}
	return d
}

// domainToProto converts a stored domain Service into its proto representation.
func domainToProto(d *domain.Service) *scv1.Service {
	p := &scv1.Service{
		ServiceId:  d.ID,
		Name:       d.Name,
		Origins:    d.Origins,
		ExecuteUrl: d.ExecuteURL,
		StatusUrl:  d.StatusURL,
		Status:     statusToProto(d.Status),
		Fee:        &scv1.Fee{Percent: d.Fee.Percent, Fixed: d.Fee.Fixed},
		Limits:     &scv1.Limits{MaxAmount: d.Limits.MaxAmount, PerHour: d.Limits.PerHour},
	}
	for _, k := range d.PublicKeys {
		p.PublicKeys = append(p.PublicKeys, &scv1.PublicKey{Kid: k.KID, Pem: k.PEM})
	}
	for _, w := range d.ReceivingWallets {
		p.ReceivingWallets = append(p.ReceivingWallets, &scv1.ReceivingWallet{
			CurrencyId: w.CurrencyID,
			WalletId:   w.WalletID,
		})
	}
	if !d.CreatedAt.IsZero() {
		p.CreatedAt = timestamppb.New(d.CreatedAt)
	}
	if !d.UpdatedAt.IsZero() {
		p.UpdatedAt = timestamppb.New(d.UpdatedAt)
	}
	return p
}

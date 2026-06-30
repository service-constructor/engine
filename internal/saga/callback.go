package saga

import (
	"encoding/json"

	"github.com/nvsces/service-constructor/internal/domain"
)

// Callback is the provider's async finalization message (white paper section 9).
// It is signed with the service private key and verified against the registry
// public key (by kid), giving the same non-repudiation as the quote.
type Callback struct {
	OrderID string `json:"orderId"`
	// Status is "SUCCESS" or "FAILED".
	Status      string `json:"status"`
	ExternalRef string `json:"externalRef"`
	Kid         string `json:"kid"`
	// Sig is the base64 signature over the canonical callback (excluding Sig).
	Sig string `json:"sig"`
}

// Success reports whether the callback signals a successful execution.
func (c Callback) Success() bool { return c.Status == "SUCCESS" }

// canonicalCallbackBytes returns the deterministic JSON over which the callback
// is signed: every field except sig.
func canonicalCallbackBytes(c Callback) ([]byte, error) {
	unsigned := c
	unsigned.Sig = ""
	return json.Marshal(unsigned)
}

// VerifyCallbackSignature checks the webhook signature against the service
// public key (selected by kid) from the registry record.
func VerifyCallbackSignature(c Callback, svc *domain.Service) error {
	pem := publicKeyPEMByKID(svc, c.Kid)
	if pem == "" {
		return domain.ErrInvalidSignature
	}
	msg, err := canonicalCallbackBytes(c)
	if err != nil {
		return err
	}
	return verifySignature(pem, msg, c.Sig)
}

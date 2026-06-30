package saga

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"

	"github.com/nvsces/service-constructor/internal/domain"
)

// canonicalQuoteBytes returns the deterministic JSON over which the quote is
// signed: every field except sig. Using encoding/json with a fixed struct field
// order yields a stable byte sequence both sides agree on.
func canonicalQuoteBytes(q Quote) ([]byte, error) {
	unsigned := q
	unsigned.Sig = ""
	return json.Marshal(unsigned)
}

// canonicalConsentBytes returns the deterministic JSON over which the consent is
// signed: every field except sig.
func canonicalConsentBytes(c Consent) ([]byte, error) {
	unsigned := c
	unsigned.Sig = ""
	return json.Marshal(unsigned)
}

// QuoteHash returns the hex sha256 of the canonical quote bytes. The consent's
// QuoteHash must equal this, binding the user's approval to the exact quote.
func QuoteHash(q Quote) (string, error) {
	b, err := canonicalQuoteBytes(q)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// VerifyQuoteSignature checks the quote signature against the service public key
// (selected by kid) from the registry record. Supports Ed25519 and ECDSA P-256.
func VerifyQuoteSignature(q Quote, svc *domain.Service) error {
	pem := publicKeyPEMByKID(svc, q.Kid)
	if pem == "" {
		return fmt.Errorf("%w: unknown quote kid %q", domain.ErrInvalidSignature, q.Kid)
	}
	msg, err := canonicalQuoteBytes(q)
	if err != nil {
		return err
	}
	return verifySignature(pem, msg, q.Sig)
}

// VerifyConsentSignature checks the consent signature against the device public
// key. The device key is supplied by the caller (the wallet shell knows it);
// here we accept the PEM so the platform can validate without storing device
// keys itself.
func VerifyConsentSignature(c Consent, devicePublicKeyPEM string) error {
	if devicePublicKeyPEM == "" {
		return fmt.Errorf("%w: missing device public key", domain.ErrInvalidSignature)
	}
	msg, err := canonicalConsentBytes(c)
	if err != nil {
		return err
	}
	return verifySignature(devicePublicKeyPEM, msg, c.Sig)
}

// publicKeyPEMByKID finds a registered public key by kid.
func publicKeyPEMByKID(svc *domain.Service, kid string) string {
	for _, k := range svc.PublicKeys {
		if k.KID == kid {
			return k.PEM
		}
	}
	return ""
}

// verifySignature verifies a base64 signature over msg using a PEM-encoded
// public key (PKIX). Ed25519 and ECDSA (ASN.1 DER signature) are supported.
func verifySignature(publicKeyPEM string, msg []byte, sigB64 string) error {
	sig, err := base64.StdEncoding.DecodeString(sigB64)
	if err != nil {
		return fmt.Errorf("%w: signature not base64", domain.ErrInvalidSignature)
	}
	pub, err := parsePublicKey(publicKeyPEM)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidSignature, err)
	}

	switch key := pub.(type) {
	case ed25519.PublicKey:
		if !ed25519.Verify(key, msg, sig) {
			return domain.ErrInvalidSignature
		}
		return nil
	case *ecdsa.PublicKey:
		sum := sha256.Sum256(msg)
		if !ecdsa.VerifyASN1(key, sum[:], sig) {
			return domain.ErrInvalidSignature
		}
		return nil
	default:
		return fmt.Errorf("%w: unsupported key type", domain.ErrInvalidSignature)
	}
}

func parsePublicKey(pemStr string) (any, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	return x509.ParsePKIXPublicKey(block.Bytes)
}

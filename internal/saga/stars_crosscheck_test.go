package saga

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/service-constructor/engine/internal/domain"
)

// TestStarsServiceQuoteSignature verifies that a quote signed by
// telegram-stars-service (Go) is accepted by the platform's verifier. It reads
// the output of that service's TestEmitCrosscheckQuote from /tmp/stars-cross.json.
// Skipped if absent.
func TestStarsServiceQuoteSignature(t *testing.T) {
	data, err := os.ReadFile("/tmp/stars-cross.json")
	if err != nil {
		t.Skip("no /tmp/stars-cross.json; run telegram-stars-service TestEmitCrosscheckQuote first")
	}
	var c struct {
		PublicKeyPEM string `json:"publicKeyPEM"`
		Canonical    string `json:"canonical"`
		Quote        Quote  `json:"quote"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal stars-cross.json: %v", err)
	}

	// 1. Canonical bytes must match the platform's exactly.
	goCanon, err := canonicalQuoteBytes(c.Quote)
	if err != nil {
		t.Fatal(err)
	}
	if string(goCanon) != c.Canonical {
		t.Fatalf("canonical mismatch:\n stars: %s\n engine: %s", c.Canonical, goCanon)
	}

	// 2. The stars-service signature must verify against its public key.
	svc := &domain.Service{PublicKeys: []domain.PublicKey{{KID: c.Quote.Kid, PEM: c.PublicKeyPEM}}}
	if err := VerifyQuoteSignature(c.Quote, svc); err != nil {
		t.Fatalf("verify stars-service quote: %v", err)
	}
}

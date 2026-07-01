package saga

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/service-constructor/engine/internal/domain"
)

// TestCrossLanguageQuoteSignature verifies that a quote signed by the Node/TS
// reference service is accepted by the platform's verifier. It reads the output
// of `npx tsx src/crosscheck.ts` from /tmp/cross.json. Skipped if absent.
func TestCrossLanguageQuoteSignature(t *testing.T) {
	data, err := os.ReadFile("/tmp/cross.json")
	if err != nil {
		t.Skip("no /tmp/cross.json; run the TS crosscheck first")
	}
	var c struct {
		PublicKeyPEM string `json:"publicKeyPEM"`
		Canonical    string `json:"canonical"`
		Quote        Quote  `json:"quote"`
	}
	if err := json.Unmarshal(data, &c); err != nil {
		t.Fatalf("unmarshal cross.json: %v", err)
	}

	// 1. Canonical bytes must match Go's exactly.
	goCanon, err := canonicalQuoteBytes(c.Quote)
	if err != nil {
		t.Fatal(err)
	}
	if string(goCanon) != c.Canonical {
		t.Fatalf("canonical mismatch:\n TS: %s\n Go: %s", c.Canonical, goCanon)
	}

	// 2. The TS signature must verify against the TS public key (registered
	// under the quote's own kid).
	svc := &domain.Service{PublicKeys: []domain.PublicKey{{KID: c.Quote.Kid, PEM: c.PublicKeyPEM}}}
	if err := VerifyQuoteSignature(c.Quote, svc); err != nil {
		t.Fatalf("verify TS-signed quote: %v", err)
	}
}

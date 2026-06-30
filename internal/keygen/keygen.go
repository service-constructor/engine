// Package keygen generates asymmetric key pairs for services. The platform
// stores only the public key; the private key is returned to the integrator
// once at generation time and never persisted.
package keygen

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Algorithm identifies the key type to generate.
type Algorithm string

const (
	AlgorithmEd25519 Algorithm = "ed25519"
	AlgorithmECP256  Algorithm = "ec_p256"
)

// KeyPair is a freshly generated pair in PEM form, plus its kid.
type KeyPair struct {
	KID           string
	PublicKeyPEM  string
	PrivateKeyPEM string
}

// Generate produces a key pair for the given algorithm. The kid is derived from
// a UUID and the current key generation is not encoded into it; callers pass a
// human prefix (e.g. the service id) for traceability.
func Generate(alg Algorithm, kidPrefix string) (KeyPair, error) {
	switch alg {
	case AlgorithmEd25519, "":
		return generateEd25519(kidPrefix)
	case AlgorithmECP256:
		return generateECP256(kidPrefix)
	default:
		return KeyPair{}, fmt.Errorf("unsupported algorithm %q", alg)
	}
}

func generateEd25519(kidPrefix string) (KeyPair, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate ed25519: %w", err)
	}
	return encode(kidPrefix, pub, priv)
}

func generateECP256(kidPrefix string) (KeyPair, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return KeyPair{}, fmt.Errorf("generate ec p256: %w", err)
	}
	return encode(kidPrefix, priv.Public(), priv)
}

// encode marshals public (PKIX) and private (PKCS#8) keys to PEM.
func encode(kidPrefix string, pub, priv any) (KeyPair, error) {
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return KeyPair{}, fmt.Errorf("marshal public: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return KeyPair{}, fmt.Errorf("marshal private: %w", err)
	}
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})
	privPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})
	return KeyPair{
		KID:           newKID(kidPrefix),
		PublicKeyPEM:  string(pubPEM),
		PrivateKeyPEM: string(privPEM),
	}, nil
}

// newKID builds a unique key id, optionally prefixed for traceability.
func newKID(prefix string) string {
	id := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	if prefix == "" {
		return "key_" + id
	}
	return prefix + "_" + id
}

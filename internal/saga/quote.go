package saga

// Quote is the signed payment instruction issued by a service (white paper §7).
// It is signed with the service's private key; the platform verifies it against
// the public key in the registry (by kid). The signed bytes are the canonical
// JSON of every field except Sig.
type Quote struct {
	Version             int            `json:"version"`
	ServiceID           string         `json:"serviceId"`
	UserID              string         `json:"userId"`
	Amount              string         `json:"amount"`
	CurrencyID          int64          `json:"currencyId"`
	AcceptedCurrencyIDs []int64        `json:"acceptedCurrencyIds"`
	Description         string         `json:"description"`
	Metadata            map[string]any `json:"metadata,omitempty"`
	Nonce               string         `json:"nonce"`
	// Exp is the quote expiry as a Unix timestamp (seconds).
	Exp int64  `json:"exp"`
	Kid string `json:"kid"`
	// Sig is the base64 signature over the canonical quote (excluding Sig).
	Sig string `json:"sig"`
}

// Consent is the device-signed approval the wallet shell produces after the
// user confirms on the trusted screen. It binds the approval to a specific
// quote (QuoteHash) and source wallet, and cannot be forged or replayed
// (white paper §7).
type Consent struct {
	// QuoteHash is sha256 over the canonical quote bytes (hex).
	QuoteHash string `json:"quoteHash"`
	WalletID  string `json:"walletId"`
	Nonce     string `json:"nonce"`
	Ts        int64  `json:"ts"`
	// DeviceKid identifies the device key that signed this consent.
	DeviceKid string `json:"deviceKid"`
	// Sig is the base64 device signature over the canonical consent.
	Sig string `json:"sig"`
}

// PayCommand carries everything POST /v1/services/pay needs: the signed quote,
// the user's selected wallet, and the device-signed consent. AuthUserID is the
// userId from the authenticated session, checked against quote.userId.
type PayCommand struct {
	Quote            Quote
	SelectedWalletID string
	Consent          Consent
	AuthUserID       string
	// UserWalletCurrency maps the selected wallet to its currency, supplied by
	// the wallet shell / ledger so the platform can check the wallet's currency
	// is accepted by the service.
	SelectedWalletCurrencyID int64
}

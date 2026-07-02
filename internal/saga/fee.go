package saga

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/service-constructor/engine/internal/domain"
)

// splitFee computes the platform fee and the net amount routed to the service,
// from the registry fee (percent and/or fixed). amount is the full quote amount.
//
// fee = amount * percent/100 + fixed, clamped to [0, amount].
// net = amount - fee.
//
// The fee is exact — NOT rounded. The ledger stores NUMERIC(38,18), so a precise
// value like 0.05 or 0.025 is represented faithfully. (An earlier version rounded
// the fee to the price's decimal places, which overcharged short-scale prices:
// a 10% fee on "0.5" rounded 0.05 up to 0.1 = 20%.)
func splitFee(amount string, fee domain.Fee) (net string, feeOut string, err error) {
	amt, err := decimal.NewFromString(amount)
	if err != nil {
		return "", "", fmt.Errorf("%w: bad amount %q", domain.ErrInvalidArgument, amount)
	}
	if amt.IsNegative() {
		return "", "", fmt.Errorf("%w: negative amount", domain.ErrInvalidArgument)
	}

	total := decimal.Zero
	if fee.Percent != "" {
		pct, err := decimal.NewFromString(fee.Percent)
		if err != nil {
			return "", "", fmt.Errorf("%w: bad fee percent %q", domain.ErrInvalidArgument, fee.Percent)
		}
		total = total.Add(amt.Mul(pct).Div(decimal.NewFromInt(100)))
	}
	if fee.Fixed != "" {
		fx, err := decimal.NewFromString(fee.Fixed)
		if err != nil {
			return "", "", fmt.Errorf("%w: bad fee fixed %q", domain.ErrInvalidArgument, fee.Fixed)
		}
		total = total.Add(fx)
	}

	// Clamp so the fee never exceeds the amount (or goes negative).
	if total.GreaterThan(amt) {
		total = amt
	}
	if total.IsNegative() {
		total = decimal.Zero
	}

	netDec := amt.Sub(total)
	return netDec.String(), total.String(), nil
}

package saga

import (
	"fmt"

	"github.com/shopspring/decimal"

	"github.com/service-constructor/engine/internal/domain"
)

// splitFee computes the platform fee and the net amount routed to the service,
// from the registry fee (percent and/or fixed). amount is the full quote
// amount. Results are decimal strings with the same scale handling as input.
//
// fee = round(amount * percent/100) + fixed, clamped to [0, amount].
// net = amount - fee.
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

	// Match the amount's scale (number of decimal places) for currency-clean
	// values, then clamp so fee never exceeds the amount.
	scale := amt.Exponent()
	if scale < 0 {
		total = total.Round(-scale)
	}
	if total.GreaterThan(amt) {
		total = amt
	}
	if total.IsNegative() {
		total = decimal.Zero
	}

	netDec := amt.Sub(total)
	return netDec.String(), total.String(), nil
}

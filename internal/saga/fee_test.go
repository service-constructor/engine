package saga

import (
	"testing"

	"github.com/service-constructor/engine/internal/domain"
)

func TestSplitFeeExactNoRounding(t *testing.T) {
	cases := []struct {
		amount  string
		fee     domain.Fee
		wantNet string
		wantFee string
	}{
		// Regression: a 10% fee on a short-scale price must be exact, not rounded
		// up to the price's decimal places (0.5 -> 0.05, not 0.1 = 20%).
		{"0.5", domain.Fee{Percent: "10"}, "0.45", "0.05"},
		{"2.5", domain.Fee{Percent: "10"}, "2.25", "0.25"},
		{"0.05", domain.Fee{Percent: "10"}, "0.045", "0.005"},
		// Whole and already-round values unaffected.
		{"1", domain.Fee{Percent: "10"}, "0.9", "0.1"},
		{"10", domain.Fee{Percent: "10"}, "9", "1"},
		{"2.50", domain.Fee{Percent: "10"}, "2.25", "0.25"},
		// Fixed component adds on top.
		{"1", domain.Fee{Percent: "10", Fixed: "0.02"}, "0.88", "0.12"},
		// No fee configured.
		{"5", domain.Fee{}, "5", "0"},
		// Fee clamped to amount when it would exceed it.
		{"1", domain.Fee{Fixed: "5"}, "0", "1"},
	}
	for _, c := range cases {
		net, fee, err := splitFee(c.amount, c.fee)
		if err != nil {
			t.Fatalf("splitFee(%s,%+v): %v", c.amount, c.fee, err)
		}
		if net != c.wantNet || fee != c.wantFee {
			t.Errorf("splitFee(%s, pct=%q fixed=%q) = net %s / fee %s, want net %s / fee %s",
				c.amount, c.fee.Percent, c.fee.Fixed, net, fee, c.wantNet, c.wantFee)
		}
	}
}

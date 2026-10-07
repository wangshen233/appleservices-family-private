package icloud

import (
	"encoding/json"
	"testing"
)

func TestParseStoragePlanInfo(t *testing.T) {
	body := map[string]any{
		"storefront":   "CN",
		"currentQuota": float64(5 * 1024 * 1024 * 1024),
		"plans": []any{
			map[string]any{"sku": "CN-50GB", "capacityBytes": float64(50 * 1024 * 1024 * 1024), "price": float64(600), "currency": "CNY", "period": "month", "available": true},
		},
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	info, err := parseStoragePlanInfo(data)
	if err != nil {
		t.Fatal(err)
	}
	if info.Region != "CN" || info.CurrentBytes == 0 || len(info.Plans) != 1 {
		t.Fatalf("unexpected plan info: %+v", info)
	}
	plan := info.Plans[0]
	if plan.ID != "CN-50GB" || plan.Price != 600 || plan.Currency != "CNY" || !plan.Available {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

func TestParseStoragePlanInfoRejectsMissingPlans(t *testing.T) {
	if _, err := parseStoragePlanInfo([]byte(`{"currentQuota":5368709120}`)); err == nil {
		t.Fatal("missing plans accepted")
	}
}

func TestStorefrontOfferParsingUsesStorefrontCurrencyAndMinorPrice(t *testing.T) {
	body := []byte(`<form><input type="hidden" value="/WebObjects/MZFinance.woa/wa/buyProduct?storagePlanInBytes=53687091200&amp;cc=CN&amp;price=6000&amp;offerName=com.apple.icloud50GB_monthly" /><input type="hidden" value="/WebObjects/MZFinance.woa/wa/buyProduct?storagePlanInBytes=214748364800&amp;cc=CN&amp;price=21000&amp;offerName=com.apple.icloud200GB_monthly" /></form>`)
	// The HTML extraction is shared with the purchase flow; this assertion
	// protects the CN -> CNY and Store price -> fen conversion at its boundary.
	u, err := exactOfferURL(body, PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"})
	if err != nil || u == "" {
		t.Fatalf("exact storefront offer was not recognized: %q, %v", u, err)
	}
}

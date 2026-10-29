package icloud

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestConfirmedStoragePurchaseOnlyConsumesExactAppleConfirmation(t *testing.T) {
	for _, scenario := range []string{"valid", "changed-price", "unknown-dialog"} {
		calls := 0
		original := DirectStorageOfferURL("TEST-GUID")
		client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
			calls++
			var response []byte
			switch calls {
			case 1:
				response = []byte(`<form><input type="hidden" value="` + strings.ReplaceAll(original, "&", "&amp;") + `"></form>`)
			case 2:
				u, _ := url.Parse(original)
				q := u.Query()
				q.Set("hasConfirmAutoRenew", "true")
				id := "SRVOrderManager.ConfirmBuyingAutoRenewingSubscription"
				if scenario == "changed-price" {
					q.Set("price", "7000")
				}
				if scenario == "unknown-dialog" {
					id = "AnotherPurchaseDialog"
				}
				response, _ = plist.Marshal(map[string]any{"dialog": map[string]any{"dialogId": id, "okButtonAction": map[string]any{"kind": "Buy", "buyParams": q.Encode()}}}, plist.XMLFormat)
			case 3:
				data, _ := io.ReadAll(req.Body)
				var body map[string]any
				plist.Unmarshal(data, &body)
				if body["hasConfirmAutoRenew"] != "true" || body["price"] != "6000" {
					t.Fatal("server confirmation or original price was not preserved")
				}
				response = []byte(`<plist><dict><key>jingleDocType</key><string>purchaseSuccess</string><key>status</key><integer>0</integer></dict></plist>`)
			default:
				t.Fatal("purchase was replayed")
			}
			return &http.Response{StatusCode: 200, Header: http.Header{}, Request: req, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
		})}
		s := &StoreCommerceSession{client: client, identity: &storeSession{DSID: "123", PasswordToken: "store-token", GUID: "TEST-GUID"}}
		target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}
		info := &StoragePlanInfo{Plans: []StoragePlan{{Bytes: target.Bytes, Price: 600, Currency: "CNY", Period: "month", Available: true}}}
		result, err := s.SubmitConfirmedStoragePurchase(context.Background(), info, "owner@example.test", "test-password", "123", "account", "mme-token", "storage-token", nil, target)
		switch scenario {
		case "valid":
			if err != nil || calls != 3 || result.State != "completed" {
				t.Fatalf("valid confirmation not consumed: %v", err)
			}
		case "changed-price":
			if err == nil || calls != 2 {
				t.Fatal("changed-price confirmation was submitted")
			}
		case "unknown-dialog":
			if err != nil || calls != 2 || result.State != "confirmation-required" {
				t.Fatal("unknown dialog was confirmed")
			}
		}
	}
}

func TestNativeStoragePurchaseCarriesExactOfferAndRejectsPriceChanges(t *testing.T) {
	called := 0
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		called++
		if req.Header.Get("X-Dsid") != "123" || req.Header.Get("X-Token") != "store-token" {
			t.Fatal("account session changed")
		}
		var body map[string]any
		data, _ := io.ReadAll(req.Body)
		if _, err := plist.Unmarshal(data, &body); err != nil {
			t.Fatal(err)
		}
		for key, value := range map[string]string{"cc": "CN", "price": "6000", "offerName": "com.apple.icloud50GB_monthly", "storagePlanInBytes": "53687091200", "guid": "TEST-GUID"} {
			if body[key] != value {
				t.Fatalf("missing exact product field %s", key)
			}
		}
		if _, ok := body["Request"]; ok {
			t.Fatal("unexpected request envelope")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Request: req, Body: io.NopCloser(strings.NewReader(`<plist><dict><key>jingleDocType</key><string>purchaseSuccess</string><key>status</key><integer>0</integer></dict></plist>`))}, nil
	})}
	session := &storeSession{DSID: "123", PasswordToken: "store-token", GUID: "TEST-GUID"}
	result, err := submitStoreBuyProduct(context.Background(), client, DirectStorageOfferURL(session.GUID), nil, "account", session)
	if err != nil || result == nil || result.State != "completed" || called != 1 {
		t.Fatalf("valid purchase not accepted: %v", err)
	}
	u, _ := url.Parse(DirectStorageOfferURL(session.GUID))
	q := u.Query()
	q.Set("price", "7000")
	u.RawQuery = q.Encode()
	if _, err := submitStoreBuyProduct(context.Background(), client, u.String(), nil, "account", session); err == nil || called != 1 {
		t.Fatal("changed price submitted")
	}
}

func TestNativeStorageConfirmationRejectsChangedOfferOrAccount(t *testing.T) {
	original, _ := url.Parse(DirectStorageOfferURL("TEST-GUID"))
	q := original.Query()
	q.Set("hasConfirmAutoRenew", "true")
	session := &storeSession{DSID: "123", PasswordToken: "store-token", GUID: "TEST-GUID"}
	if err := validateNativeStorageConfirmation(q, original.Query(), session); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"price": "7000", "cc": "US", "offerName": "another-product", "storagePlanInBytes": "214748364800", "salableAdamId": "999", "guid": "OTHER-GUID", "xToken": "other-token", "prsId": "456", "hasConfirmAutoRenew": "false"} {
		changed := url.Values{}
		for k, v := range q {
			changed[k] = append([]string(nil), v...)
		}
		changed.Set(key, value)
		if err := validateNativeStorageConfirmation(changed, original.Query(), session); err == nil {
			t.Fatalf("accepted changed confirmation %s", key)
		}
	}
	q.Add("price", "6000")
	if err := validateNativeStorageConfirmation(q, original.Query(), session); err == nil {
		t.Fatal("duplicate parameter accepted")
	}
}

func TestNativeUSStorageConfirmationBinding(t *testing.T) {
	target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 99, Currency: "USD", Period: "month"}
	raw, err := DirectStorageOfferURLForTarget("TEST-GUID", target)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := url.Parse(raw)
	confirmation := original.Query()
	confirmation.Set("hasConfirmAutoRenew", "true")
	session := &storeSession{DSID: "123", PasswordToken: "store-token", GUID: "TEST-GUID"}
	if err := validateNativeStorageConfirmation(confirmation, original.Query(), session); err != nil {
		t.Fatal(err)
	}
	confirmation.Set("price", "999")
	if err := validateNativeStorageConfirmation(confirmation, original.Query(), session); err == nil {
		t.Fatal("changed US price was accepted")
	}
}

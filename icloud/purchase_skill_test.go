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

func TestSkillPurchasePreservesFullConfirmationAndVerifiesSubscription(t *testing.T) {
	buys := 0
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		var response []byte
		if req.Method == http.MethodPost {
			buys++
			if req.URL.Path != "/WebObjects/MZFinance.woa/wa/buyProduct" || req.Header.Get("User-Agent") != skillStoreAgent || req.Header.Get("X-Dsid") != "123" {
				t.Fatal("wrong native purchase request")
			}
			data, _ := io.ReadAll(req.Body)
			var body map[string]any
			if _, err := plist.Unmarshal(data, &body); err != nil {
				t.Fatal(err)
			}
			if body["storagePlanInBytes"] != uint64(53687091200) || body["appAdamId"] != "1581031638" {
				t.Fatal("skill offer fields or plist types changed")
			}
			if buys == 1 {
				u, _ := url.Parse(DirectStorageOfferURL("TEST-GUID"))
				q := u.Query()
				q.Set("hasConfirmAutoRenew", "true")
				q.Set("opaqueAppleField", "preserve-me")
				response, _ = plist.Marshal(map[string]any{"dialog": map[string]any{"dialogId": "SRVOrderManager.ConfirmBuyingAutoRenewingSubscription", "okButtonAction": map[string]any{"kind": "Buy", "buyParams": q.Encode()}}}, plist.XMLFormat)
			} else if buys == 2 {
				if body["opaqueAppleField"] != "preserve-me" || body["hasShownExtraConfirmationNeededForBuyRecurringItem"] != "true" {
					t.Fatal("Apple confirmation was reconstructed or incomplete")
				}
				response, _ = plist.Marshal(map[string]any{"jingleDocType": "purchaseSuccess", "jingleAction": "purchaseProduct", "status": 0}, plist.XMLFormat)
			} else {
				t.Fatal("purchase replayed")
			}
		} else if strings.HasSuffix(req.URL.Path, "/list") {
			response = []byte(`{"active":[{"subscriptionId":"test-subscription","displayName":"iCloud+"}]}`)
		} else {
			if req.URL.Query().Get("subscriptionId") != "test-subscription" {
				t.Fatal("wrong subscription selected")
			}
			response = []byte(`{"specialSubInfo":{"storageAmount":"50\u00a0GB","storageUsage":"0 KB"},"isAutoRenewEnabled":true,"latestPlan":{"displayName":"iCloud+ with 50 GB of storage","paidPrice":"\u00a56.00"}}`)
		}
		return &http.Response{StatusCode: 200, Request: req, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(string(response)))}, nil
	})}
	s := &StoreCommerceSession{client: client, identity: &storeSession{DSID: "123", PasswordToken: "test-token", GUID: "TEST-GUID", StoreFront: "143465"}}
	target := PurchaseTarget{Bytes: 53687091200, PriceMinor: 600, Currency: "CNY", Period: "month"}
	info := &StoragePlanInfo{Plans: []StoragePlan{{Bytes: target.Bytes, Price: 600, Currency: "CNY", Period: "month", Available: true}}}
	r, err := s.SubmitSkillPurchase(context.Background(), info, target, "")
	if err != nil || r.State != "completed" || buys != 2 {
		t.Fatalf("native purchase failed: %v", err)
	}
	sub, err := s.ReadSkillSubscription(context.Background())
	if err != nil || !sub.Verified50GB || !sub.AutoRenew {
		t.Fatalf("actual paid storage not verified: %v", err)
	}
}

func TestSkillAuthenticationAppendsCodeOnlyAfterAppleChallenge(t *testing.T) {
	calls, codes := 0, 0
	client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		data, _ := io.ReadAll(req.Body)
		var body map[string]any
		plist.Unmarshal(data, &body)
		want := "test-password"
		if calls == 2 {
			want += "123456"
		}
		if body["password"] != want {
			t.Fatal("code submitted before the challenge or omitted afterward")
		}
		response := `<plist><dict><key>customerMessage</key><string>MZFinance.BadLogin.Configurator_message</string></dict></plist>`
		h := http.Header{}
		if calls == 2 {
			response = `<plist><dict><key>dsPersonId</key><string>123</string><key>passwordToken</key><string>test-token</string></dict></plist>`
			h.Set("X-Set-Apple-Store-Front", "143465")
		}
		return &http.Response{StatusCode: 200, Header: h, Request: req, Body: io.NopCloser(strings.NewReader(response))}, nil
	})}
	s, err := authenticateSkillStoreClient(context.Background(), client, "TEST-GUID", "owner@example.test", "test-password", func() (string, error) { codes++; return "123456", nil })
	if err != nil || !s.MatchesAccount("123") || calls != 2 || codes != 1 {
		t.Fatalf("incorrect challenge continuation: %v", err)
	}
}

package icloud

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestExactOfferURLRequiresAllTargetFields(t *testing.T) {
	body := []byte(`<form><input type="hidden" value="/WebObjects/MZFinance.woa/wa/buyProduct?storagePlanInBytes=53687091200&amp;cc=CN&amp;price=6000&amp;offerName=com.apple.icloud50GB_monthly" /></form>`)
	got, err := exactOfferURL(body, PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"})
	if err != nil || got == "" {
		t.Fatalf("expected exact offer URL, got %q, %v", got, err)
	}
	bad := []byte(`<form><input type="hidden" value="/WebObjects/MZFinance.woa/wa/buyProduct?storagePlanInBytes=53687091200&amp;cc=CN&amp;price=7000&amp;offerName=com.apple.icloud50GB_monthly" /></form>`)
	if _, err := exactOfferURL(bad, PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}); err == nil {
		t.Fatal("wrong price accepted")
	}
}

func TestStoreContinuationRequiresExactPriceAndSameAccount(t *testing.T) {
	q := url.Values{"offerName": {"com.apple.icloud50GB_monthly"}, "storagePlanInBytes": {"53687091200"}, "price": {"6000"}, "cc": {"CN"}, "xToken": {"store-token"}, "prsId": {"123"}, "guid": {"ABCDEF012345"}, "appleId": {"person@example.test"}, "authToken": {"icloud-token"}}
	form := url.Values{"appleId": {"person@example.test"}, "authToken": {"icloud-token"}, "guid": {"ABCDEF012345"}}
	session := &storeSession{DSID: "123", PasswordToken: "store-token", GUID: "MAC000000001"}
	if err := validateStoreContinuation(storeBuyProductURL+"?"+q.Encode(), form, session); err != nil {
		t.Fatal(err)
	}
	for key, value := range map[string]string{"price": "7000", "prsId": "456", "xToken": "other-token", "authToken": "other-token", "appleId": "other@example.test", "guid": "other-guid"} {
		bad := url.Values{}
		for k, v := range q {
			bad[k] = append([]string(nil), v...)
		}
		bad.Set(key, value)
		if err := validateStoreContinuation(storeBuyProductURL+"?"+bad.Encode(), form, session); err == nil {
			t.Fatalf("accepted wrong continuation %s", key)
		}
	}
	if err := validateStoreContinuation("https://example.test/WebObjects/MZFinance.woa/wa/buyProduct?"+q.Encode(), form, session); err == nil {
		t.Fatal("accepted external continuation")
	}
}

func TestStoreFormBindingsCheckOfferAndBothAppleIDsBeforePosting(t *testing.T) {
	target := PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}
	session := &storeSession{DSID: "123", PasswordToken: "store-token", GUID: "SAP-GUID"}
	form := url.Values{
		"appleId":            {"person@example.test", "person@example.test"},
		"prsId":              {"123"},
		"guid":               {"123"}, // MZFinance issued value, distinct from SAP GUID.
		"xToken":             {"store-token"},
		"authToken":          {"icloud-token"},
		"offerName":          {"com.apple.icloud50GB_monthly"},
		"storagePlanInBytes": {"53687091200"},
		"price":              {"6000"},
		"cc":                 {"CN"},
	}
	continuation := url.Values{}
	for key, values := range form {
		continuation[key] = append([]string(nil), values[:1]...)
	}
	form.Set("okURL", storeBuyProductURL+"?"+continuation.Encode())
	if err := validateStoreFormBindings(form, "person@example.test", "123", "icloud-token", target, session); err != nil {
		t.Fatal(err)
	}
	form["appleId"][1] = "other@example.test"
	if err := validateStoreFormBindings(form, "person@example.test", "123", "icloud-token", target, session); err == nil {
		t.Fatal("second Apple ID mismatch was accepted")
	}
	form["appleId"][1] = "person@example.test"
	form.Set("price", "7000")
	if err := validateStoreFormBindings(form, "person@example.test", "123", "icloud-token", target, session); err == nil {
		t.Fatal("wrong form price was accepted")
	}
}

func TestStoreContinuationUsesIssuedURLWithoutRewritingFlags(t *testing.T) {
	endpoint := storeBuyProductURL + "?hasBeenAuthedForBuy=true&test=original"
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		if req.Method != "GET" || req.URL.String() != endpoint || req.Header.Get("X-Token") != "store-token" {
			t.Fatal("issued continuation or verified Store session changed")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`<body class="buy-upgrade-success">Completed</body>`)), Request: req}, nil
	})}
	result, err := followStorePurchase(context.Background(), client, endpoint, storeBuyProductURL, nil, "account", "capability", &storeSession{DSID: "123", PasswordToken: "store-token"})
	if err != nil || result.State != "completed" {
		t.Fatalf("continuation result: %v", err)
	}
}

func TestVolumeRootUsesServerResponse(t *testing.T) {
	for _, status := range []int{200, 404} {
		called := false
		client := &http.Client{Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
			called = true
			if req.URL.String() != "https://volume.itunes.apple.com/" || req.Header.Get("X-Token") != "" {
				t.Fatal("incorrect navigation or native Store token sent to volume host")
			}
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`<body class="buy-upgrade-success">Completed</body>`)), Request: req}, nil
		})}
		result, err := followStorePurchase(context.Background(), client, "https://volume.itunes.apple.com/", "", nil, "", "", &storeSession{})
		if !called {
			t.Fatal("volume navigation skipped")
		}
		if status == 200 && (err != nil || result.State != "completed") {
			t.Fatalf("completion not recognized: %v", err)
		}
		if status == 404 && (err == nil || result != nil) {
			t.Fatal("404 was reported as a handoff or completion")
		}
	}
}

func TestPurchaseSuccessCannotComeFromStyleOrScript(t *testing.T) {
	for _, body := range []string{
		`<style>body.buy-upgrade-success { color: green; }</style><body class="login">Sign in</body>`,
		`<body><script>var subscriptionActive = false; document.querySelector('.buy-upgrade-success')</script></body>`,
	} {
		if storePurchaseComplete([]byte(body)) {
			t.Fatal("script or style text was mistaken for a completed purchase")
		}
	}
	if !storePurchaseComplete([]byte(`<body class="finance buy-upgrade-success">Purchased</body>`)) {
		t.Fatal("success document not recognized")
	}
}

func TestStoreFormURLRespectsDocumentBaseAndRejectsNonStoreHosts(t *testing.T) {
	document, _ := url.Parse(storeBuyProductURL + "?private=test")
	got, err := storeFormURL(document, []byte(`<base href="https://p12-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/">`), "volumeAuthenticate")
	if err != nil || got.String() != "https://p12-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/volumeAuthenticate" {
		t.Fatalf("resolved URL incorrect: %v", err)
	}
	if _, err := storeFormURL(document, []byte(`<base href="https://example.test/">`), "volumeAuthenticate"); err == nil {
		t.Fatal("external form destination accepted")
	}
}

func TestParseStoreFormsKeepsHiddenFieldsAndSubmit(t *testing.T) {
	forms := parseStoreForms([]byte(`<form method="post" action="/WebObjects/MZFinance.woa/wa/volumeAuthenticate"><input type="hidden" name="offerName" value="com.apple.icloud50GB_monthly"><input name="password" type="password"><input name="buy" type="submit" value="Buy"></form>`))
	if len(forms) != 1 || forms[0].method != "POST" || len(forms[0].fields) != 3 {
		t.Fatalf("unexpected form parse: %+v", forms)
	}
}

func TestSuccessfulStoreFormValuesOnlySelectsPurchaseControl(t *testing.T) {
	forms := parseStoreForms([]byte(`<form action="/WebObjects/MZFinance.woa/wa/volumeAuthenticate" method="post">
        <input type="hidden" name="offerName" value="com.apple.icloud50GB_monthly">
        <input type="text" name="appleId" value="owner@example.test">
        <input type="password" name="password" value="">
        <input type="submit" name="buy" value="Buy">
        <input type="submit" name="cancel" value="Cancel">
    </form>`))
	if len(forms) != 1 {
		t.Fatalf("forms=%d", len(forms))
	}
	got := successfulStoreFormValues(forms[0])
	if got.Get("offerName") != "com.apple.icloud50GB_monthly" || got.Get("appleId") != "owner@example.test" {
		t.Fatalf("ordinary controls missing: %#v", got)
	}
	if got.Get("buy") != "Buy" || got.Get("cancel") != "" {
		t.Fatalf("submit controls were not filtered: %#v", got)
	}
	if got.Get("password") != "" {
		t.Fatalf("password must be filled by the caller")
	}
	encoded := encodeStoreForm(forms[0], "secret password")
	if encoded != "offerName=com.apple.icloud50GB_monthly&appleId=owner%40example.test&password=secret+password&buy=Buy" {
		t.Fatal("form controls were not encoded in document order")
	}
}

func TestSetStorageBuyTokenUsesAccountBoundHeader(t *testing.T) {
	req, _ := http.NewRequest(http.MethodGet, "https://buy.itunes.apple.com", nil)
	setStorageBuyToken(req, "adsid", "storage-token")
	want := base64.StdEncoding.EncodeToString([]byte("adsid:storage-token"))
	if req.Header.Get("X-Apple-GS-Token") != want {
		t.Fatalf("unexpected service token header")
	}
}

func TestValidatePurchaseTarget(t *testing.T) {
	info := &StoragePlanInfo{Plans: []StoragePlan{{Bytes: 50 * 1024 * 1024 * 1024, Price: 600, Currency: "CNY", Period: "month", Available: true}}}
	if err := ValidatePurchaseTarget(info, PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}); err != nil {
		t.Fatal(err)
	}
	if _, err := SubmitStoragePurchase(context.Background(), info, "", "", "", "", "", nil, PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}); err == nil {
		t.Fatal("missing purchase credentials accepted")
	}
}

func TestValidatePurchaseTargetDoesNotGuess(t *testing.T) {
	info := &StoragePlanInfo{Plans: []StoragePlan{{Bytes: 50 * 1024 * 1024 * 1024, Price: 600, Currency: "USD", Period: "month", Available: true}}}
	if err := ValidatePurchaseTarget(info, PurchaseTarget{Bytes: 50 * 1024 * 1024 * 1024, PriceMinor: 600, Currency: "CNY", Period: "month"}); err == nil {
		t.Fatal("currency mismatch accepted")
	}
}

func TestFormNavigationMatchesOfficialBrowserShape(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, storeAuthenticateURL, nil)
	setStoreFormHeaders(req, storeBuyProductURL)
	if req.Header.Get("User-Agent") != storeWebViewUA {
		t.Fatalf("form post must present the WebView user agent, got %q", req.Header.Get("User-Agent"))
	}
	if req.Header.Get("Upgrade-Insecure-Requests") != "1" {
		t.Fatal("form navigation must upgrade insecure requests")
	}
	if req.Header.Get("Origin") != "https://buy.itunes.apple.com" {
		t.Fatalf("form post must carry the store origin, got %q", req.Header.Get("Origin"))
	}
	// The captured official request carries none of these.
	for _, absent := range []string{"X-Token", "X-Dsid", "X-Apple-Store-Front", "X-Apple-ADSID",
		"Authorization", "X-Mme-Client-Info", "X-MMe-Client-Info"} {
		if req.Header.Get(absent) != "" {
			t.Fatalf("header %s is not part of the official form navigation", absent)
		}
	}
}

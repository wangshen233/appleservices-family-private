package accountsetup

import (
	"encoding/base64"
	"io"
	"net/url"
	"strings"
	"testing"
)

func testPage(owner string) []byte {
	return []byte(`<script>var c={updateMode:true,deviceQualified:true,internalServerError:false,appleId:"` + owner + `",authHeader:"Basic ` + base64.StdEncoding.EncodeToString([]byte(owner+":test-pet")) + `",clientInfo:"<test-device> <test-os> <test-client>",nasQualify:""};</script><input type="hidden" id="tosversion_field" value="123"><input type="hidden" id="amstosversion_field" value="456"><input type="hidden" id="countryCode_field" value="JP"><input name="firstName" value="private"><input name="birthday" value="private">`)
}

func TestTermsUpdateNeedsConsentAndPreservesScope(t *testing.T) {
	p, err := PrepareTerms(testPage("owner@example.test"), "owner@example.test", strings.Repeat("a", 64), make([]byte, 32), "Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Request(false); err == nil {
		t.Fatal("terms agreement prepared without consent")
	}
	r, err := p.Request(true)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	values, err := url.ParseQuery(string(data))
	if err != nil || values.Get("tos-version") != "123" || values.Get("ams-tos-version") != "456" || values.Has("emailUpdates") {
		t.Fatal("wrong agreement or marketing subscription")
	}
	if r.URL.Host != "setup.icloud.com" || !strings.HasPrefix(r.URL.Path, "/setup/web/basic_auth_update_account/") || r.Header.Get("X-Mme-Nas-Qualify") != "" {
		t.Fatal("unexpected endpoint or invented NAS data")
	}
	if values.Has("firstName") || values.Has("birthday") || values.Has("country") || values.Has("password") {
		t.Fatal("terms update changes unrelated account fields")
	}
}

func TestTermsUpdateRejectsDifferentAccountAndCreationPage(t *testing.T) {
	for _, page := range [][]byte{testPage("other@example.test"), []byte(strings.ReplaceAll(string(testPage("owner@example.test")), "updateMode:true", "updateMode:false"))} {
		if _, err := PrepareTerms(page, "owner@example.test", strings.Repeat("a", 64), make([]byte, 32), "Asia/Singapore"); err == nil {
			t.Fatal("unbound or account creation page accepted")
		}
	}
}

func TestTermsUpdateAllowsICloudOnlyVersion(t *testing.T) {
	page := []byte(strings.Replace(string(testPage("owner@example.test")), `id="amstosversion_field" value="456"`, `id="amstosversion_field" value=""`, 1))
	p, err := PrepareTerms(page, "owner@example.test", strings.Repeat("a", 64), make([]byte, 32), "Asia/Singapore")
	if err != nil {
		t.Fatal(err)
	}
	if p.ICloudVersion != "123" || p.MediaVersion != "" {
		t.Fatalf("wrong terms versions: iCloud=%q media=%q", p.ICloudVersion, p.MediaVersion)
	}
	r, err := p.Request(true)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(r.Body)
	values, err := url.ParseQuery(string(data))
	if err != nil || values.Get("tos-version") != "123" || values.Get("ams-tos-version") != "" {
		t.Fatal("iCloud-only agreement was not preserved")
	}
}

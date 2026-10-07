package icloud

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestPlistLoginBodyDoesNotUseFormEncoding(t *testing.T) {
	body, err := plistLoginBody("person@example.test", "secret", "001122AABBCC")
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if _, err := plist.Unmarshal(body, &values); err != nil {
		t.Fatal(err)
	}
	if values["appleId"] != "person@example.test" || values["guid"] != "001122AABBCC" {
		t.Fatalf("unexpected plist fields: %#v", values)
	}
	if strings.Contains(string(body), "&") {
		t.Fatal("login body was form encoded")
	}
}

func TestDecodeStoreLoginRejectsHTTP200WithoutSession(t *testing.T) {
	for _, body := range []string{
		`<html><body>Sign in</body></html>`,
		`<plist><dict/></plist>`,
		`<plist><dict><key>dsPersonId</key><string>123</string></dict></plist>`,
	} {
		if session, err := decodeStoreLogin([]byte(body), 200, http.Header{}); session != nil || err == nil {
			t.Fatal("HTTP 200 without Store credentials was accepted")
		}
	}
}

func TestDecodeStoreLoginHandlesApple2FAAndWrappedSuccess(t *testing.T) {
	_, err := decodeStoreLogin([]byte(`<plist><dict><key>customerMessage</key><string>MZFinance.BadLogin.Configurator_message</string></dict></plist>`), 200, http.Header{})
	var failure *StorageError
	if !errors.As(err, &failure) || failure.Stage != "purchase-store-2fa" {
		t.Fatal("2FA response not recognized")
	}
	headers := http.Header{}
	headers.Set("X-Set-Apple-Store-Front", "143465-19,32")
	session, err := decodeStoreLogin([]byte(`<Document><plist><dict><key>dsPersonId</key><string>123</string><key>passwordToken</key><string>store-token</string></dict></plist></Document>`), 200, headers)
	if err != nil || session.DSID != "123" || session.PasswordToken != "store-token" {
		t.Fatal("wrapped authentication response not recognized")
	}
	req, _ := http.NewRequest("GET", storePresentOffersURL, nil)
	session.setHeaders(req)
	if req.Header.Get("X-Dsid") != "123" || req.Header.Get("X-Token") != "store-token" {
		t.Fatal("Store identity was not propagated to commerce request")
	}
}

type testStoreTransport func(*http.Request) (*http.Response, error)

func (f testStoreTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type testStoreSigner struct{ body []byte }

func (s *testStoreSigner) Sign(b []byte) ([]byte, error) {
	s.body = append([]byte(nil), b...)
	return []byte("signed"), nil
}
func (*testStoreSigner) Close() error { return nil }

func TestSignedStoreAuthenticationRetriesCredentialProbeAndReplaysPOST(t *testing.T) {
	var calls int
	signer := &testStoreSigner{}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }, Transport: testStoreTransport(func(req *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(req.Body)
		if req.Method != "POST" || req.Header.Get("X-Apple-ActionSignature") != base64.StdEncoding.EncodeToString([]byte("signed")) || string(body) != string(signer.body) {
			t.Fatal("signed POST body not preserved")
		}
		var payload map[string]any
		if _, err := plist.Unmarshal(body, &payload); err != nil {
			t.Fatal(err)
		}
		header := http.Header{}
		status, response := 200, `<plist><dict><key>dsPersonId</key><string>123</string><key>passwordToken</key><string>store-token</string></dict></plist>`
		switch calls {
		case 1:
			header.Set("Location", "https://p12-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/authenticate/")
			status, response = 302, ""
		case 2:
			if payload["attempt"] != "1" {
				t.Fatal("redirect changed attempt")
			}
			response = `<plist><dict><key>failureType</key><string>-5000</string></dict></plist>`
		case 3:
			if payload["attempt"] != "2" {
				t.Fatal("credential probe not retried")
			}
			header.Set("X-Set-Apple-Store-Front", "143465-19,32")
		default:
			t.Fatal("unexpected extra authentication request")
		}
		return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(response)), Request: req}, nil
	})}
	session, err := authenticateSignedStore(context.Background(), client, storeAuthenticateURL, "person@example.test", "secret", "", "001122AABBCC", nil, signer)
	if err != nil || session == nil || calls != 3 {
		t.Fatalf("requests=%d, err=%v", calls, err)
	}
}

func TestValidStoreRedirectKeepsAppleHost(t *testing.T) {
	got, err := validStoreRedirect("https://auth.itunes.apple.com/auth/v1/native/fast", "/WebObjects/MZFinance.woa/wa/authenticate")
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://auth.itunes.apple.com/WebObjects/MZFinance.woa/wa/authenticate" {
		t.Fatalf("unexpected redirect: %s", got)
	}
	if _, err := validStoreRedirect("https://auth.itunes.apple.com/auth/v1/native/fast", "https://example.test/steal"); err == nil {
		t.Fatal("accepted redirect outside Apple")
	}
}

func TestLoadStoreSAPBagUsesVersionedKeys(t *testing.T) {
	bag, err := decodeStoreSAPBag([]byte(`<?xml version="1.0"?><plist version="1.0"><dict>
		<key>authenticateAccount</key><string>https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/authenticate</string>
		<key>loc-sign-sap-setup</key><string>https://fpinit.itunes.apple.com/v1/signSapSetup/legacy</string>
		<key>loc-sign-sap-setup-cert</key><string>https://s.mzstatic.com/sap/setupCert.plist</string>
		<key>loc-sign-sap-version</key><string>200</string>
	</dict></plist>`))
	if err != nil {
		t.Fatal(err)
	}
	if bag.AuthEndpoint == "" || bag.SetupURL == "" || bag.CertificateURL == "" || bag.Version != 200 {
		t.Fatalf("incomplete SAP bag: %#v", bag)
	}
}

func TestStoreHardwareIdentitySkipsVirtualAdapters(t *testing.T) {
	cases := []struct {
		name string
		mac  []byte
		want bool
	}{
		{"vEthernet (WSL (Hyper-V firewall))", []byte{0x00, 0x15, 0x5d, 0xa5, 0x1c, 0x34}, true},
		{"VMware Network Adapter VMnet8", []byte{0x00, 0x50, 0x56, 0xc0, 0x00, 0x08}, true},
		{"Ethernet 2", []byte{0x08, 0x00, 0x27, 0x01, 0x02, 0x03}, true},
		{"WLAN", []byte{0x28, 0xa4, 0x4a, 0xba, 0xc4, 0x26}, false},
		{"Ethernet", []byte{0x00, 0x1a, 0x2b, 0x3c, 0x4d, 0x5e}, false},
	}
	for _, c := range cases {
		if got := isVirtualInterface(c.name, c.mac); got != c.want {
			t.Fatalf("isVirtualInterface(%q, %x) = %v, want %v", c.name, c.mac, got, c.want)
		}
	}
}

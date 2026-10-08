package gsa

import (
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestCurrentDeviceDetailsAreReadOnlyAndBoundToAppleLink(t *testing.T) {
	c := NewClient(deviceTestAnisette{})
	calls := 0
	c.transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Host != "gsa.apple.com" || r.URL.Path != "/appleid/account/manage/security/devices/test-device" {
			t.Fatal("device details used a write operation or different target")
		}
		if r.Header.Get("X-Apple-Identity-Token") != base64.StdEncoding.EncodeToString([]byte("test-account:test-identity")) {
			t.Fatal("wrong account identity binding")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"trustedDevice":true,"serialNumber":"TEST01234567"}`))}, nil
	})
	device := AccountDevice{ID: "test-device", CurrentDevice: true, DeviceDetailHTTPMethod: http.MethodGet, DeviceDetailURI: "https://gsa.apple.com/appleid/account/manage/security/devices/test-device"}
	payload := map[string]any{"adsid": "test-account", "GsIdmsToken": "test-identity"}
	for _, invalid := range []AccountDevice{
		{ID: device.ID, CurrentDevice: false, DeviceDetailHTTPMethod: device.DeviceDetailHTTPMethod, DeviceDetailURI: device.DeviceDetailURI},
		{ID: device.ID, CurrentDevice: true, DeviceDetailHTTPMethod: http.MethodPost, DeviceDetailURI: device.DeviceDetailURI},
		{ID: device.ID, CurrentDevice: true, DeviceDetailHTTPMethod: http.MethodGet, DeviceDetailURI: "https://example.test/appleid/account/manage/security/devices/test-device"},
		{ID: device.ID, CurrentDevice: true, DeviceDetailHTTPMethod: http.MethodGet, DeviceDetailURI: device.DeviceDetailURI + "?action=remove"},
		{ID: "different-device", CurrentDevice: true, DeviceDetailHTTPMethod: http.MethodGet, DeviceDetailURI: device.DeviceDetailURI},
	} {
		if _, err := c.AccountDeviceDetails(payload, invalid); err == nil || calls != 0 {
			t.Fatal("followed an unbound or mutable device link")
		}
	}
	details, err := c.AccountDeviceDetails(payload, device)
	if err != nil || calls != 1 || details["trustedDevice"] != true || details["serialNumber"] != "TEST01234567" {
		t.Fatal("failed to preserve Apple's explicit device trust state")
	}
}

func TestRegistrationRequiresRealLengthTokenAndChecksServerFailure(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Apple-HB-Token") != base64.StdEncoding.EncodeToString([]byte("account:heartbeat")) {
			t.Error("wrong heartbeat authentication")
		}
		var packet map[string]any
		body, _ := io.ReadAll(r.Body)
		if _, err := plist.Unmarshal(body, &packet); err != nil {
			t.Error(err)
		}
		request, _ := packet["Request"].(map[string]any)
		if request["dn"] != "test-software" || request["ptkn"] == nil {
			t.Error("missing registration identity or APS token")
		}
		if _, ok := request["denyICloudWebAccess"]; ok {
			t.Error("registration changes unrelated cloud access")
		}
		w.WriteHeader(401)
		data, _ := plist.Marshal(map[string]any{"Response": map[string]any{"Status": map[string]any{"ec": -800012, "em": "Push token is invalid."}}}, plist.XMLFormat)
		w.Write(data)
	}))
	defer server.Close()
	original := deviceServiceURL
	deviceServiceURL = server.URL
	defer func() { deviceServiceURL = original }()
	client := NewClient(deviceTestAnisette{})
	payload := map[string]any{"adsid": "account", "t": map[string]any{"com.apple.gs.idms.hb": map[string]any{"token": "heartbeat"}}}
	if err := client.RegisterDevice(payload, DeviceRegistration{Name: "test-software"}); err == nil || calls != 0 {
		t.Fatal("registration attempted without APS token")
	}
	err := client.RegisterDevice(payload, DeviceRegistration{Name: "test-software", PushToken: bytes.Repeat([]byte{3}, 32)})
	var failure *DeviceRequestError
	if !errors.As(err, &failure) || failure.Code != -800012 || calls != 1 {
		t.Fatalf("server refusal was not preserved: %v", err)
	}
}

type deviceTestAnisette struct{}

func (deviceTestAnisette) Headers() (map[string]string, error) { return map[string]string{}, nil }

func TestSignInApprovalUsesServerButtonAndRejectsBodyError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		var packet map[string]any
		if _, err := plist.Unmarshal(data, &packet); err != nil {
			t.Error(err)
		}
		request, _ := packet["Request"].(map[string]any)
		if request["action"] != "defbtn" || request["txnid"] != "pending-test-transaction" {
			t.Error("wrong approval transaction/button")
		}
		data, _ = plist.Marshal(map[string]any{"Response": map[string]any{"Status": map[string]any{"ec": -1}}}, plist.XMLFormat)
		w.Write(data)
	}))
	defer server.Close()
	original := deviceTeardownURL
	deviceTeardownURL = server.URL
	defer func() { deviceTeardownURL = original }()
	client := NewClient(deviceTestAnisette{})
	payload := map[string]any{"adsid": "account", "t": map[string]any{"com.apple.gs.idms.hb": map[string]any{"token": "heartbeat"}}}
	if err := client.RespondToSignIn(payload, "pending-test-transaction", true); err == nil {
		t.Fatal("accepted an approval rejected in a successful HTTP response")
	}
}

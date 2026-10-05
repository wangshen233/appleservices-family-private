package icloud

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"howett.net/plist"
)

func TestDelegateLoginUsesPETAndReturnsICloudToken(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "POST" || r.URL.Path != "/setup/iosbuddy/loginDelegates" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte("owner@example.test:test-pet")) {
			t.Error("delegate login did not use PET authentication")
		}
		if r.Header.Get("X-Apple-I-MD") != "test-md" {
			t.Error("missing anisette header")
		}
		if r.Header.Get("X-Mme-Client-Info") != "<test-model> <test-os> <com.apple.AuthKit/1 (com.apple.akd/1.0)>" {
			t.Error("iCloud delegate exchange still sends retired client token")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request map[string]any
		if _, err := plist.Unmarshal(body, &request); err != nil {
			t.Error(err)
			return
		}
		if request["password"] != "test-pet" {
			t.Error("delegate body did not use PET")
		}
		response, err := plist.Marshal(map[string]any{
			"dsid": "test-dsid",
			"delegates": map[string]any{"com.apple.mobileme": map[string]any{
				"service-data": map[string]any{"tokens": map[string]any{"mmeAuthToken": "test-mme", "cloudKitToken": "test-ck"}},
			}},
		}, plist.XMLFormat)
		if err != nil {
			t.Error(err)
			return
		}
		w.Write(response)
	}))
	defer server.Close()
	previous := setupBaseURL
	setupBaseURL = server.URL
	t.Cleanup(func() { setupBaseURL = previous })
	tokens, err := FetchDelegateTokens(map[string]string{
		"X-Apple-I-MD":      "test-md",
		"x-mme-client-info": "<test-model> <test-os> <com.apple.AuthKit/1 (com.apple.dt.Xcode/3594.4.19)>",
	}, "owner@example.test", "test-adsid", "test-pet")
	if err != nil {
		t.Fatal(err)
	}
	if tokens.DSID != "test-dsid" || tokens.MMEAuthToken != "test-mme" || tokens.CloudKitToken != "test-ck" || requests != 1 {
		t.Fatal("delegate login failed to return token result in one request")
	}
}

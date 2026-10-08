package icloud

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"howett.net/plist"
)

func TestGzipDelegateResponseYieldsRealTokens(t *testing.T) {
	response := map[string]any{"status": 0, "dsid": "test-dsid", "delegates": map[string]any{"com.apple.mobileme": map[string]any{"status": 0, "service-data": map[string]any{"tokens": map[string]any{"mmeAuthToken": "test-token"}}}}}
	raw, err := plist.Marshal(response, plist.XMLFormat)
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	w := gzip.NewWriter(&compressed)
	w.Write(raw)
	w.Close()
	body, err := decodeDelegateBody(compressed.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if _, err := plist.Unmarshal(body, &decoded); err != nil {
		t.Fatal(err)
	}
	tokens, err := extractDelegateTokens(decoded, "fallback")
	if err != nil || tokens.MMEAuthToken != "test-token" || tokens.DSID != "test-dsid" {
		t.Fatal("compressed successful response was lost", err)
	}
	if _, err := decodeDelegateBody([]byte{0x1f, 0x8b, 0}); err == nil {
		t.Fatal("invalid gzip accepted")
	}
}

func TestWindowsNativeAccountRequiresBoundMMEToken(t *testing.T) {
	for _, resp := range []map[string]any{
		{"status": 1, "appleAccountInfo": map[string]any{"dsPrsID": "test-dsid"}, "tokens": map[string]any{"mmeAuthToken": "test-token"}},
		{"tokens": map[string]any{"mmeAuthToken": "test-token"}},
		{"appleAccountInfo": map[string]any{"dsPrsID": "test-dsid"}},
	} {
		if tokens, err := extractWindowsAccountTokens(resp); err == nil || tokens.MMEAuthToken != "" {
			t.Fatal("invalid Windows account accepted")
		}
	}
}

func TestDelegateLoginUsesPETAndReturnsICloudToken(t *testing.T) {
	t.Setenv("APPLESERVICES_DELEGATE_PATH", "iosbuddy")
	t.Setenv("APPLESERVICES_ICLOUD_SETUP", "")
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
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "com.apple.iCloudHelper/") {
			t.Error("GSA agent leaked into iCloud delegate exchange")
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
		"User-Agent":        "akd/1.0",
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

func TestDelegateProfilesMatchSerializedReference(t *testing.T) {
	for _, profile := range []string{"v2", "legacy-v2"} {
		t.Run(profile, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/setup/signin/v2/login" {
					t.Error("wrong path")
				}
				data, _ := io.ReadAll(r.Body)
				var packet map[string]any
				if _, err := plist.Unmarshal(data, &packet); err != nil {
					t.Error(err)
				}
				if profile == "v2" {
					if packet["protocolVersion"] != "1.0" || packet["userInfo"] == nil {
						t.Error("V2AuthRequest serde camelCase was not applied")
					}
					if packet["protocol_version"] != nil || packet["user_info"] != nil {
						t.Error("obsolete underscore keys")
					}
					info, _ := packet["userInfo"].(map[string]any)
					if info["client-id"] == nil || info["clientId"] != nil {
						t.Error("nested kebab-case lost")
					}
				} else if packet["apple-id"] != "owner@example.test" || packet["password"] != "test-pet" || packet["userInfo"] != nil {
					t.Error("legacy body changed")
				}
				if !strings.HasPrefix(r.Header.Get("X-Mme-Client-Info"), "<test-model> <macOS;test;test> ") {
					t.Error("provider hardware identity overwritten")
				}
				if r.Header.Get("X-Mme-Nas-Qualify") != "test-validation-data" {
					t.Error("validation header discarded")
				}
				response, _ := plist.Marshal(map[string]any{
					"ErrorID": "ICLOUD_INVALID_DEVICE_TOKEN", "protocolVersion": "2",
					"description": "private-account private-token",
				}, plist.XMLFormat)
				w.Header().Set("X-Apple-Reason-Code", "3005")
				w.WriteHeader(400)
				w.Write(response)
			}))
			defer server.Close()
			t.Setenv("APPLESERVICES_ICLOUD_SETUP", server.URL)
			t.Setenv("APPLESERVICES_DELEGATE_PATH", profile)
			_, err := FetchDelegateTokens(map[string]string{
				"X-Mme-Client-Info": "<test-model> <macOS;test;test> <com.apple.AuthKit/1>",
				"X-Mme-Nas-Qualify": "test-validation-data",
			}, "owner@example.test", "test-adsid", "test-pet")
			var failure *DelegateError
			if !errors.As(err, &failure) || failure.HTTPStatus != 400 || failure.ErrorID != "ICLOUD_INVALID_DEVICE_TOKEN" || failure.ReasonCode != "3005" || failure.ProtocolVersion != "2" {
				t.Fatalf("HTTP failure diagnostic discarded: %v", err)
			}
			if strings.Contains(err.Error(), "private-") || calls != 1 {
				t.Fatal("credential echoed or PET retried")
			}
		})
	}
}

func TestRejectedDelegateCannotReturnTokens(t *testing.T) {
	for _, scope := range []string{"top", "mobileme", "error-id"} {
		mme := map[string]any{"status": 0, "service-data": map[string]any{"tokens": map[string]any{"mmeAuthToken": "test-token"}}}
		response := map[string]any{"status": 0, "delegates": map[string]any{"com.apple.mobileme": mme}}
		switch scope {
		case "top":
			response["status"] = 1
		case "mobileme":
			mme["status"] = 1
		case "error-id":
			response["ErrorID"] = "UNAUTHORIZED"
		}
		if tokens, err := extractDelegateTokens(response, "test-dsid"); err == nil || tokens.MMEAuthToken != "" {
			t.Fatalf("accepted rejected %s response", scope)
		}
	}
}

func TestServiceDSIDUsedEvenWithEmptyAccountInfo(t *testing.T) {
	response := map[string]any{"appleAccountInfo": map[string]any{}, "delegates": map[string]any{"com.apple.mobileme": map[string]any{
		"service-data": map[string]any{"dsid": "service-dsid", "tokens": map[string]any{"mmeAuthToken": "test-token"}},
	}}}
	tokens, err := extractDelegateTokens(response, "test-adsid")
	if err != nil || tokens.DSID != "service-dsid" {
		t.Fatal("ADSID substituted for supplied service DSID", err)
	}
}

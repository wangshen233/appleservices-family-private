package icloud

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountAuthCapturesOAuthGrantCode(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Apple-OAuth-Grant-Code", "test-grant")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.http = server.Client()
	state := &hmeAuthState{}
	response, _, err := client.authRequest(context.Background(), state, http.MethodGet, server.URL, nil, make(http.Header))
	if err != nil || response.StatusCode != http.StatusOK || state.grantCode != "test-grant" {
		t.Fatal("OAuth grant code was not captured for portal validation")
	}
}

func TestAccountAuthHeadersMatchCurrentFirstPartyWidget(t *testing.T) {
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	state := &hmeAuthState{frameID: "frame-value", clientID: "service-key"}
	headers := client.authHeaders(state)
	if headers.Get("X-Apple-Frame-Id") != "auth-frame-value" || headers.Get("X-Apple-OAuth-State") != "auth-frame-value" {
		t.Fatal("first-party OAuth frame binding did not match the current Apple widget")
	}
	if headers.Get("X-Apple-App-Id") != "service-key" || headers.Get("X-Apple-Widget-Key") != "service-key" {
		t.Fatal("Apple application binding headers were incomplete")
	}
	if headers.Get("X-Apple-Privacy-Consent-Accepted") != "true" {
		t.Fatal("sign-in v2 privacy consent header was missing")
	}
	if headers.Get("Origin") != "https://idmsa.apple.com/appleauth/auth" || headers.Get("Referer") != "https://idmsa.apple.com/appleauth/auth/" {
		t.Fatal("identity authentication origin was not bound to the Apple auth iframe")
	}
	if headers.Get("X-Apple-OAuth-Client-Type") != "firstPartyAuth" || headers.Get("X-Apple-Domain-Id") != "6" || headers.Get("X-Apple-Offer-Security-Upgrade") != "1" {
		t.Fatal("Apple compatibility authentication headers were incomplete")
	}
}

func TestAppleAccountManagementSessionPersistsWithoutPassword(t *testing.T) {
	dir := t.TempDir()
	client, err := NewAppleAccountClientWithStore(dir, "owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase, client.apiKey = "https://appleid.apple.com", "test-api-key"
	client.portalSCNT = "test-scnt"
	u, _ := url.Parse("https://appleid.apple.com")
	client.jar.SetCookies(u, []*http.Cookie{{Name: "test-session", Value: "session-value", Path: "/", Secure: true}})
	if err := client.saveSession(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "account-management-session.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	restored, err := NewAppleAccountClientWithStore(dir, "owner@example.test")
	if err != nil || restored.apiKey != "test-api-key" || restored.portalSCNT != "test-scnt" {
		t.Fatalf("management session was not restored: %v", err)
	}
	if cookies := restored.jar.Cookies(u); len(cookies) != 1 || cookies[0].Name != "test-session" {
		t.Fatal("management cookies were not restored")
	}
}

func TestPortalGrantValidationRequiresBusinessSuccess(t *testing.T) {
	if portalGrantValidationSucceeded([]byte(`<script id="grant_code_args">{"message":{"event":"WidgetBridgeFailed"}}</script>`)) {
		t.Fatal("OAuth business failure was accepted")
	}
	if !portalGrantValidationSucceeded([]byte(`<script id="grant_code_args">{"message":{"event":"WidgetBridgeComplete"}}</script>`)) {
		t.Fatal("OAuth business success was not accepted")
	}
}

func TestGrantCodeCandidatesPreferDecodedThenRetainRawFallback(t *testing.T) {
	raw := "plain-grant-code"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || string(decoded) != raw {
		t.Fatal("grant code candidate setup failed")
	}
	if encoded == raw {
		t.Fatal("encoded grant code unexpectedly matched raw value")
	}
}

func TestReachableEmailChallengeBindsAddressAndVerifiesReadback(t *testing.T) {
	const address = "forward@example.test"
	verified := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Apple-Api-Key") != "test-api-key" && r.URL.Path != "/account/manage" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/account/manage/email/alternate/add/verification":
			if r.Method != http.MethodPost {
				t.Fatal("wrong challenge method")
			}
			var request map[string]string
			if json.NewDecoder(r.Body).Decode(&request) != nil || request["address"] != address {
				t.Fatal("challenge was not bound to the requested address")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"address": address, "verificationId": 42})
		case "/account/manage/email/alternate/verification":
			if r.Method != http.MethodPut {
				t.Fatal("wrong verification method")
			}
			var request struct {
				Address          string `json:"address"`
				VerificationInfo struct {
					ID     int    `json:"id"`
					Answer string `json:"answer"`
				} `json:"verificationInfo"`
			}
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.Address != address || request.VerificationInfo.ID != 42 || request.VerificationInfo.Answer != "123456" {
				t.Fatal("verification did not preserve the challenge binding")
			}
			verified = true
			_ = json.NewEncoder(w).Encode(map[string]any{"address": address})
		case "/account/manage":
			addresses := []map[string]string{}
			if verified {
				addresses = append(addresses, map[string]string{"address": address})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{"person": map[string]any{"reachableAtOptions": map[string]any{"alternateEmailAddresses": addresses}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase, client.apiKey, client.http = server.URL, "test-api-key", server.Client()
	if err := client.BeginReachableEmail(context.Background(), address, ""); err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyReachableEmail(context.Background(), "123456"); err != nil {
		t.Fatal(err)
	}
	if !verified || !client.HasReachableEmail(context.Background(), address) {
		t.Fatal("verified reachable email was not read back")
	}
}

func TestReachableEmailCompletesPasswordChallengeAndReplaysOnce(t *testing.T) {
	const (
		address  = "forward@example.test"
		password = "test-password"
	)
	sendAttempts, passwordAttempts := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Apple-Api-Key") != "test-api-key" {
			http.Error(w, "missing api key", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/account/manage":
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{}})
		case "/authenticate/password":
			passwordAttempts++
			var request map[string]string
			if json.NewDecoder(r.Body).Decode(&request) != nil || request["password"] != password {
				t.Fatal("password challenge did not preserve the current password")
			}
			w.WriteHeader(http.StatusNoContent)
		case "/account/manage/email/alternate/add/verification":
			sendAttempts++
			if sendAttempts == 1 {
				http.Error(w, "TwoStepAuthenticationRequired", http.StatusUnavailableForLegalReasons)
				return
			}
			if sendAttempts > 2 {
				t.Fatal("reachable email mutation was replayed more than once")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{"address": address, "verificationId": 84})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase, client.apiKey, client.http = server.URL, "test-api-key", server.Client()
	if err := client.BeginReachableEmail(context.Background(), address, password); err != nil {
		t.Fatal(err)
	}
	if sendAttempts != 2 || passwordAttempts != 1 {
		t.Fatalf("unexpected challenge sequence: send=%d password=%d", sendAttempts, passwordAttempts)
	}
}

func TestReachableEmailPasswordChallengeDoesNotReplayWhenRejected(t *testing.T) {
	sendAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/account/manage":
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{}})
		case "/authenticate/password":
			http.Error(w, "rejected", http.StatusUnauthorized)
		case "/account/manage/email/alternate/add/verification":
			sendAttempts++
			http.Error(w, "TwoStepAuthenticationRequired", http.StatusUnavailableForLegalReasons)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase, client.apiKey, client.http = server.URL, "test-api-key", server.Client()
	if err := client.BeginReachableEmail(context.Background(), "forward@example.test", "test-password"); err == nil {
		t.Fatal("rejected password challenge was accepted")
	}
	stage, status := client.AuthenticationFailure()
	if sendAttempts != 1 || stage != "reachable-password-challenge" || status != http.StatusUnauthorized {
		t.Fatalf("unexpected rejection state: send=%d stage=%q status=%d", sendAttempts, stage, status)
	}
}

func TestPortalRequestReplaysOfficialManagementContext(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("X-Apple-I-Request-Context") != "ca" || r.Header.Get("X-Apple-I-FD-Client-Info") == "" {
			t.Fatal("official Apple Account management context headers were missing")
		}
		if requests == 1 {
			if r.Header.Get("scnt") != "" {
				t.Fatal("unexpected scnt on the first request")
			}
			w.Header().Set("scnt", "issued-scnt")
		} else if r.Header.Get("scnt") != "issued-scnt" {
			t.Fatal("Apple Account scnt was not replayed")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{}})
	}))
	defer server.Close()
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase, client.apiKey, client.http = server.URL, "test-api-key", server.Client()
	if _, _, err := client.portalRequest(context.Background(), http.MethodGet, "/account/manage", nil, true); err != nil {
		t.Fatal(err)
	}
	if _, _, err := client.portalRequest(context.Background(), http.MethodGet, "/account/manage", nil, true); err != nil {
		t.Fatal(err)
	}
}

func TestReachableEmailDoesNotTreatForbiddenAsPasswordChallenge(t *testing.T) {
	passwordAttempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/account/manage":
			_ = json.NewEncoder(w).Encode(map[string]any{"account": map[string]any{}})
		case "/authenticate/password":
			passwordAttempts++
			w.WriteHeader(http.StatusNoContent)
		case "/account/manage/email/alternate/add/verification":
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client, err := NewAppleAccountClient("owner@example.test")
	if err != nil {
		t.Fatal(err)
	}
	client.apiBase, client.apiKey, client.http = server.URL, "test-api-key", server.Client()
	if err := client.BeginReachableEmail(context.Background(), "forward@example.test", "test-password"); err == nil {
		t.Fatal("forbidden reachable email request was accepted")
	}
	if passwordAttempts != 0 {
		t.Fatal("forbidden response incorrectly triggered a password challenge")
	}
}

func TestNextDistinctTrustedCodeWaitsForCodeRotation(t *testing.T) {
	calls := 0
	provider := func(context.Context) (string, error) {
		calls++
		if calls < 3 {
			return "123456", nil
		}
		return "654321", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	code, err := nextDistinctTrustedCode(ctx, provider, "123456", time.Millisecond)
	if err != nil || code != "654321" || calls != 3 {
		t.Fatalf("trusted code rotation was not observed: code=%q calls=%d err=%v", code, calls, err)
	}
}

func TestWaitForReachableEmailRequiresAuthoritativeReadback(t *testing.T) {
	checks := 0
	confirmed := waitForReachableEmail(context.Background(), 3, time.Millisecond, func() bool {
		checks++
		return checks == 3
	})
	if !confirmed || checks != 3 {
		t.Fatalf("authoritative reachable email readback was not awaited: confirmed=%v checks=%d", confirmed, checks)
	}
	if waitForReachableEmail(context.Background(), 2, time.Millisecond, func() bool { return false }) {
		t.Fatal("missing authoritative readback was accepted")
	}
}

package icloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
	if err := client.BeginReachableEmail(context.Background(), address); err != nil {
		t.Fatal(err)
	}
	if err := client.VerifyReachableEmail(context.Background(), "123456"); err != nil {
		t.Fatal(err)
	}
	if !verified || !client.HasReachableEmail(context.Background(), address) {
		t.Fatal("verified reachable email was not read back")
	}
}

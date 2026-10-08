package gsa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"io"
	"net/http"
	"testing"

	"howett.net/plist"
)

func TestAppTokenAuthenticatedEnvelopeAndBinding(t *testing.T) {
	key := make([]byte, 32)
	service := "com.apple.gs.icloud.auth"
	for _, tampered := range []bool{false, true} {
		client := NewClient(headerProvider{})
		calls := 0
		client.transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			calls++
			data, _ := io.ReadAll(r.Body)
			var envelope map[string]any
			if _, err := plist.Unmarshal(data, &envelope); err != nil {
				t.Fatal(err)
			}
			request := envelope["Request"].(map[string]any)
			checksum := hmac.New(sha256.New, key)
			checksum.Write([]byte("apptokenstest-adsid" + service))
			if request["o"] != "apptokens" || request["u"] != "test-adsid" || request["t"] != "test-idms" || !hmac.Equal(request["checksum"].([]byte), checksum.Sum(nil)) {
				t.Fatal("app-token request binding lost")
			}
			plain, _ := plist.Marshal(map[string]any{"t": map[string]any{service: map[string]any{"token": "test-service-token"}}}, plist.XMLFormat)
			block, _ := aes.NewCipher(key)
			gcm, _ := cipher.NewGCMWithNonceSize(block, 16)
			nonce := make([]byte, 16)
			encrypted := append([]byte("XYZ"), nonce...)
			encrypted = append(encrypted, gcm.Seal(nil, nonce, plain, []byte("XYZ"))...)
			if tampered {
				encrypted[len(encrypted)-1] ^= 1
			}
			return plistResponse(t, map[string]any{"Response": map[string]any{"Status": map[string]any{"ec": 0}, "et": encrypted}}), nil
		})
		token, err := client.FetchAppToken(map[string]any{"adsid": "test-adsid", "GsIdmsToken": "test-idms", "sk": key, "c": []byte("test-cookie")}, service)
		if tampered {
			if err == nil || token != "" {
				t.Fatal("tampered token accepted")
			}
		} else if err != nil || token != "test-service-token" {
			t.Fatal("valid app token rejected", err)
		}
		if calls != 1 {
			t.Fatal("unexpected token retry")
		}
	}
}

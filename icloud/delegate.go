package icloud

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/internal/httpx"
	"github.com/Laky-64/appleservices/internal/uuid"
	"github.com/Laky-64/http"
	"howett.net/plist"
)

var setupBaseURL = "https://setup.icloud.com"

// setupBaseFor returns the setup host for an account's region. Apple operates
// iCloud in mainland China through a separate operator (GCBD) on the .com.cn
// domains; asking the global host for a mainland account's delegates is
// answered with "A server problem is blocking Apple ID sign in" and no
// service-data. APPLESERVICES_ICLOUD_SETUP overrides the host outright.
func setupBaseFor(appleID string) string {
	if override := strings.TrimSpace(os.Getenv("APPLESERVICES_ICLOUD_SETUP")); override != "" {
		return override
	}
	return setupBaseURL
}

type DelegateTokens struct {
	DSID          string
	MMEAuthToken  string
	CloudKitToken string
	ServiceData   map[string]any
}

func FetchDelegateTokens(anisetteHeaders map[string]string, username, adsid, pet string) (DelegateTokens, error) {
	anisetteHeaders = httpx.NormalizeAnisetteHeaders(anisetteHeaders)
	body := map[string]any{
		"apple-id":  username,
		"delegates": map[string]any{"com.apple.mobileme": map[string]any{}},
		"password":  pet,
		"client-id": uuid.New(),
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: encode loginDelegates body: %w", err)
	}

	headers := map[string]string{
		"Content-Type":      "text/plist",
		"X-Apple-ADSID":     adsid,
		"Cookie":            "termsAccepted=true",
		"User-Agent":        "com.apple.iCloudHelper/282 CFNetwork/1408.0.4 Darwin/22.5.0",
		"X-Mme-Client-Info": "<MacBookPro18,3> <Mac OS X;13.4.1;22F8> <com.apple.AOSKit/282 (com.apple.accountsd/113)>",
		"Authorization":     httpx.BasicAuth(username, pet),
	}
	for k, v := range anisetteHeaders {
		headers[k] = v
	}

	result, err := http.ExecuteRequest(setupBaseFor(username)+"/setup/iosbuddy/loginDelegates",
		http.Method("POST"),
		http.Body(buf.Bytes()),
		http.Headers(headers),
		http.Timeout(30*time.Second),
	)
	if err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: loginDelegates request: %w", err)
	}
	if result.StatusCode != 200 {
		return DelegateTokens{}, fmt.Errorf("icloud: loginDelegates status %d", result.StatusCode)
	}

	var decoded map[string]any
	if _, err := plist.Unmarshal(result.Body, &decoded); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: decode loginDelegates response: %w", err)
	}
	if path := strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_DUMP")); path != "" {
		// A refusal explains itself in localizedError and description, which the
		// per-delegate status does not carry. Keep the raw plist so a failure
		// stays diagnosable instead of collapsing into "status 1".
		_ = os.WriteFile(path, result.Body, 0o600)
	}
	return extractDelegateTokens(decoded, adsid)
}

func extractDelegateTokens(resp map[string]any, fallbackDSID string) (DelegateTokens, error) {
	// localizedError and description are separate channels from the delegate's
	// own status fields, and they are where a refusal states its reason. Reading
	// only the delegate status turns an explained failure into "status 1".
	localized, _ := resp["localizedError"].(string)
	description, _ := resp["description"].(string)
	explain := func() string {
		parts := make([]string, 0, 2)
		if localized != "" {
			parts = append(parts, fmt.Sprintf("localizedError=%q", localized))
		}
		if description != "" {
			parts = append(parts, fmt.Sprintf("description=%q", description))
		}
		if len(parts) == 0 {
			return ""
		}
		return " (" + strings.Join(parts, ", ") + ")"
	}
	if top, ok := resp["status"]; ok && !isZeroStatus(top) {
		if explanation := explain(); explanation != "" {
			return DelegateTokens{}, errors.New("icloud: loginDelegates refused the delegate" + explanation)
		}
	}
	delegates, ok := resp["delegates"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: loginDelegates response has no delegates" + explain())
	}
	mme, ok := delegates["com.apple.mobileme"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: loginDelegates response has no com.apple.mobileme delegate" + explain())
	}
	sd, ok := mme["service-data"].(map[string]any)
	if !ok {
		msg, _ := mme["status-message"].(string)
		shape := jsonShape(mme, 0)
		if len(shape) > 400 {
			shape = shape[:400]
		}
		return DelegateTokens{}, fmt.Errorf("icloud: mobileme delegate has no service-data (status-message: %q, delegate: %s)%s", msg, shape, explain())
	}
	tokens, ok := sd["tokens"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: mobileme service-data has no tokens")
	}
	mmeToken, ok := tokens["mmeAuthToken"].(string)
	if !ok || mmeToken == "" {
		msg, _ := mme["status-message"].(string)
		return DelegateTokens{}, fmt.Errorf("icloud: no mmeAuthToken in response (status-message: %q)", msg)
	}
	ckToken, _ := tokens["cloudKitToken"].(string)

	dsid := fallbackDSID
	if d := coerceDSID(resp["dsid"]); d != "" {
		dsid = d
	} else if d := coerceDSID(resp["DsPrsId"]); d != "" {
		dsid = d
	} else if accountInfo, ok := resp["appleAccountInfo"].(map[string]any); ok {
		if d := coerceDSID(accountInfo["dsPrsID"]); d != "" {
			dsid = d
		}
	} else if d := coerceDSID(sd["dsid"]); d != "" {
		dsid = d
	}
	return DelegateTokens{DSID: dsid, MMEAuthToken: mmeToken, CloudKitToken: ckToken, ServiceData: sd}, nil
}

// isZeroStatus reports whether a plist status value means success. Apple sends
// the field as an integer in the delegate response and as a string elsewhere.
func isZeroStatus(v any) bool {
	switch s := v.(type) {
	case int64:
		return s == 0
	case uint64:
		return s == 0
	case int:
		return s == 0
	case string:
		return strings.TrimSpace(s) == "0"
	}
	return false
}

func coerceDSID(v any) string {
	switch d := v.(type) {
	case string:
		return d
	case uint64:
		return strconv.FormatUint(d, 10)
	case int64:
		return strconv.FormatInt(d, 10)
	case int:
		return strconv.Itoa(d)
	case uint:
		return strconv.FormatUint(uint64(d), 10)
	}
	return ""
}

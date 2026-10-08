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
	"github.com/Laky-64/http/types"
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

	// The iosbuddy path takes the Apple ID and PET in the body and answers a
	// refusal with a generic message. The other two paths report the reason:
	// prefpane names it in ErrorID and localizedError, and the v2 sign-in
	// endpoint, which a working client uses, takes a
	// delegates/protocol_version/user_info document.
	switch strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_PATH")) {
	case "prefpane":
		return fetchDelegateTokensAt(anisetteHeaders, username, adsid, pet, "/setup/prefpane/loginDelegates")
	case "v2":
		return fetchDelegateTokensV2(anisetteHeaders, username, adsid, pet)
	}
	if strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_V2")) != "" {
		return fetchDelegateTokensV2(anisetteHeaders, username, adsid, pet)
	}

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
	return finishDelegate(result, err, adsid, "loginDelegates")
}

// fetchDelegateTokensAt posts the same document to a named delegate path.
// The prefpane path is worth using while diagnosing: unlike iosbuddy it names
// the reason in ErrorID and localizedError instead of a generic message.
func fetchDelegateTokensAt(anisetteHeaders map[string]string, username, adsid, pet, path string) (DelegateTokens, error) {
	body := map[string]any{
		"apple-id":  username,
		"delegates": map[string]any{"com.apple.mobileme": map[string]any{}},
		"password":  pet,
		"client-id": uuid.New(),
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: encode delegate body: %w", err)
	}
	headers := map[string]string{
		"Content-Type":      "text/plist",
		"X-Apple-ADSID":     adsid,
		"User-Agent":        "com.apple.iCloudHelper/282 CFNetwork/1408.0.4 Darwin/22.5.0",
		"X-Mme-Client-Info": "<MacBookPro18,3> <Mac OS X;13.4.1;22F8> <com.apple.AOSKit/282 (com.apple.accountsd/113)>",
		"Authorization":     httpx.BasicAuth(username, pet),
	}
	for k, v := range anisetteHeaders {
		headers[k] = v
	}
	result, err := http.ExecuteRequest(setupBaseFor(username)+path,
		http.Method("POST"),
		http.Body(buf.Bytes()),
		http.Headers(headers),
		http.Timeout(30*time.Second),
	)
	return finishDelegate(result, err, adsid, path)
}

// finishDelegate keeps the raw response, reports a transport failure, and hands
// the decoded document to the token extractor.
func finishDelegate(result *types.HTTPResult, err error, adsid, label string) (DelegateTokens, error) {
	if result != nil {
		if path := strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_DUMP")); path != "" {
			_ = os.WriteFile(path, result.Body, 0o600)
		}
	}
	if err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: %s request: %w", label, err)
	}
	if result.StatusCode != 200 {
		return DelegateTokens{}, fmt.Errorf("icloud: %s status %d", label, result.StatusCode)
	}
	var decoded map[string]any
	if _, err := plist.Unmarshal(result.Body, &decoded); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: decode %s response: %w", label, err)
	}
	return extractDelegateTokens(decoded, adsid)
}

// fetchDelegateTokensV2 posts the delegate request to Apple's v2 sign-in
// endpoint. The request shape and headers follow a client that completes this
// exchange: a delegates/protocol_version/user_info document, the iCloud helper
// agent under accountsd, and Basic authentication with the account's PET.
func fetchDelegateTokensV2(anisetteHeaders map[string]string, username, adsid, pet string) (DelegateTokens, error) {
	body := map[string]any{
		"delegates":        map[string]any{"com.apple.mobileme": map[string]any{}},
		"protocol_version": "1.0",
		"user_info": map[string]any{
			// The user-info keys are kebab-case in the reference client while
			// the two outer keys are not.
			"client-id": uuid.New(),
			"language":  "en-US",
			"timezone":  "America/New_York",
		},
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: encode v2 login body: %w", err)
	}

	headers := map[string]string{
		"Content-Type":      "text/plist",
		"Accept-Encoding":   "gzip",
		"User-Agent":        "com.apple.iCloudHelper/282 CFNetwork/1408.0.4 Darwin/22.5.0",
		"X-Mme-Client-Info": "<MacBookPro18,3> <macOS;13.4.1;22F8> <com.apple.AOSKit/282 (com.apple.accountsd/113)>",
		"X-Apple-ADSID":     adsid,
		"Authorization":     httpx.BasicAuth(username, pet),
	}
	for k, v := range anisetteHeaders {
		if strings.EqualFold(k, "X-Mme-Client-Info") || strings.EqualFold(k, "User-Agent") {
			continue
		}
		headers[k] = v
	}

	result, err := http.ExecuteRequest(setupBaseFor(username)+"/setup/signin/v2/login",
		http.Method("POST"),
		http.Body(buf.Bytes()),
		http.Headers(headers),
		http.Timeout(30*time.Second),
	)
	if result != nil {
		if path := strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_DUMP")); path != "" {
			_ = os.WriteFile(path, result.Body, 0o600)
		}
	}
	if err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: v2 login request: %w", err)
	}
	if result.StatusCode != 200 {
		return DelegateTokens{}, fmt.Errorf("icloud: v2 login status %d", result.StatusCode)
	}

	var decoded map[string]any
	if _, err := plist.Unmarshal(result.Body, &decoded); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: decode v2 login response: %w", err)
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

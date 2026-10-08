package icloud

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/internal/httpx"
	"github.com/Laky-64/appleservices/internal/uuid"
	"github.com/Laky-64/http"
	"github.com/Laky-64/http/types"
	"howett.net/plist"
	nethttp "net/http"
)

var setupBaseURL = "https://setup.icloud.com"

// setupBaseFor honors an explicit regional endpoint override. An Apple ID's
// spelling does not identify its region, and a generic error cannot prove it.
func setupBaseFor(appleID string) string {
	if override := strings.TrimSpace(os.Getenv("APPLESERVICES_ICLOUD_SETUP")); override != "" {
		return strings.TrimRight(override, "/")
	}
	return setupBaseURL
}

type DelegateTokens struct {
	DSID          string
	MMEAuthToken  string
	CloudKitToken string
	ServiceData   map[string]any
}

// FetchDelegateTokens performs one exchange. Endpoint experiments must use a
// separately refreshed PET; this function never falls back or retries a refusal.
func FetchDelegateTokens(anisetteHeaders map[string]string, username, adsid, pet string) (DelegateTokens, error) {
	anisetteHeaders = httpx.NormalizeAnisetteHeaders(anisetteHeaders)
	profile := strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_PATH"))
	if profile == "" && strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_V2")) != "" {
		profile = "v2"
	}
	if validationPath := strings.TrimSpace(os.Getenv("APPLESERVICES_NAC_VALIDATION_FILE")); validationPath != "" {
		if profile != "v2" {
			return DelegateTokens{}, errors.New("icloud: NAC validation file requires explicit Mac v2 profile")
		}
		if anisetteHeaders["X-Mme-Nas-Qualify"] != "" {
			return DelegateTokens{}, errors.New("icloud: duplicate NAC validation sources")
		}
		validation, err := os.ReadFile(validationPath)
		if err != nil || len(validation) == 0 || len(validation) > 65536 {
			return DelegateTokens{}, errors.New("icloud: private NAC validation unavailable or invalid")
		}
		anisetteHeaders["X-Mme-Nas-Qualify"] = base64.StdEncoding.EncodeToString(validation)
	}
	path := "/setup/iosbuddy/loginDelegates"
	body := map[string]any{
		"apple-id":  username,
		"delegates": map[string]any{"com.apple.mobileme": map[string]any{}},
		"password":  pet,
		"client-id": uuid.New(),
	}
	switch profile {
	case "", "iosbuddy", "windows-iosbuddy":
	case "windows-aos":
		// Experimental Windows account refresh, recovered from AOSKitWin
		// 133.3. Unlike iosbuddy it sends an empty POST and returns a native
		// account-settings dictionary. No NAS/hardware proof is invented.
		path = "/setup/login_or_create_account"
	case "prefpane":
		// Retained as an experimental endpoint. No claim is made that the
		// legacy body matches the native prefpane protocol.
		path = "/setup/prefpane/loginDelegates"
	case "legacy-v2":
		// The headless RustPush compatibility client sends the legacy body
		// to its configured sign-in URL when validation data is unavailable.
		path = "/setup/signin/v2/login"
	case "v2":
		path = "/setup/signin/v2/login"
		// Rust V2AuthRequest uses serde rename_all="camelCase". Nested
		// V2AuthUserInfo uses kebab-case instead.
		body = map[string]any{
			"delegates":       map[string]any{"com.apple.mobileme": map[string]any{}},
			"protocolVersion": "1.0",
			"userInfo": map[string]any{
				"client-id": strings.ToUpper(uuid.New()),
				"language":  "en-US",
				"timezone":  "America/New_York",
			},
		}
	default:
		return DelegateTokens{}, errors.New("icloud: unsupported delegate profile")
	}
	var buf bytes.Buffer
	if err := plist.NewEncoder(&buf).Encode(body); err != nil {
		return DelegateTokens{}, fmt.Errorf("icloud: encode delegate body: %w", err)
	}
	if profile == "windows-aos" {
		buf.Reset()
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
		// The provider's akd User-Agent belongs to GSA, not the iCloud
		// delegate request. The native wrapper adds it for authentication.
		if strings.EqualFold(k, "User-Agent") {
			continue
		}
		headers[k] = v
	}
	// The setup terms flow sets this only after Apple acknowledged success.
	// Keep it opt-in so an unverified account cannot masquerade as accepted.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("APPLESERVICES_TERMS_ACCEPTED")), "1") {
		headers["Cookie"] = "termsAccepted=true"
	}
	if profile == "v2" || profile == "legacy-v2" {
		// Preserve the provider's model and OS; change only the consuming
		// framework. The previous code silently used a different Mac model.
		info := headers["X-Mme-Client-Info"]
		if split := strings.LastIndex(info, "<"); split > 0 {
			headers["X-Mme-Client-Info"] = info[:split] + "<com.apple.AOSKit/282 (com.apple.accountsd/113)>"
		}
		headers["User-Agent"] = "com.apple.iCloudHelper/282 CFNetwork/1408.0.4 Darwin/22.5.0"
	}
	if profile == "windows-aos" || profile == "windows-iosbuddy" {
		info := headers["X-Mme-Client-Info"]
		if !strings.HasPrefix(info, "<PC> <Windows; ") {
			return DelegateTokens{}, errors.New("icloud: Windows probe requires a Windows provider identity")
		}
		if split := strings.LastIndex(info, "<"); split > 0 {
			// 129.8 is the installed DLL's literal fallback framework version.
			headers["X-Mme-Client-Info"] = info[:split] + "<com.apple.AOSKit/129.8 (com.apple.iCloud/15.10)>"
		}
		headers["User-Agent"] = anisetteHeaders["User-Agent"]
		for _, key := range []string{"capp", "cbid", "svct", "loc"} {
			delete(headers, key)
		}
		headers["X-Mme-Timezone"], headers["Accept-Language"] = "UTC", "en"
		if profile == "windows-aos" {
			headers["X-Aos-Accept-Tos"] = "false"
			delete(headers, "Content-Type")
			// Native AOSKit's AuthKit adapter uses these older MD aliases.
			headers["X-Apple-MD"], headers["X-Apple-MD-M"] = headers["X-Apple-I-MD"], headers["X-Apple-I-MD-M"]
		}
	}
	transport := gsa.AppleRootTransport()
	defer transport.(*nethttp.Transport).CloseIdleConnections()
	result, err := http.ExecuteRequest(setupBaseFor(username)+path,
		http.Method("POST"), http.Body(buf.Bytes()), http.Headers(headers),
		http.Transport(transport), http.Timeout(30*time.Second))
	return finishDelegate(result, err, adsid, path)
}

// DelegateError retains machine-readable Apple diagnostics, including on HTTP
// 400. Neither Error() nor these fields contain raw bodies or credential values.
type DelegateError struct {
	HTTPStatus      int
	ErrorID         string
	ReasonCode      string
	ProtocolVersion string
	Stage           string
}

func (e *DelegateError) Error() string {
	message := "icloud: " + e.Stage
	if e.HTTPStatus != 0 {
		message += fmt.Sprintf(" (HTTP %d)", e.HTTPStatus)
	}
	for _, item := range []struct{ key, value string }{
		{"ErrorID", e.ErrorID}, {"reason-code", e.ReasonCode}, {"protocolVersion", e.ProtocolVersion},
	} {
		if item.value != "" {
			message += " " + item.key + "=" + item.value
		}
	}
	return message
}

func safeDiagnosticCode(value any) string {
	text := coerceDSID(value)
	if len(text) > 100 {
		return ""
	}
	for _, char := range text {
		if !(char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' || char == '.') {
			return ""
		}
	}
	return text
}

func delegateFailure(decoded map[string]any, stage string) *DelegateError {
	id := safeDiagnosticCode(decoded["ErrorID"])
	if id == "" {
		id = safeDiagnosticCode(decoded["localizedError"])
	}
	return &DelegateError{ErrorID: id, Stage: stage, ProtocolVersion: safeDiagnosticCode(decoded["protocolVersion"])}
}

func finishDelegate(result *types.HTTPResult, err error, adsid, label string) (DelegateTokens, error) {
	if result != nil {
		if path := strings.TrimSpace(os.Getenv("APPLESERVICES_DELEGATE_DUMP")); path != "" {
			if writeErr := os.WriteFile(path, result.Body, 0o600); writeErr != nil {
				return DelegateTokens{}, errors.New("icloud: cannot save requested delegate dump")
			}
		}
	}
	if err != nil && (result == nil || result.StatusCode == 0 || result.StatusCode == 200) {
		return DelegateTokens{}, fmt.Errorf("icloud: %s transport failed", label)
	}
	if result == nil {
		return DelegateTokens{}, errors.New("icloud: delegate returned no response")
	}
	body, decodeErr := decodeDelegateBody(result.Body)
	var decoded map[string]any
	if decodeErr == nil {
		_, decodeErr = plist.Unmarshal(body, &decoded)
	}
	if result.StatusCode != 200 {
		failure := delegateFailure(decoded, label+" refused")
		failure.HTTPStatus = result.StatusCode
		for key, values := range result.Headers {
			if strings.EqualFold(key, "X-Apple-Reason-Code") && len(values) > 0 {
				failure.ReasonCode = safeDiagnosticCode(values[0])
			}
		}
		return DelegateTokens{}, failure
	}
	if decodeErr != nil {
		return DelegateTokens{}, errors.New("icloud: delegate response is not a plist")
	}
	if label == "/setup/login_or_create_account" {
		return extractWindowsAccountTokens(decoded)
	}
	return extractDelegateTokens(decoded, adsid)
}

// Explicit Accept-Encoding disables Go's automatic gzip decompression. Apple
// can return gzip on a successful delegate exchange; keep raw diagnostic dumps
// intact and decode a bounded copy before parsing.
func decodeDelegateBody(body []byte) ([]byte, error) {
	const maxBody = 4 << 20
	if len(body) > maxBody {
		return nil, errors.New("icloud: oversized delegate response")
	}
	if len(body) < 2 || body[0] != 0x1f || body[1] != 0x8b {
		return body, nil
	}
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("icloud: invalid gzip delegate response")
	}
	defer r.Close()
	decoded, err := io.ReadAll(io.LimitReader(r, maxBody+1))
	if err != nil || len(decoded) > maxBody {
		return nil, errors.New("icloud: invalid or oversized gzip delegate response")
	}
	return decoded, nil
}

func extractWindowsAccountTokens(resp map[string]any) (DelegateTokens, error) {
	if status, ok := resp["status"]; ok && !isZeroStatus(status) {
		return DelegateTokens{}, delegateFailure(resp, "Windows account refresh refused")
	}
	failure := delegateFailure(resp, "Windows account refresh refused")
	if failure.ErrorID != "" {
		return DelegateTokens{}, failure
	}
	account, _ := resp["appleAccountInfo"].(map[string]any)
	tokens, _ := resp["tokens"].(map[string]any)
	dsid := coerceDSID(account["dsPrsID"])
	mme, _ := tokens["mmeAuthToken"].(string)
	ck, _ := tokens["cloudKitToken"].(string)
	if dsid == "" || mme == "" {
		return DelegateTokens{}, errors.New("icloud: Windows account response has no bound MME token")
	}
	return DelegateTokens{DSID: dsid, MMEAuthToken: mme, CloudKitToken: ck, ServiceData: resp}, nil
}

func extractDelegateTokens(resp map[string]any, fallbackDSID string) (DelegateTokens, error) {
	failure := delegateFailure(resp, "loginDelegates refused")
	if top, ok := resp["status"]; ok && !isZeroStatus(top) {
		return DelegateTokens{}, failure
	}
	if failure.ErrorID != "" {
		return DelegateTokens{}, failure
	}
	delegates, ok := resp["delegates"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: loginDelegates response has no delegates")
	}
	mme, ok := delegates["com.apple.mobileme"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: loginDelegates response has no com.apple.mobileme delegate")
	}
	if status, ok := mme["status"]; ok && !isZeroStatus(status) {
		return DelegateTokens{}, delegateFailure(mme, "mobileme delegate refused status="+safeDiagnosticCode(status))
	}
	sd, ok := mme["service-data"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: mobileme delegate has no service-data")
	}
	tokens, ok := sd["tokens"].(map[string]any)
	if !ok {
		return DelegateTokens{}, errors.New("icloud: mobileme service-data has no tokens")
	}
	mmeToken, ok := tokens["mmeAuthToken"].(string)
	if !ok || mmeToken == "" {
		return DelegateTokens{}, errors.New("icloud: no mmeAuthToken in response")
	}
	ckToken, _ := tokens["cloudKitToken"].(string)

	dsid := ""
	if d := coerceDSID(resp["dsid"]); d != "" {
		dsid = d
	} else if d := coerceDSID(resp["DsPrsId"]); d != "" {
		dsid = d
	} else if accountInfo, ok := resp["appleAccountInfo"].(map[string]any); ok {
		if d := coerceDSID(accountInfo["dsPrsID"]); d != "" {
			dsid = d
		}
	}
	if dsid == "" {
		dsid = coerceDSID(sd["dsid"])
	}
	if dsid == "" {
		dsid = fallbackDSID
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

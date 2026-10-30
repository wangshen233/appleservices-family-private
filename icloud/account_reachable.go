package icloud

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/gsa"
	"github.com/Laky-64/appleservices/internal/srp"
)

var ErrAccountManagementAuthenticationRequired = errors.New("Apple Account management authentication required")

type AppleAccountClient struct {
	appleID       string
	http          *http.Client
	jar           http.CookieJar
	apiBase       string
	apiKey        string
	pendingEmail  string
	pendingVerify json.RawMessage
	lastStage     string
	lastHTTP      int
}

type portalBootstrap struct {
	APIURL     string `json:"apiUrl"`
	ServiceKey string `json:"serviceKey"`
	ServiceURL string `json:"serviceUrl"`
}

func NewAppleAccountClient(appleID string) (*AppleAccountClient, error) {
	appleID = strings.TrimSpace(appleID)
	if appleID == "" {
		return nil, errors.New("Apple Account binding is incomplete")
	}
	jar, _ := cookiejar.New(nil)
	transport := gsa.AppleRootTransport().(*http.Transport)
	transport.Proxy = nil
	return &AppleAccountClient{appleID: appleID, jar: jar, http: &http.Client{Jar: jar, Transport: transport, Timeout: 35 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *AppleAccountClient) authHeaders(state *hmeAuthState) http.Header {
	h := make(http.Header)
	if state.scnt != "" {
		h.Set("scnt", state.scnt)
	}
	if state.sessionID != "" {
		h.Set("X-Apple-ID-Session-Id", state.sessionID)
	}
	if state.authAttributes != "" {
		h.Set("X-Apple-Auth-Attributes", state.authAttributes)
	}
	if state.sessionToken != "" {
		h.Set("X-Apple-Session-Token", state.sessionToken)
	}
	h.Set("X-Apple-OAuth-Client-Id", state.clientID)
	h.Set("X-Apple-OAuth-Client-Type", "firstPartyAuth")
	h.Set("X-Apple-OAuth-Redirect-URI", "https://account.apple.com")
	h.Set("X-Apple-OAuth-Require-Grant-Code", "true")
	h.Set("X-Apple-OAuth-Response-Mode", "web_message")
	h.Set("X-Apple-OAuth-Response-Type", "code")
	h.Set("X-Apple-OAuth-State", "auth-"+state.frameID)
	h.Set("X-Apple-Widget-Key", state.clientID)
	h.Set("X-Apple-Frame-Id", state.frameID)
	h.Set("X-Apple-Domain-Id", "6")
	h.Set("X-Apple-Locale", "zh_CN")
	h.Set("X-Apple-Offer-Security-Upgrade", "1")
	h.Set("X-Apple-Privacy-Consent", "true")
	h.Set("X-Apple-I-FD-Client-Info", hmeFDClientInfo())
	h.Set("X-Requested-With", "XMLHttpRequest")
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")
	h.Set("Origin", "https://account.apple.com")
	h.Set("Referer", "https://account.apple.com/")
	h.Set("User-Agent", hmeWebUserAgent)
	return h
}

func (c *AppleAccountClient) authRequest(ctx context.Context, state *hmeAuthState, method, endpoint string, body []byte, headers http.Header) (*http.Response, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	request.Header = headers
	response, err := c.http.Do(request)
	if err != nil {
		return nil, nil, err
	}
	c.lastHTTP = response.StatusCode
	for name, target := range map[string]*string{
		"X-Apple-Auth-Attributes": &state.authAttributes, "X-Apple-ID-Session-Id": &state.sessionID,
		"scnt": &state.scnt, "X-Apple-Session-Token": &state.sessionToken,
		"X-Apple-TwoSV-Trust-Token": &state.trustToken, "X-Apple-ID-Account-Country": &state.accountCountry,
	} {
		if value := response.Header.Get(name); value != "" {
			*target = value
		}
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	response.Body.Close()
	if readErr != nil || len(data) > 2<<20 {
		return response, nil, errors.New("Apple Account authentication response was invalid")
	}
	return response, data, nil
}

func (c *AppleAccountClient) Authenticate(ctx context.Context, password string, codeProvider func(context.Context) (string, error)) error {
	if password == "" {
		return ErrAccountManagementAuthenticationRequired
	}
	c.lastStage, c.lastHTTP = "bootstrap", 0
	bootstrapRequest, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://appleid.apple.com/bootstrap/portal", nil)
	bootstrapRequest.Header.Set("Accept", "application/json")
	bootstrapRequest.Header.Set("User-Agent", hmeWebUserAgent)
	bootstrapResponse, err := c.http.Do(bootstrapRequest)
	if err != nil {
		return errors.New("Apple Account bootstrap failed")
	}
	bootstrapData, readErr := io.ReadAll(io.LimitReader(bootstrapResponse.Body, (1<<20)+1))
	c.lastHTTP = bootstrapResponse.StatusCode
	bootstrapResponse.Body.Close()
	var bootstrap portalBootstrap
	if readErr != nil || bootstrapResponse.StatusCode != http.StatusOK || json.Unmarshal(bootstrapData, &bootstrap) != nil || bootstrap.ServiceKey == "" || bootstrap.ServiceURL == "" || bootstrap.APIURL == "" {
		return errors.New("Apple Account bootstrap was invalid")
	}
	authBase := strings.TrimRight(bootstrap.ServiceURL, "/") + "/auth"
	state := &hmeAuthState{frameID: randomHex(16), clientID: bootstrap.ServiceKey}
	frame := "auth-" + state.frameID
	query := url.Values{"frame_id": {frame}, "language": {"en_US"}, "skVersion": {"7"}, "iframeId": {frame}, "client_id": {state.clientID}, "redirect_uri": {"https://account.apple.com"}, "response_type": {"code"}, "response_mode": {"web_message"}, "state": {frame}, "authVersion": {"latest"}}
	startHeaders := make(http.Header)
	startHeaders.Set("Accept", "*/*")
	startHeaders.Set("User-Agent", hmeWebUserAgent)
	c.lastStage = "authorize"
	response, _, err := c.authRequest(ctx, state, http.MethodGet, authBase+"/authorize/signin?"+query.Encode(), nil, startHeaders)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Apple Account authentication could not be initialized")
	}
	federateBody, _ := json.Marshal(map[string]any{"accountName": c.appleID, "rememberMe": false})
	c.lastStage = "federate"
	response, _, err = c.authRequest(ctx, state, http.MethodPost, authBase+"/federate?isRememberMeEnabled=false", federateBody, c.authHeaders(state))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Apple Account federation failed")
	}
	params := srp.NewParams()
	srpClient := srp.NewClient(params, c.appleID)
	initBody, _ := json.Marshal(map[string]any{"a": base64.StdEncoding.EncodeToString(srpClient.PublicA().Bytes()), "accountName": c.appleID, "protocols": []string{"s2k", "s2k_fo"}})
	c.lastStage = "srp-init"
	response, initData, err := c.authRequest(ctx, state, http.MethodPost, authBase+"/signin/init", initBody, c.authHeaders(state))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Apple Account SRP challenge failed")
	}
	var challenge struct {
		Iteration            int `json:"iteration"`
		Salt, Protocol, B, C string
	}
	if json.Unmarshal(initData, &challenge) != nil || challenge.Iteration <= 0 || challenge.Salt == "" || challenge.B == "" || challenge.C == "" {
		return errors.New("Apple Account SRP challenge was incomplete")
	}
	state.srpSession = challenge.C
	salt, saltErr := base64.StdEncoding.DecodeString(challenge.Salt)
	bBytes, bErr := base64.StdEncoding.DecodeString(challenge.B)
	if saltErr != nil || bErr != nil {
		return errors.New("Apple Account SRP challenge encoding was invalid")
	}
	passwordHash := sha256.Sum256([]byte(password))
	passwordKey, keyErr := pbkdf2.Key(sha256.New, string(passwordHash[:]), salt, challenge.Iteration, 32)
	if keyErr != nil {
		return keyErr
	}
	x := params.DeriveX(salt, passwordKey)
	m1, _, proofErr := srpClient.ProcessChallenge(x, salt, new(big.Int).SetBytes(bBytes))
	if proofErr != nil {
		return proofErr
	}
	m2, proofErr := srpClient.ExpectedM2()
	if proofErr != nil {
		return proofErr
	}
	completeBody, _ := json.Marshal(map[string]any{"accountName": c.appleID, "rememberMe": false, "trustTokens": []string{}, "m1": base64.StdEncoding.EncodeToString(m1), "c": state.srpSession, "m2": base64.StdEncoding.EncodeToString(m2)})
	c.lastStage = "srp-complete"
	response, _, err = c.authRequest(ctx, state, http.MethodPost, authBase+"/signin/complete?isRememberMeEnabled=false", completeBody, c.authHeaders(state))
	if err != nil {
		return err
	}
	if response.StatusCode == http.StatusConflict {
		if codeProvider == nil {
			return ErrAccountManagementAuthenticationRequired
		}
		c.lastStage = "trusted-challenge"
		response, _, err = c.authRequest(ctx, state, http.MethodPut, authBase+"/verify/trusteddevice/securitycode", nil, c.authHeaders(state))
		if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
			return errors.New("Apple Account trusted-device challenge failed")
		}
		codeCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		code, codeErr := codeProvider(codeCtx)
		cancel()
		if codeErr != nil || len(strings.TrimSpace(code)) != 6 {
			return errors.New("local trusted-device code generation failed")
		}
		verifyBody, _ := json.Marshal(map[string]any{"securityCode": map[string]string{"code": strings.TrimSpace(code)}})
		c.lastStage = "trusted-code"
		response, verifyData, err := c.authRequest(ctx, state, http.MethodPost, authBase+"/verify/trusteddevice/securitycode", verifyBody, c.authHeaders(state))
		for index := range verifyBody {
			verifyBody[index] = 0
		}
		code = ""
		if err != nil || !((response.StatusCode >= 200 && response.StatusCode < 300) || (response.StatusCode == http.StatusConflict && state.sessionToken != "" && bytes.Contains(verifyData, []byte(`"valid":true`)))) {
			return errors.New("Apple Account rejected the trusted-device verification code")
		}
	} else if response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Apple Account credentials were rejected")
	}
	c.lastStage = "trust-session"
	response, _, err = c.authRequest(ctx, state, http.MethodGet, authBase+"/2sv/trust", nil, c.authHeaders(state))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return errors.New("Apple Account web session trust failed")
	}
	c.apiBase = strings.TrimRight(bootstrap.APIURL, "/")
	c.lastStage = "portal-token"
	if _, status, err := c.portalRequest(ctx, http.MethodGet, "/account/manage/gs/ws/token", nil, false); err != nil {
		c.lastHTTP = status
		return errors.New("Apple Account management session was not established")
	}
	c.lastStage = "manage-data"
	data, status, err := c.portalRequest(ctx, http.MethodGet, "/account/manage", nil, false)
	c.lastHTTP = status
	if err != nil {
		return errors.New("Apple Account management data was unavailable")
	}
	var manage struct {
		APIKey string `json:"apiKey"`
	}
	if json.Unmarshal(data, &manage) != nil || manage.APIKey == "" {
		return errors.New("Apple Account management API key was unavailable")
	}
	c.apiKey = manage.APIKey
	c.lastStage, c.lastHTTP = "", 0
	return nil
}

func (c *AppleAccountClient) AuthenticationFailure() (string, int) {
	return c.lastStage, c.lastHTTP
}

func (c *AppleAccountClient) portalRequest(ctx context.Context, method, path string, payload any, requireAPIKey bool) ([]byte, int, error) {
	if c.apiBase == "" {
		return nil, 0, ErrAccountManagementAuthenticationRequired
	}
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, 0, err
		}
		body = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.apiBase+path, body)
	if err != nil {
		return nil, 0, err
	}
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://account.apple.com")
	request.Header.Set("Referer", "https://account.apple.com/")
	request.Header.Set("User-Agent", hmeWebUserAgent)
	if requireAPIKey {
		if c.apiKey == "" {
			return nil, 0, ErrAccountManagementAuthenticationRequired
		}
		request.Header.Set("X-Apple-Api-Key", c.apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	c.lastHTTP = response.StatusCode
	data, readErr := io.ReadAll(io.LimitReader(response.Body, (4<<20)+1))
	if readErr != nil || len(data) > 4<<20 {
		return nil, response.StatusCode, errors.New("Apple Account response was invalid")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return data, response.StatusCode, errors.New("Apple Account request was rejected")
	}
	return data, response.StatusCode, nil
}

func (c *AppleAccountClient) BeginReachableEmail(ctx context.Context, address string) error {
	address = strings.TrimSpace(address)
	if address == "" || !strings.Contains(address, "@") || len(address) > 254 {
		return errors.New("invalid reachable email address")
	}
	data, _, err := c.portalRequest(ctx, http.MethodPost, "/account/manage/email/alternate/add/verification", map[string]string{"address": address}, true)
	if err != nil {
		return err
	}
	var challenge struct {
		Address        string          `json:"address"`
		VerificationID json.RawMessage `json:"verificationId"`
	}
	if json.Unmarshal(data, &challenge) != nil || len(challenge.VerificationID) == 0 || string(challenge.VerificationID) == "null" {
		return errors.New("Apple Account did not return an email verification challenge")
	}
	if challenge.Address != "" && !strings.EqualFold(strings.TrimSpace(challenge.Address), address) {
		return errors.New("Apple Account returned a different email challenge")
	}
	c.pendingEmail, c.pendingVerify = address, append(json.RawMessage(nil), challenge.VerificationID...)
	return nil
}

func (c *AppleAccountClient) VerifyReachableEmail(ctx context.Context, code string) error {
	code = strings.TrimSpace(code)
	if c.pendingEmail == "" || len(c.pendingVerify) == 0 {
		return errors.New("no reachable email verification is pending")
	}
	if len(code) < 4 || len(code) > 8 {
		return errors.New("invalid reachable email verification code")
	}
	var verificationID any
	if json.Unmarshal(c.pendingVerify, &verificationID) != nil {
		return errors.New("invalid reachable email challenge")
	}
	payload := map[string]any{"address": c.pendingEmail, "verificationInfo": map[string]any{"id": verificationID, "answer": code}}
	if _, _, err := c.portalRequest(ctx, http.MethodPut, "/account/manage/email/alternate/verification", payload, true); err != nil {
		return err
	}
	if !c.HasReachableEmail(ctx, c.pendingEmail) {
		return errors.New("Apple Account did not confirm the reachable email")
	}
	c.pendingEmail, c.pendingVerify = "", nil
	return nil
}

func (c *AppleAccountClient) HasReachableEmail(ctx context.Context, address string) bool {
	data, _, err := c.portalRequest(ctx, http.MethodGet, "/account/manage", nil, false)
	if err != nil {
		return false
	}
	var manage struct {
		Account struct {
			Person struct {
				ReachableAtOptions struct {
					AlternateEmailAddresses []struct {
						Address string `json:"address"`
					} `json:"alternateEmailAddresses"`
				} `json:"reachableAtOptions"`
			} `json:"person"`
		} `json:"account"`
	}
	if json.Unmarshal(data, &manage) != nil {
		return false
	}
	for _, candidate := range manage.Account.Person.ReachableAtOptions.AlternateEmailAddresses {
		if strings.EqualFold(strings.TrimSpace(candidate.Address), strings.TrimSpace(address)) {
			return true
		}
	}
	return false
}

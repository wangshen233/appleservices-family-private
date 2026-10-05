package gsa

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/Laky-64/http"
	"howett.net/plist"
)

func (c *Client) twoFactorHeaders(dsid, idmsToken string) (map[string]string, error) {
	anisetteHeaders, err := c.anisetteHeaders()
	if err != nil {
		return nil, err
	}
	identityToken := base64.StdEncoding.EncodeToString([]byte(dsid + ":" + idmsToken))
	headers := map[string]string{
		"User-Agent":             "Xcode",
		"Accept-Language":        "en-us",
		"X-Apple-Identity-Token": identityToken,
		"X-Apple-App-Info":       "com.apple.gs.xcode.auth",
		"X-Xcode-Version":        "11.2 (11B41)",
	}
	for k, v := range anisetteHeaders {
		headers[k] = v
	}
	return headers, nil
}

func (c *Client) RequestTrustedDeviceCode(dsid, idmsToken string) error {
	headers, err := c.twoFactorHeaders(dsid, idmsToken)
	if err != nil {
		return err
	}
	headers["Content-Type"] = "text/x-xml-plist"
	headers["Accept"] = "text/x-xml-plist"

	result, err := http.ExecuteRequest(gsaBaseURL+"/auth/verify/trusteddevice",
		http.Method("GET"),
		http.Headers(headers),
		http.Transport(c.transport),
		http.CookieJar(c.cookieJar),
		http.Timeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("gsa: request trusted-device code: %w", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("gsa: request trusted-device code: status %d", result.StatusCode)
	}
	return nil
}

func (c *Client) SubmitTrustedDeviceCode(dsid, idmsToken, code string) error {
	headers, err := c.twoFactorHeaders(dsid, idmsToken)
	if err != nil {
		return err
	}
	headers["security-code"] = code

	result, err := http.ExecuteRequest(gsaBaseURL+"/grandslam/GsService2/validate",
		http.Method("GET"),
		http.Headers(headers),
		http.Transport(c.transport),
		http.CookieJar(c.cookieJar),
		http.Timeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("gsa: submit trusted-device code: %w", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("gsa: submit trusted-device code: status %d", result.StatusCode)
	}
	if len(bytes.TrimSpace(result.Body)) > 0 {
		var response map[string]any
		if _, err := plist.Unmarshal(result.Body, &response); err != nil {
			return fmt.Errorf("gsa: decode trusted-device validation response: %w", err)
		}
		if inner, ok := response["Response"].(map[string]any); ok {
			response = inner
		}
		if _, err := checkStatus(response, "validate", ""); err != nil {
			return err
		}
	}
	c.collectVerifiedTokens(result.Headers)
	return nil
}

type phoneNumberID struct {
	ID int `json:"id"`
}

type securityCodeBody struct {
	Code string `json:"code"`
}

type smsRequestBody struct {
	PhoneNumber phoneNumberID `json:"phoneNumber"`
	Mode        string        `json:"mode"`
}

type smsSubmitBody struct {
	PhoneNumber  phoneNumberID    `json:"phoneNumber"`
	Mode         string           `json:"mode"`
	SecurityCode securityCodeBody `json:"securityCode"`
}

var bootArgsScriptRe = regexp.MustCompile(`(?is)<script\b[^>]*\bclass\s*=\s*["'][^"']*\bboot_args\b[^"']*["'][^>]*>(.*?)</script\s*>`)

type bootArgs struct {
	TrustedPhoneNumbers []phoneNumberID `json:"trustedPhoneNumbers"`
	Direct              struct {
		PhoneNumberVerification struct {
			TrustedPhoneNumbers []phoneNumberID `json:"trustedPhoneNumbers"`
			TrustedPhoneNumber  struct {
				ID int `json:"id"`
			} `json:"trustedPhoneNumber"`
		} `json:"phoneNumberVerification"`
	} `json:"direct"`
}

func (c *Client) fetchTrustedPhoneID(dsid, idmsToken string) (int, error) {
	headers, err := c.twoFactorHeaders(dsid, idmsToken)
	if err != nil {
		return 0, err
	}
	headers["Content-Type"] = "application/json"
	headers["Accept"] = "application/json"
	headers["Connection"] = "keep-alive"

	result, err := http.ExecuteRequest(gsaBaseURL+"/auth",
		http.Method("GET"),
		http.Headers(headers),
		http.Transport(c.transport),
		http.CookieJar(c.cookieJar),
		http.Timeout(10*time.Second),
	)
	if err != nil {
		return 0, fmt.Errorf("gsa: fetch boot_args page: %w", err)
	}
	if result.StatusCode != 200 && result.StatusCode != 201 {
		return 0, fmt.Errorf("gsa: fetch boot_args page: status %d", result.StatusCode)
	}
	c.smsCodeAlreadySent = result.StatusCode == 201

	return parseTrustedPhoneID(result.Body)
}

func parseTrustedPhoneID(body []byte) (int, error) {
	var args bootArgs
	if err := json.Unmarshal(body, &args); err != nil {
		match := bootArgsScriptRe.FindSubmatch(body)
		if match == nil {
			return 0, errors.New("gsa: auth response is neither phone JSON nor an HTML boot_args page")
		}
		if err := json.Unmarshal(match[1], &args); err != nil {
			return 0, fmt.Errorf("gsa: decode boot_args: %w", err)
		}
	}
	id := args.Direct.PhoneNumberVerification.TrustedPhoneNumber.ID
	if id > 0 {
		return id, nil
	}
	for _, phone := range args.TrustedPhoneNumbers {
		if phone.ID > 0 {
			return phone.ID, nil
		}
	}
	for _, phone := range args.Direct.PhoneNumberVerification.TrustedPhoneNumbers {
		if phone.ID > 0 {
			return phone.ID, nil
		}
	}
	return 0, errors.New("gsa: auth response missing trusted phone number id")
}

func (c *Client) RequestSMSCode(dsid, idmsToken string) error {
	phoneID, err := c.fetchTrustedPhoneID(dsid, idmsToken)
	if err != nil {
		return err
	}
	if c.smsCodeAlreadySent {
		c.smsPhoneID = phoneID
		return nil
	}

	headers, err := c.twoFactorHeaders(dsid, idmsToken)
	if err != nil {
		return err
	}
	headers["Content-Type"] = "application/json"

	body, err := json.Marshal(smsRequestBody{PhoneNumber: phoneNumberID{ID: phoneID}, Mode: "sms"})
	if err != nil {
		return fmt.Errorf("gsa: encode sms request: %w", err)
	}
	headers["Accept"] = "application/json"

	result, err := http.ExecuteRequest(gsaBaseURL+"/auth/verify/phone",
		http.Method("PUT"),
		http.Headers(headers),
		http.Body(body),
		http.Transport(c.transport),
		http.CookieJar(c.cookieJar),
		http.Timeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("gsa: request sms code: %w", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("gsa: request sms code: status %d", result.StatusCode)
	}
	c.smsPhoneID = phoneID
	return nil
}

func (c *Client) SubmitSMSCode(dsid, idmsToken, code string) error {
	phoneID := c.smsPhoneID
	if phoneID == 0 {
		var err error
		phoneID, err = c.fetchTrustedPhoneID(dsid, idmsToken)
		if err != nil {
			return err
		}
	}

	headers, err := c.twoFactorHeaders(dsid, idmsToken)
	if err != nil {
		return err
	}
	headers["Content-Type"] = "application/json"

	body, err := json.Marshal(smsSubmitBody{
		PhoneNumber:  phoneNumberID{ID: phoneID},
		Mode:         "sms",
		SecurityCode: securityCodeBody{Code: code},
	})
	if err != nil {
		return fmt.Errorf("gsa: encode sms submit: %w", err)
	}
	headers["Accept"] = "application/json"

	result, err := http.ExecuteRequest(gsaBaseURL+"/auth/verify/phone/securitycode",
		http.Method("POST"),
		http.Headers(headers),
		http.Body(body),
		http.Transport(c.transport),
		http.CookieJar(c.cookieJar),
		http.Timeout(10*time.Second),
	)
	if err != nil {
		return fmt.Errorf("gsa: submit sms code: %w", err)
	}
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		return fmt.Errorf("gsa: submit sms code: status %d", result.StatusCode)
	}
	c.collectVerifiedTokens(result.Headers)
	return nil
}

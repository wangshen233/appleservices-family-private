package gsa

import (
	"encoding/base64"
	"maps"
	"strings"
)

// Apple can issue the authenticated session directly in code-response headers.
// Keep every header value; PET's first field is an account ID, not a service name.
func (c *Client) collectVerifiedTokens(headers map[string][]string) {
	for header, values := range headers {
		if !strings.EqualFold(header, "X-Apple-PE-Token") && !strings.EqualFold(header, "X-Apple-HB-Token") && !strings.EqualFold(header, "X-Apple-GS-Token") {
			continue
		}
		for _, value := range values {
			data, err := base64.StdEncoding.DecodeString(value)
			if err != nil {
				data, err = base64.RawStdEncoding.DecodeString(value)
			}
			if err != nil {
				continue
			}
			fields := strings.Split(string(data), ":")
			if len(fields) < 2 || fields[1] == "" {
				continue
			}
			service := fields[0]
			if strings.EqualFold(header, "X-Apple-PE-Token") {
				service = "com.apple.gs.idms.pet"
			}
			if strings.EqualFold(header, "X-Apple-HB-Token") {
				service = "com.apple.gs.idms.hb"
			}
			if !strings.HasPrefix(service, "com.apple.") {
				continue
			}
			if c.verifiedTokens == nil {
				c.verifiedTokens = map[string]string{}
			}
			c.verifiedTokens[service] = fields[1]
		}
	}
}

// VerifiedLoginResult is available only when Apple issued a PET after accepting
// a verification code. No new password handshake is necessary in that case.
func (c *Client) VerifiedLoginResult() *LoginResult {
	if c.loginPayload == nil || c.verifiedTokens["com.apple.gs.idms.pet"] == "" {
		return nil
	}
	payload := maps.Clone(c.loginPayload)
	original, _ := payload["t"].(map[string]any)
	tokens := maps.Clone(original)
	if tokens == nil {
		tokens = map[string]any{}
	}
	for service, token := range c.verifiedTokens {
		tokens[service] = map[string]any{"token": token}
	}
	payload["t"] = tokens
	return &LoginResult{SessionPayload: payload}
}

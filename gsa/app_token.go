package gsa

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"

	"howett.net/plist"
)

// FetchAppToken requests one service-scoped credential from the live SRP
// master session. Protocol reference: Dadoum/apple-crates, grandslam/account.rs.
// Master credentials are neither logged nor persisted by this method.
func (c *Client) FetchAppToken(spd map[string]any, service string) (string, error) {
	adsid, _ := spd["adsid"].(string)
	idms, _ := spd["GsIdmsToken"].(string)
	key, _ := spd["sk"].([]byte)
	cookie, _ := spd["c"].([]byte)
	if adsid == "" || idms == "" || len(key) != 32 || len(cookie) == 0 {
		return "", errors.New("gsa: live app-token master credentials unavailable")
	}
	if !strings.HasPrefix(service, "com.apple.gs.") || len(service) > 150 {
		return "", errors.New("gsa: invalid app-token service")
	}
	cpd, err := c.buildCPD()
	if err != nil {
		return "", err
	}
	checksum := hmac.New(sha256.New, key)
	checksum.Write([]byte("apptokens"))
	checksum.Write([]byte(adsid))
	checksum.Write([]byte(service))
	resp, err := c.doRequest(map[string]any{"o": "apptokens", "u": adsid, "t": idms, "c": cookie, "app": []string{service}, "checksum": checksum.Sum(nil), "cpd": cpd})
	if err != nil {
		return "", err
	}
	if _, err := checkStatus(resp, "apptokens", ""); err != nil {
		return "", err
	}
	encrypted, _ := resp["et"].([]byte)
	if len(encrypted) < 35 || string(encrypted[:3]) != "XYZ" {
		return "", errors.New("gsa: invalid encrypted app-token envelope")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", errors.New("gsa: invalid app-token key")
	}
	gcm, err := cipher.NewGCMWithNonceSize(block, 16)
	if err != nil {
		return "", errors.New("gsa: cannot create app-token cipher")
	}
	plain, err := gcm.Open(nil, encrypted[3:19], encrypted[19:], encrypted[:3])
	if err != nil {
		return "", errors.New("gsa: app-token integrity check failed")
	}
	var decoded map[string]any
	if _, err := plist.Unmarshal(plain, &decoded); err != nil {
		return "", errors.New("gsa: decrypted app-token payload is invalid")
	}
	tokens, _ := decoded["t"].(map[string]any)
	entry, _ := tokens[service].(map[string]any)
	token, _ := entry["token"].(string)
	if token == "" {
		return "", errors.New("gsa: requested app token absent")
	}
	return token, nil
}

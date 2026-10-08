// Package accountsetup prepares the existing-account terms update described
// by Apple's setup UI. Preparing a plan does not send it or grant consent.
package accountsetup

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"golang.org/x/net/html"
)

type TermsPlan struct {
	Country, ICloudVersion, MediaVersion     string
	endpoint, authorization, clientInfo, nas string
	values                                   url.Values
}

func flag(page, key string, expected bool) bool {
	want := "false"
	if expected {
		want = "true"
	}
	return regexp.MustCompile(`\b` + key + `\s*:\s*` + want + `\b`).MatchString(page)
}

func literal(page, key string) string {
	match := regexp.MustCompile(`\b` + key + `\s*:\s*("(?:[^"\\]|\\.)*")`).FindStringSubmatch(page)
	if len(match) != 2 {
		return ""
	}
	var value string
	if json.Unmarshal([]byte(match[1]), &value) != nil {
		return ""
	}
	return value
}

func PrepareTerms(page []byte, appleID, udid string, apsToken []byte, timezone string) (*TermsPlan, error) {
	source := string(page)
	if !flag(source, "updateMode", true) || !flag(source, "deviceQualified", true) || !flag(source, "internalServerError", false) {
		return nil, errors.New("accountsetup: page is not a qualified existing-account update")
	}
	if !strings.EqualFold(literal(source, "appleId"), appleID) {
		return nil, errors.New("accountsetup: page belongs to another account")
	}
	authorization := literal(source, "authHeader")
	encoded, ok := strings.CutPrefix(authorization, "Basic ")
	if !ok {
		return nil, errors.New("accountsetup: expected Basic setup authentication")
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, errors.New("accountsetup: invalid setup authentication")
	}
	owner, token, ok := strings.Cut(string(decoded), ":")
	if !ok || token == "" || !strings.EqualFold(owner, appleID) {
		return nil, errors.New("accountsetup: setup credential account mismatch")
	}
	if len(udid) != 64 || len(apsToken) != 32 {
		return nil, errors.New("accountsetup: incomplete native device data")
	}
	if _, err := hex.DecodeString(udid); err != nil {
		return nil, errors.New("accountsetup: invalid native UDID")
	}
	root, err := html.Parse(bytes.NewReader(page))
	if err != nil {
		return nil, errors.New("accountsetup: invalid setup HTML")
	}
	fields := map[string]string{}
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "input" {
			var id, value, kind string
			for _, a := range n.Attr {
				switch a.Key {
				case "id":
					id = a.Val
				case "value":
					value = a.Val
				case "type":
					kind = a.Val
				}
			}
			if kind == "hidden" {
				fields[id] = value
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(root)
	iCloud, media := fields["tosversion_field"], fields["amstosversion_field"]
	version := regexp.MustCompile(`^[0-9]{1,20}$`)
	if !version.MatchString(iCloud) || !version.MatchString(media) {
		return nil, errors.New("accountsetup: missing terms versions")
	}
	country := fields["countryCode_field"]
	if !regexp.MustCompile(`^[A-Z]{2}$`).MatchString(country) {
		return nil, errors.New("accountsetup: missing terms region")
	}
	clientInfo := literal(source, "clientInfo")
	if clientInfo == "" {
		return nil, errors.New("accountsetup: missing setup client identity")
	}
	values := url.Values{
		"udid": {udid}, "aps-token": {strings.ToUpper(hex.EncodeToString(apsToken))},
		"mobileme-setup-token": {fields["setuptoken_field"]}, "tos-version": {iCloud}, "ams-tos-version": {media},
		"macos-tos-version": {fields["macostosversion_field"]}, "warranty-tos-version": {fields["warrantytosversion_field"]},
		"language": {"en"}, "timezone": {timezone}, "certQualify": {fields["certQualify_field"]}, "certId": {fields["certId_field"]},
	}
	if parental := fields["parentalConsentversion_field"]; parental != "" {
		values.Set("parentalConsent-version", parental)
	}
	return &TermsPlan{Country: country, ICloudVersion: iCloud, MediaVersion: media,
		endpoint:      "https://setup.icloud.com/setup/web/basic_auth_update_account/" + url.PathEscape(appleID) + "/" + url.PathEscape(udid),
		authorization: authorization, clientInfo: clientInfo, nas: literal(source, "nasQualify"), values: values}, nil
}

// Request requires explicit consent. Callers must use their existing setup
// cookie jar and verified Apple transport and must not retry ambiguous writes.
func (p *TermsPlan) Request(consent bool) (*http.Request, error) {
	if !consent {
		return nil, errors.New("accountsetup: explicit terms consent required")
	}
	r, err := http.NewRequest(http.MethodPost, p.endpoint, strings.NewReader(p.values.Encode()))
	if err != nil {
		return nil, errors.New("accountsetup: cannot build terms update")
	}
	r.Header.Set("Authorization", p.authorization)
	r.Header.Set("X-Mme-Client-Info", p.clientInfo)
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	r.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	r.Header.Set("X-Requested-With", "XMLHttpRequest")
	r.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko)")
	r.Header.Set("Origin", "https://setup.icloud.com")
	r.Header.Set("Referer", "https://setup.icloud.com/setup/update_account_ui")
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	r.Header.Set("Sec-Fetch-Mode", "cors")
	r.Header.Set("Sec-Fetch-Dest", "empty")
	if p.nas != "" {
		r.Header.Set("X-Mme-Nas-Qualify", p.nas)
	}
	return r, nil
}
